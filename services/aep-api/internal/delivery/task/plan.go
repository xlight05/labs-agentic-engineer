// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package task

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/aep/aep-api/internal/clients/agentsvc"
	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/platform/taskplan"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// NOTE: playground/src/engine/compose.ts carries a verbatim copy of this
// string (and mirrors renderPlanContext); its steer-parity test fails when
// they drift. Update both together.
//
// planInstruction is the steering directive the BFF composes server-side (§9.1:
// the request body is empty; the BFF assembles the whole generation directive).
// The design/requirements content is NOT inlined anymore — the agents service
// reads it from the workspace snapshot (shared-volume-clone D9); the existing-
// task renders + lineage diffs are appended to this instruction (they are
// platform state, not repository files, so they cannot ride in the snapshot —
// see renderPlanContext).
const planInstruction = "Plan the implementation Tasks for this project. Load the task-planning skill and follow it: create one Task per design component with planTask, wire dependsOn by component name, and write each Task's body with updateTask in the same turn. The design is under specs/design/ and the requirements under specs/requirements/. Existing open Tasks (if any) are listed at the end of this message for reference — add Tasks ONLY for components they do not cover, and do not recreate or update the listed Tasks in this turn. Never invent a component the design does not define."

// PlanService assembles the plan-turn context, starts the upstream turn, and
// hands back a PlanSession the HTTP edge streams. One active plan turn per
// project is enforced by an in-process in-flight set (§6) plus the upstream 409
// passthrough. (This guard is a separate concern from genai's durable
// one-active-turn row — plan turns commit nothing, so the in-process set is
// enough.)
type PlanService struct {
	repos      RepoResolver
	versions   VersionReader
	git        GitReader
	keys       AnthropicKeyResolver
	client     TurnClient
	issues     IssueClient
	snapshots  sourcecontrol.SnapshotProvider
	skillsRepo SkillsRepoResolver
	// paths resolves each component's appPath for the planned Task's body.
	// Optional; nil omits the App Path line.
	paths ComponentPathReader

	inflight sync.Map // projectKey → struct{}
}

// SetComponentPaths wires the design's component → appPath reader so a planned
// Task's body carries the App Path the agent works in. A nil reader is a
// documented no-op (the line is omitted).
func (s *PlanService) SetComponentPaths(r ComponentPathReader) { s.paths = r }

// NewPlanService wires the plan service. git/snapshots/skillsRepo back the
// workspace dispatch (snapshot refs + lineage diffs).
func NewPlanService(repos RepoResolver, versions VersionReader, git GitReader, keys AnthropicKeyResolver, client TurnClient, issues IssueClient, snapshots sourcecontrol.SnapshotProvider, skillsRepo SkillsRepoResolver) *PlanService {
	return &PlanService{repos: repos, versions: versions, git: git, keys: keys, client: client, issues: issues, snapshots: snapshots, skillsRepo: skillsRepo}
}

// planSession is a started plan turn: the raw upstream SSE body, the tap that
// executes tool frames against GitHub, and a release for the in-flight lock.
type planSession struct {
	body    io.ReadCloser
	tap     *planTap
	release func()
}

// drain runs the turn to completion and reports how many GitHub writes the tap
// could not land. The build click has no SSE consumer — planning is the
// detached half of the plan path — so the turn is driven to the end here and
// the failure count decides whether the run it just planned is honest.
//
// It survives a caller that walks away: the tap keeps reading upstream so every
// write lands, and the in-flight lock releases when this returns.
func (s *planSession) drain() int {
	defer s.release()
	s.tap.Stream(s.body, io.Discard, func() {})
	return s.tap.failures
}

// PlanIntoMilestone plans the version's Tasks straight into its milestone and
// waits for the turn to finish. Every issue the turn mints joins the milestone
// AT CREATION (1+N calls) with the `aep` working-set label and a prose body.
//
// It is the plan path's half of the build click. A write the tap could not land
// is an error rather than a warning: the run this plan feeds is about to be
// supervised against the milestone's contents, so a silently short plan would
// become a run that settles early.
// Pre-stream failures are typed errors (ErrNoSpecVersion, ErrNoAnthropicKey,
// ErrProjectRepoNotFound, ErrPlanInProgress, ErrSkillsRepoUnavailable) or an
// *agentsvc.UpstreamError.
func (s *PlanService) PlanIntoMilestone(ctx context.Context, orgID, projectID string, milestoneNumber int) error {
	session, err := s.startPlan(ctx, orgID, projectID, milestoneNumber)
	if err != nil {
		return err
	}
	if failures := session.drain(); failures > 0 {
		return fmt.Errorf("plan: %d issue write(s) failed for milestone %d", failures, milestoneNumber)
	}
	return nil
}

