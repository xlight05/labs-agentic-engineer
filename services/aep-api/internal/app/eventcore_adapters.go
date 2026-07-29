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

package app

import (
	"context"
	"log/slog"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/delivery/eventcore"
	"github.com/wso2/aep/aep-api/internal/platform/gitfs/naming"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// The composition-root adapters behind the event plane's consumer ports. Each
// is a thin projection of a repository or client the package must not name.

// eventcoreRuns projects the milestone-run repository onto the event plane's
// RunStore: it turns the repository's plain reads into the "is there a live
// run here?" questions every handler gates on.
//
// The non-terminal filtering is done here rather than as another repository
// query because "live" is the event plane's word for it, and the repository
// already exposes the rows it means.
type eventcoreRuns struct {
	runs delivery.MilestoneRunRepository
}

func (a eventcoreRuns) LiveRunForMilestone(ctx context.Context, orgID, projectID string, milestoneNumber int) (*delivery.MilestoneRun, error) {
	rows, err := a.runs.ListByMilestone(ctx, orgID, projectID, milestoneNumber)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if !delivery.IsTerminalRunState(rows[i].State) {
			return &rows[i], nil
		}
	}
	return nil, nil
}

func (a eventcoreRuns) LiveRunsForProject(ctx context.Context, orgID, projectID string) ([]delivery.MilestoneRun, error) {
	rows, err := a.runs.ListByProject(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	live := make([]delivery.MilestoneRun, 0, len(rows))
	for i := range rows {
		if !delivery.IsTerminalRunState(rows[i].State) {
			live = append(live, rows[i])
		}
	}
	return live, nil
}

// DeployedMilestoneRun is the project's most recent SUCCEEDED spec build — the
// version that is live, and therefore the milestone an incident belongs to.
// ListByProject is newest-first, so the first match is the answer.
func (a eventcoreRuns) DeployedMilestoneRun(ctx context.Context, orgID, projectID string) (*delivery.MilestoneRun, error) {
	rows, err := a.runs.ListByProject(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].Origin == delivery.RunOriginSpecBuild && rows[i].State == delivery.RunStateSucceeded {
			return &rows[i], nil
		}
	}
	return nil, nil
}

// KnownMilestones is the distinct set of milestones this project has ever run,
// newest first — the reconcile sweep's walk.
func (a eventcoreRuns) KnownMilestones(ctx context.Context, orgID, projectID string) ([]eventcore.MilestoneRef, error) {
	rows, err := a.runs.ListByProject(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	seen := make(map[int]bool, len(rows))
	out := make([]eventcore.MilestoneRef, 0, len(rows))
	for i := range rows {
		if seen[rows[i].MilestoneNumber] {
			continue
		}
		seen[rows[i].MilestoneNumber] = true
		out = append(out, eventcore.MilestoneRef{Number: rows[i].MilestoneNumber, Title: rows[i].MilestoneTitle})
	}
	return out, nil
}

func (a eventcoreRuns) BumpBudget(ctx context.Context, runID string, counter delivery.RunBudget) error {
	_, err := a.runs.BumpBudget(ctx, runID, counter)
	return err
}

// eventcoreCycles projects the cycle repository onto the event plane's
// CycleStore. The repository's guarded mutators return the row they changed
// (or nil when a closed cycle made them a no-op); the event plane only needs
// to know the write happened, so the row is dropped here.
type eventcoreCycles struct{ cycles delivery.RunCycleRepository }

func (a eventcoreCycles) Latest(ctx context.Context, orgID, runID string) (*delivery.RunCycle, error) {
	return a.cycles.Latest(ctx, orgID, runID)
}

func (a eventcoreCycles) NotePullRequest(ctx context.Context, cycleID string, pr delivery.CyclePullRequest) error {
	_, err := a.cycles.NotePullRequest(ctx, cycleID, pr)
	return err
}

func (a eventcoreCycles) NoteMergeDecision(ctx context.Context, cycleID string, resolves []int, verdict, reason string) error {
	_, err := a.cycles.NoteMergeDecision(ctx, cycleID, resolves, verdict, reason)
	return err
}

func (a eventcoreCycles) FinishCycle(ctx context.Context, cycleID, mergeSHA string) error {
	_, err := a.cycles.Finish(ctx, cycleID, mergeSHA)
	return err
}

// eventcoreBuilds is the OpenChoreo half: trigger a build pinned to a commit
// under a caller-chosen name, and read a component's runs back.
//
// The name matters — it carries the (component, commit, attempt) triple the
// re-trigger budget is counted on — which is why the build secret is staged
// under the SAME name the caller picked, exactly as the coding executor does
// for its own builds.
type eventcoreBuilds struct {
	oc     openchoreo.ComponentClient
	repos  sourcecontrol.RepoRepository
	stager codingagentBuildStager
}

// codingagentBuildStager is the per-org build git-credential stager (the same
// one the manual build path and the coding executor use). Restated here so
// this file names a capability rather than a service.
type codingagentBuildStager interface {
	StageBuildSecret(ctx context.Context, orgID, repoSlug, runName string) (string, error)
}

func (a eventcoreBuilds) TriggerBuildAtCommit(ctx context.Context, orgID, projectID, component, commitSHA, runName string) error {
	secretRef, err := a.stageSecret(ctx, orgID, projectID, runName)
	if err != nil {
		return err
	}
	_, err = a.oc.TriggerBuildAtCommit(ctx, orgID, projectID, component, commitSHA, secretRef, runName)
	return err
}

// stageSecret mirrors the coding executor's staging contract: no stager or no
// repo slug means clone unauthenticated (correct for a public repo), while a
// staging refusal blocks the build rather than producing a build that cannot
// clone.
func (a eventcoreBuilds) stageSecret(ctx context.Context, orgID, projectID, runName string) (string, error) {
	if a.stager == nil || a.repos == nil {
		return "", nil
	}
	repo, err := a.repos.GetByOrgAndProjectID(ctx, orgID, projectID)
	if err != nil || repo == nil || repo.RepoSlug == "" {
		slog.WarnContext(ctx, "eventcore build: no repo slug — cloning unauthenticated",
			"org", orgID, "project", projectID, "error", err)
		return "", nil
	}
	return a.stager.StageBuildSecret(ctx, orgID, repo.RepoSlug, runName)
}

// ListBuildRuns reads the component's WorkflowRuns — the fact the re-trigger
// budget is derived from. It takes the host's default page rather than a
// cursor walk: attempts at the commit just merged are the newest runs a
// component has, so a first page always contains them, and paging a
// long-lived component's whole history on every build terminal would cost far
// more than the two rows the count needs.
func (a eventcoreBuilds) ListBuildRuns(ctx context.Context, orgID, projectID, component string) ([]eventcore.BuildRun, error) {
	list, err := a.oc.ListWorkflowRuns(ctx, orgID, projectID, component, 0, "")
	if err != nil {
		return nil, err
	}
	if list == nil {
		return nil, nil
	}
	out := make([]eventcore.BuildRun, 0, len(list.Items))
	for _, item := range list.Items {
		out = append(out, eventcore.BuildRun{Name: item.Name, Status: item.Status, Completed: item.Completed})
	}
	return out, nil
}

// eventcoreRepoLister projects the repo repository onto the sweep's lister.
type eventcoreRepoLister struct{ repos sourcecontrol.RepoRepository }

func (l eventcoreRepoLister) ListAll(ctx context.Context) ([]eventcore.RepoRef, error) {
	rows, err := l.repos.ListAllReady(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]eventcore.RepoRef, 0, len(rows))
	for i := range rows {
		owner, name := naming.OwnerRepoFromURL(rows[i].RepoURL)
		if owner == "" || name == "" {
			continue
		}
		out = append(out, eventcore.RepoRef{OrgID: rows[i].OrgID, ProjectID: rows[i].ProjectID, FullName: owner + "/" + name})
	}
	return out, nil
}

