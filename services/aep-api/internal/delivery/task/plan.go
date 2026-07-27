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
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
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
	// validationMinter mints the project's single aep:validation Task right
	// after the plan tap drains — the validation task is born in the SAME
	// planning pass as the implementation tasks (validation-phase). Consumer-
	// side port wired via SetValidationIssueMinter; nil is a documented no-op.
	//
	// It runs for a plan with NO milestone only. A milestone run mints its
	// validation issue at deployed-green, from the supervisor — minting it at
	// plan time would put an issue in the working set that nothing can work
	// until every component is deployed.
	validationMinter validationIssueMinter
	// paths resolves each component's appPath for the planned Task's body.
	// Optional; nil omits the App Path line.
	paths ComponentPathReader

	inflight sync.Map // projectKey → struct{}
}

// validationIssueMinter is the plan service's narrow consumer port for the
// validation feature: after a plan turn creates the implementation issues, it
// ensures the project's aep:validation Task exists (idempotent — dedups on the
// open issue; no-op when specs/validation/validation-criteria.json is absent).
// *validation.Service satisfies it; wired at the composition root. Minting
// here (not at design approval) keeps the validation issue OUT of the plan
// turn's existing-task context.
type validationIssueMinter interface {
	EnsureValidationIssue(ctx context.Context, orgID, projectID, designTag string) error
}