// startPlan takes the per-project plan lock and starts one turn. Every minted
// issue joins milestoneNumber at creation, so the plan costs 1+N calls.
func (s *PlanService) startPlan(ctx context.Context, orgID, projectID string, milestoneNumber int) (*planSession, error) {
	key := orgID + "/" + projectID
	if _, loaded := s.inflight.LoadOrStore(key, struct{}{}); loaded {
		return nil, ErrPlanInProgress
	}
	release := func() { s.inflight.Delete(key) }
	session, err := s.startPlanLocked(ctx, orgID, projectID, milestoneNumber, release)
	if err != nil {
		release()
		return nil, err
	}
	return session, nil
}

func (s *PlanService) startPlanLocked(ctx context.Context, orgID, projectID string, milestoneNumber int, release func()) (*planSession, error) {
	// Resolved directly (not via resolveProjectRepo): the plan turn keys on
	// the workspace ref, so it needs no owner/name split — only a ready row.
	repo, err := s.repos.GetRepo(ctx, orgID, projectID)
	if err != nil {
		if errors.Is(err, sourcecontrol.ErrRepoNotFound) {
			return nil, ErrProjectRepoNotFound
		}
		return nil, err
	}
	if repo == nil {
		return nil, ErrProjectRepoNotFound
	}
	// The plan turn reads its context from a workspace snapshot — a repo that
	// is not ready yet cannot back one.
	if repo.Status != "" && repo.Status != "ready" {
		return nil, ErrProjectRepoNotFound
	}

	// Gate: a versioned (tagged) spec must exist (§6, build-first). The `v<N>`
	// tag is cut by the build endpoint AFTER the whole-spec hard gate, so its
	// presence certifies a buildable requirements+design pair.
	reqVersions, err := s.versions.ListRequirementsVersions(ctx, orgID, projectID)
	if err != nil || len(reqVersions) == 0 {
		return nil, ErrNoSpecVersion
	}

	apiKey, err := s.keys(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("resolve anthropic key: %w", err)
	}
	if apiKey == "" {
		return nil, ErrNoAnthropicKey
	}

	ref, err := sourcecontrol.ResolveWorkspaceRef(ctx, s.git.Resolver(), orgID, repo)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace ref: %w", err)
	}

	// Existing Tasks → instruction context + tap preload + dedupe slugs. These
	// are platform state (GitHub issues), not repository files, so they ride in
	// the instruction — the snapshot carries only committed content.
	//
	// A version plans FRESH from the new spec (§6: supersede, no carry-over), so
	// the only context is the milestone's OWN issues — empty on a first pass and
	// non-empty only on a re-plan or a crash re-run, exactly the cases dedupe
	// exists for.
	contextFiles := map[string]string{}
	preload, slugs := s.assembleMilestoneTasks(ctx, orgID, projectID, milestoneNumber, contextFiles)
	// Freeze the set of issue numbers the agent actually received as context: an
	// updateTask{issueNumber} ref is fenced to it (a hallucinated / out-of-context
	// number must never be written — plan_tap.resolveRef).
	contextNumbers := make(map[int]bool, len(preload))
	for n := range preload {
		contextNumbers[n] = true
	}

	// Workspace snapshot refs (D9): the design/requirements context is read
	// from snapshots/<baseRef>/; the task-planning skill is a flow skill
	// seeded into the org's _skills repo (Phase 1), read from its snapshot.
	ws := s.git.Workspace()
	baseRef, err := ws.Head(ctx, ref, "")
	if err != nil {
		return nil, fmt.Errorf("resolve base ref: %w", err)
	}
	// Skills resolve failures are typed: both arms mean the org's _skills repo
	// is unusable right now (row missing/unprovisionable, or the backing repo
	// gone/unreachable — e.g. deleted externally under a lingering row). The
	// edge maps this to a logged 503 rather than an opaque 500.
	skillsRow, err := s.skillsRepo(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve repo row: %w", ErrSkillsRepoUnavailable, err)
	}
	skillsRepoRef := sourcecontrol.WorkspaceRefFor(orgID, skillsRow, ref.Cred)
	skillsRef, err := ws.Head(ctx, skillsRepoRef, "")
	if err != nil {
		return nil, fmt.Errorf("%w: resolve head: %w", ErrSkillsRepoUnavailable, err)
	}
	if err := s.snapshots.Ensure(ctx, ref, baseRef); err != nil {
		return nil, fmt.Errorf("ensure repo snapshot: %w", err)
	}
	if err := s.snapshots.Ensure(ctx, skillsRepoRef, skillsRef); err != nil {
		return nil, fmt.Errorf("ensure skills snapshot: %w", err)
	}

	// A fresh, namespaced conversation id per plan turn — plan turns are one-shot
	// (never rehydrated), so the id only needs to be unique within the tenant.
	conversationID := agentsvc.ConversationID(orgID, projectID, "task-plan",
		strconv.FormatInt(time.Now().UnixNano(), 10))

	// Detached context so the turn drains even if the client disconnects (§6).
	detached := context.WithoutCancel(ctx)
	body, err := s.client.Turn(detached, conversationID, orgID, apiKey, agentsvc.TurnRequest{
		Instruction: planInstruction + renderPlanContext(contextFiles),
		Workspace: agentsvc.WorkspaceRef{
			ConversationID: conversationID,
			TurnID:         uuid.NewString(),
			RepoSlug:       ref.RepoSlug,
			Ref:            baseRef,
			SkillsRef:      skillsRef,
		},
		Toolset: "task-plan",
	})
	if err != nil {
		return nil, err // typed *agentsvc.UpstreamError (409 → plan_in_progress passthrough)
	}

	tap := newPlanTap(detached, orgID, projectID, s.issues)
	tap.milestone = milestoneNumber
	tap.appPaths = s.componentPaths(ctx, orgID, projectID)
	tap.state = preload
	tap.existingSlugs = slugs
	tap.contextNumbers = contextNumbers

	return &planSession{body: body, tap: tap, release: release}, nil
}