// eventcoreComponents is the pre-build component provisioning the merged-PR
// fan-out runs: create or update the component's OpenChoreo Component CR from
// the design facts, then emit any runtime config that rides on it.
//
// It is a COMPOSITE because the two used to run together as the coding
// dispatch's per-component pre-flight, and the fan-out is where that pair now
// belongs — a cycle is scoped to a milestone and may touch several components,
// so no dispatch knows which component is about to be built.
//
// The CR is required (its absence fails the build); the runtime-config emit is
// best-effort and self-no-ops for anything but a web app, exactly as before.
type eventcoreComponents struct {
	comp    componentEnsurer
	runtime componentRuntimeConfigEmitter
}

// componentEnsurer / componentRuntimeConfigEmitter are the two narrow verbs the
// composite needs. projects.ComponentService and
// *runtimeconfig.RuntimeConfigService satisfy them structurally.
type componentEnsurer interface {
	EnsureComponent(ctx context.Context, orgName, projectName, componentName string) error
}

type componentRuntimeConfigEmitter interface {
	EmitForComponent(ctx context.Context, orgID, projectID, componentName string) error
}

func (a eventcoreComponents) EnsureComponent(ctx context.Context, orgID, projectID, component string) error {
	if a.comp == nil {
		return nil
	}
	if err := a.comp.EnsureComponent(ctx, orgID, projectID, component); err != nil {
		return err
	}
	if a.runtime != nil {
		if err := a.runtime.EmitForComponent(ctx, orgID, projectID, component); err != nil {
			slog.WarnContext(ctx, "eventcore: env-config.js emit failed (best-effort)",
				"component", component, "error", err)
		}
	}
	return nil
}

// eventcoreAdopter adapts the event plane onto the task feature's Adopter port:
// the SRE/RCA handoff's promote-from-issue leg hands a freshly filed issue to
// the coding agent. It passes a BARE target — the caller just created the
// issue, so it belongs to no milestone yet and adoption files it under the
// deployed version's.
type eventcoreAdopter struct{ events *eventcore.Events }

func (a eventcoreAdopter) AdoptIssue(ctx context.Context, orgID, projectID string, issueNumber int) error {
	return a.events.AdoptIssue(ctx, orgID, projectID, eventcore.AdoptTarget{Number: issueNumber})
}

// projectRunRows adapts the milestone-run + cycle repositories onto the
// projects domain's status/purge port. The read half is the run index the
// overview's build + deploy stages render from; the purge half deletes the
// CYCLES before their runs, so a recreated same-named project cannot inherit
// orphaned cycle records.
type projectRunRows struct {
	runs   delivery.MilestoneRunRepository
	cycles delivery.RunCycleRepository
}

func (a projectRunRows) ListByProject(ctx context.Context, orgID, projectID string) ([]delivery.MilestoneRun, error) {
	return a.runs.ListByProject(ctx, orgID, projectID)
}

func (a projectRunRows) DeleteByProject(ctx context.Context, orgID, projectID string) error {
	if err := a.cycles.DeleteByProject(ctx, orgID, projectID); err != nil {
		return err
	}
	return a.runs.DeleteByProject(ctx, orgID, projectID)
}