// SetValidationIssueMinter wires the validation feature so the plan session
// mints the project's aep:validation Task after the tap drains. A nil minter
// is a documented no-op.
func (s *PlanService) SetValidationIssueMinter(m validationIssueMinter) {
	s.validationMinter = m
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

// PlanSession is a started plan turn: the raw upstream SSE body, the tap that
// executes tool frames against GitHub, a release for the in-flight lock, and
// the (optional) validation minter that runs once the tap drains.
type PlanSession struct {
	body      io.ReadCloser
	tap       *planTap
	release   func()
	minter    validationIssueMinter
	designTag string
}

// Stream forwards the turn to w verbatim while the tap performs the GitHub
// writes, then mints the project's aep:validation Task (best-effort — the
// implementation issues now exist, so the validation task is born in the same
// planning pass; the devflow validating phase re-ensures idempotently as the
// safety net), then releases the per-project in-flight lock. Survives client
// disconnect (the tap drains upstream).
func (s *PlanSession) Stream(w io.Writer, flush func()) {
	defer s.release()
	s.tap.Stream(s.body, w, flush)
	if s.minter != nil {
		if err := s.minter.EnsureValidationIssue(s.tap.ctx, s.tap.orgID, s.tap.projectID, s.designTag); err != nil {
			slog.WarnContext(s.tap.ctx, "plan: validation issue minting after plan failed", "project", s.tap.projectID, "error", err)
		}
	}
}

// Drain runs the turn to completion with no client attached and reports how
// many GitHub writes the tap could not land. It is the plan path's entry: the
// build click has no SSE consumer, so the turn is driven to the end here and
// the failure count decides whether the run it just planned is honest.
func (s *PlanSession) Drain() int {
	s.Stream(io.Discard, func() {})
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
func (s *PlanService) PlanIntoMilestone(ctx context.Context, orgID, projectID string, milestoneNumber int) error {
	session, err := s.startPlan(ctx, orgID, projectID, milestoneNumber)
	if err != nil {
		return err
	}
	if failures := session.Drain(); failures > 0 {
		return fmt.Errorf("plan: %d issue write(s) failed for milestone %d", failures, milestoneNumber)
	}
	return nil
}

// StartPlan assembles context and starts the plan turn. Pre-stream failures are
// typed errors (ErrNoSpecVersion, ErrNoAnthropicKey, ErrProjectRepoNotFound,
// ErrPlanInProgress) or an *agentsvc.UpstreamError; on any pre-stream failure
// the in-flight lock is released before returning.
func (s *PlanService) StartPlan(ctx context.Context, orgID, projectID string) (*PlanSession, error) {
	return s.startPlan(ctx, orgID, projectID, 0)
}

// startPlan takes the per-project plan lock and starts one turn. milestoneNumber
// is the milestone every minted issue joins; zero means the caller planned no
// milestone (the pre-milestone dev-workflow path), and creations are left
// unassigned.
func (s *PlanService) startPlan(ctx context.Context, orgID, projectID string, milestoneNumber int) (*PlanSession, error) {
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

func (s *PlanService) startPlanLocked(ctx context.Context, orgID, projectID string, milestoneNumber int, release func()) (*PlanSession, error) {
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
	currentSpecTag := reqVersions[0].Tag

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
	contextFiles := map[string]string{}
	var preload map[int]plannedTask
	var slugs map[string]bool
	if milestoneNumber > 0 {
		// A milestone plans FRESH from the new spec (§6: supersede, no
		// carry-over), so the only context is the milestone's OWN issues — which
		// is empty on a first pass and non-empty only on a re-plan or a crash
		// re-run, exactly the cases dedupe exists for.
		preload, slugs = s.assembleMilestoneTasks(ctx, orgID, projectID, milestoneNumber, contextFiles)
	} else {
		var olderTags map[string]bool
		preload, slugs, olderTags = s.assembleExistingTasks(ctx, orgID, projectID, currentSpecTag, contextFiles)
		s.appendLineageDiffs(ctx, ref, currentSpecTag, olderTags, contextFiles)
	}
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

	session := &PlanSession{body: body, tap: tap, release: release, designTag: currentSpecTag}
	if milestoneNumber == 0 {
		// The validation issue is born at plan time only on the pre-milestone
		// path. A milestone run mints it at deployed-green instead.
		session.minter = s.validationMinter
	}
	return session, nil
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
		if slug := taskmeta.TitleSlug(issue.Title); slug != "" {
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

// assembleExistingTasks is the PRE-MILESTONE context assembler: it renders each
// open Task as a tasks/<n>.md context file, preloads the tap state for
// updateTask{issueNumber}, collects the title-slug dedupe set, and reports the
// distinct older lineage tags (spec `v<N>` or legacy design `v<N>-<M>`) whose
// lineage diff the assembler should include (§6). Milestone plans use
// assembleMilestoneTasks instead — membership, not a label query.
func (s *PlanService) assembleExistingTasks(ctx context.Context, orgID, projectID, currentSpecTag string, files map[string]string) (map[int]plannedTask, map[string]bool, map[string]bool) {
	preload := map[int]plannedTask{}
	slugs := map[string]bool{}
	olderTags := map[string]bool{}

	issues, err := s.issues.ListIssues(ctx, orgID, projectID, []string{taskmeta.LabelMarker})
	if err != nil {
		slog.WarnContext(ctx, "plan: list existing tasks failed", "error", err)
		return preload, slugs, olderTags
	}
	for _, issue := range issues {
		if !strings.EqualFold(issue.State, "open") {
			continue
		}
		// The validation task is not plan context: it is platform-minted after
		// the plan turn (component-less, dependsOn everything), so showing it
		// would only confuse the component-based planning skill — and preloading
		// it into contextNumbers would let an updateTask clobber it.
		if taskmeta.ParseLabels(issue.Labels).Class == taskmeta.ClassValidation {
			continue
		}
		block, human, berr := taskmeta.ParseBody(issue.Body)
		if berr != nil {
			continue // mangled/missing block — the events handler flags it
		}
		cf := taskplan.TaskContextFile{
			IssueNumber: issue.Number,
			Component:   block.Component,
			Title:       issue.Title,
			DependsOn:   block.DependsOn,
			Origin:      block.Origin,
			SpecTag:     block.SpecTag,
			DesignTag:   block.DesignTag,
			Body:        human.Body,
		}
		path, content := taskplan.RenderTaskContextFile(cf)
		files[path] = content

		preload[issue.Number] = plannedTask{
			Component: block.Component,
			DependsOn: block.DependsOn,
			Rationale: human.Rationale,
			Body:      human.Body,
		}
		if slug := taskmeta.TitleSlug(issue.Title); slug != "" {
			slugs[slug] = true
		}
		if block.DesignTag != "" && block.DesignTag != currentSpecTag {
			olderTags[block.DesignTag] = true
		}
	}
	return preload, slugs, olderTags
}

// appendLineageDiffs includes the spec delta between each older lineage tag
// (spec or legacy design — both are real git tags) and the current spec tag so
// incremental planning reasons over the real change (§6 — Workspace.Diff on
// the local mirror, was GitHub CompareRefs).
func (s *PlanService) appendLineageDiffs(ctx context.Context, ref sourcecontrol.RepoRef, currentSpecTag string, olderTags map[string]bool, files map[string]string) {
	if s.git == nil || len(olderTags) == 0 {
		return
	}
	for oldTag := range olderTags {
		cmp, cerr := s.git.Workspace().Diff(ctx, ref, oldTag, currentSpecTag)
		if cerr != nil {
			slog.WarnContext(ctx, "plan: lineage compare failed", "from", oldTag, "to", currentSpecTag, "error", cerr)
			continue
		}
		files["context/lineage-diff-"+oldTag+".md"] = renderCompare(oldTag, currentSpecTag, cmp)
	}
}

// renderPlanContext renders the existing-task + lineage-diff context files as
// deterministic instruction sections. They keep their historical
// tasks/<n>.md / context/… names so the model's mental layout is unchanged.
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
	sb.WriteString("\n\n## Existing open Tasks and lineage diffs (reference)\n")
	for _, p := range paths {
		fmt.Fprintf(&sb, "\n--- %s ---\n%s\n", p, files[p])
	}
	return sb.String()
}

// renderCompare renders a compare result as a compact markdown summary for the
// planner's context (changed files + hunks).
func renderCompare(from, to string, cmp *sourcecontrol.CompareResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Lineage diff: %s → %s\n\n", from, to)
	fmt.Fprintf(&sb, "Status: %s (%d commit(s), +%d/-%d)\n\n", cmp.Status, cmp.TotalCommits, cmp.AheadBy, cmp.BehindBy)
	if cmp.Truncated {
		sb.WriteString("> NOTE: this compare was capped at a file limit — the change list below is INCOMPLETE. Treat components not shown as possibly-changed and re-verify against the current design rather than assuming they are untouched.\n\n")
	}
	for _, f := range cmp.Files {
		fmt.Fprintf(&sb, "## %s (%s, +%d/-%d)\n", f.Filename, f.Status, f.Additions, f.Deletions)
		if f.Patch != "" {
			sb.WriteString("```diff\n")
			sb.WriteString(f.Patch)
			if !strings.HasSuffix(f.Patch, "\n") {
				sb.WriteByte('\n')
			}
			sb.WriteString("```\n")
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