// componentPaths reads each component's appPath, lowercased for lookup.
// Best-effort: a design-read hiccup costs the App Path line, never the plan.
func (s *PlanService) componentPaths(ctx context.Context, orgID, projectID string) map[string]string {
	if s.paths == nil {
		return nil
	}
	raw, err := s.paths.ComponentPaths(ctx, orgID, projectID)
	if err != nil {
		slog.WarnContext(ctx, "plan: read component app paths failed", "project", projectID, "error", err)
		return nil
	}
	out := make(map[string]string, len(raw))
	for name, path := range raw {
		out[strings.ToLower(strings.TrimSpace(name))] = path
	}
	return out
}

// assembleMilestoneTasks preloads the milestone's OWN issues: their title slugs
// are the additive-only dedupe set, and each renders as a context file so a
// re-plan can see (and updateTask) what the version already holds. Best-effort:
// a read failure plans as if the milestone were empty, which at worst re-mints
// an issue the human can close.
func (s *PlanService) assembleMilestoneTasks(ctx context.Context, orgID, projectID string, milestoneNumber int, files map[string]string) (map[int]plannedTask, map[string]bool) {
	preload := map[int]plannedTask{}
	slugs := map[string]bool{}

	issues, err := s.issues.ListMilestoneIssues(ctx, orgID, projectID, sourcecontrol.MilestoneIssuesFilter{
		Number: milestoneNumber,
		State:  "all",
	})
	if err != nil {
		slog.WarnContext(ctx, "plan: list milestone issues failed", "project", projectID, "milestone", milestoneNumber, "error", err)
		return preload, slugs
	}
	for _, issue := range issues {
		if slug := titleSlug(issue.Title); slug != "" {
			slugs[slug] = true
		}
		// Gates and the validation issue are not the planner's to touch, and a
		// ledger-only human issue is not a Task — none of them belong in the
		// context set an updateTask ref is fenced to.
		if delivery.HasLabel(issue.Labels, delivery.LabelProvisionGate) ||
			delivery.HasLabel(issue.Labels, delivery.LabelValidationWork) ||
			!delivery.HasLabel(issue.Labels, delivery.LabelAgentWork) {
			continue
		}
		if !strings.EqualFold(issue.State, "open") {
			continue
		}
		path, content := taskplan.RenderTaskContextFile(taskplan.TaskContextFile{
			IssueNumber: issue.Number,
			Title:       issue.Title,
			Body:        issue.Body,
		})
		files[path] = content
		preload[issue.Number] = plannedTask{Body: issue.Body}
	}
	return preload, slugs
}

// renderPlanContext renders the milestone's existing-task context files as
// deterministic instruction sections. They keep their historical tasks/<n>.md
// names so the model's mental layout is unchanged.
func renderPlanContext(files map[string]string) string {
	if len(files) == 0 {
		return ""
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var sb strings.Builder
	sb.WriteString("\n\n## Existing open Tasks in this version (reference)\n")
	for _, p := range paths {
		fmt.Fprintf(&sb, "\n--- %s ---\n%s\n", p, files[p])
	}
	return sb.String()
}
