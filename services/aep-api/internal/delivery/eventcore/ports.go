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

package eventcore

import (
	"context"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// The consumer ports the event plane drives. Each is the narrow slice of a
// larger service this package actually uses (the house consumer-port pattern);
// the concrete providers are adapters wired at the composition root.

// RunStore is the milestone-run read surface that gates every handler, plus
// the one counter the event plane owns. Satisfied by an adapter over
// delivery.MilestoneRunRepository.
//
// Every method answers with (nil, nil) when there is nothing — "no live run"
// is the normal case, not an error.
type RunStore interface {
	// LiveRunForMilestone returns the non-terminal run working this milestone.
	// THIS is the inertness gate: a nil run means the event is not ours and the
	// handler returns without a single write.
	LiveRunForMilestone(ctx context.Context, orgID, projectID string, milestoneNumber int) (*delivery.MilestoneRun, error)
	// LiveRunsForProject returns every non-terminal run of the project, newest
	// first — at most one spec build (the mutex) plus any concurrent incident
	// adoptions. The event plane narrows them itself: by origin for a pull
	// request whose branch names no milestone, by cycle merge SHA for a build
	// terminal. Keeping the narrowing here rather than in the adapter is what
	// makes it testable without a database.
	LiveRunsForProject(ctx context.Context, orgID, projectID string) ([]delivery.MilestoneRun, error)
	// DeployedMilestoneRun returns the run of the project's most recently
	// SUCCEEDED spec build — the deployed version, whose milestone is where
	// incidents and adopted bare issues belong. Nil when the project has never
	// completed a version, which is what makes "no milestone for the deployed
	// version — trigger a build" an honest error rather than a guess.
	DeployedMilestoneRun(ctx context.Context, orgID, projectID string) (*delivery.MilestoneRun, error)
	// KnownMilestones returns every milestone the platform has ever run for this
	// project, newest first. The reconcile sweep walks these rather than
	// enumerating GitHub's milestones: a milestone the platform never ran is not
	// a missed delivery, it is somebody else's milestone.
	KnownMilestones(ctx context.Context, orgID, projectID string) ([]MilestoneRef, error)
	// BumpBudget increments one of the run's budget counters (the event plane
	// only ever bumps the build re-trigger tally; the supervisor owns the rest).
	BumpBudget(ctx context.Context, runID string, counter delivery.RunBudget) error
}

// MilestoneRef is a milestone the platform knows by NUMBER (the key) with the
// title it carried when the run row was written (display, and the `v<N>` tag a
// `?tag=` query resolves through). Titles are renamable on GitHub, so the
// number always wins.
type MilestoneRef struct {
	Number int
	Title  string
}

// CycleStore is the cycle-record write surface: the event plane learns the
// branch, the pull request and the merge SHA from webhooks and records them on
// the run's current cycle. Satisfied by an adapter over
// delivery.RunCycleRepository.
//
// Every mutator is a no-op on a closed cycle (the repository's guard), so a
// redelivered webhook cannot rewrite a recorded outcome.
type CycleStore interface {
	// Latest returns the run's newest cycle, or (nil, nil) before its first
	// dispatch.
	Latest(ctx context.Context, orgID, runID string) (*delivery.RunCycle, error)
	// NotePullRequest records the pull request the agent actually opened.
	NotePullRequest(ctx context.Context, cycleID string, pr delivery.CyclePullRequest) error
	// NoteMergeDecision records the merge policy's matched issue set and, when
	// the pull request did not merge, the verdict and its reason.
	NoteMergeDecision(ctx context.Context, cycleID string, resolves []int, verdict, reason string) error
	// FinishCycle closes the cycle and records the merge SHA it landed.
	FinishCycle(ctx context.Context, cycleID, mergeSHA string) error
}

// IssueClient is the GitHub issue + milestone surface the event plane needs:
// mint platform issues into a milestone, read a milestone's membership for the
// auto-merge predicate, count it for the dispatch predicate, and move an
// adopted issue into the deployed version's milestone.
// sourcecontrol.IssueService satisfies it.
type IssueClient interface {
	// CreateIssue mints an issue. Every call from this package passes a
	// DedupeKey — that is what makes minting redelivery-safe.
	CreateIssue(ctx context.Context, orgID, projectID string, req sourcecontrol.CreateIssueRequest) (*sourcecontrol.IssueResult, error)
	// ListMilestoneIssues reads a milestone's issues, filtered by state and
	// label. Pull requests are excluded by the host.
	ListMilestoneIssues(ctx context.Context, orgID, projectID string, filter sourcecontrol.MilestoneIssuesFilter) ([]sourcecontrol.IssueInfo, error)
	// MilestoneIssueCounts is the dispatch predicate's input, in one call.
	MilestoneIssueCounts(ctx context.Context, orgID, projectID string, number int) (*sourcecontrol.MilestoneIssueCounts, error)
	// SetIssueMilestone assigns an existing issue to a milestone by NUMBER —
	// adoption of a bare issue into the deployed version's milestone.
	SetIssueMilestone(ctx context.Context, orgID, projectID string, number, milestoneNumber int) error
}

// PRReader reads a pull request's live state (the ground-truth check before
// merging, which is what makes a redelivered merge a no-op) and its changed
// files (the path-diff input). sourcecontrol.IssueService satisfies it.
type PRReader interface {
	GetPullRequestState(ctx context.Context, orgID, projectID string, number int) (*sourcecontrol.PullRequestState, error)
	ListPullRequestFiles(ctx context.Context, orgID, projectID string, number int) ([]string, error)
}

// PRMerger squash-merges a pull request. sourcecontrol.IssueService satisfies
// it; the underlying host call is already squash and already reconciles an
// already-merged PR to success.
//
// There is no separate bot credential: the merge rides the same per-org
// credential every other GitHub call does, whose App-mode identity IS the
// <slug>[bot] the design calls "the bot".
type PRMerger interface {
	MergePullRequest(ctx context.Context, orgID, projectID string, number int) error
}

// RepoLookup resolves a webhook's "owner/name" to the owning org + project.
type RepoLookup interface {
	ByFullName(ctx context.Context, fullName string) (orgID, projectID string, err error)
}

// RepoRef identifies one project repository for the reconcile sweep.
type RepoRef struct {
	OrgID     string
	ProjectID string
	FullName  string // "owner/name"
}

// RepoLister enumerates every project repository the reconcile sweep walks.
type RepoLister interface {
	ListAll(ctx context.Context) ([]RepoRef, error)
}

// DesignReader maps a component to its App Path — the source directory the
// path diff matches a merged PR's changed files against. Wired from the
// artifacts store.
type DesignReader interface {
	// ComponentPaths returns each component's appPath relative to the repo
	// root. An empty appPath means the component builds from the repo root and
	// therefore matches any change. Nil when the project has no design.
	ComponentPaths(ctx context.Context, orgID, projectID string) (map[string]string, error)
}

// BuildRun is one OpenChoreo WorkflowRun as the event plane reads it back.
type BuildRun struct {
	Name      string
	Status    string
	Completed bool
}

// BuildTrigger is the OpenChoreo build surface: pin a build to a commit, and
// read back the runs that already exist for a component.
//
// The read half is load-bearing, not a convenience. Per-component build state
// is DERIVED from OpenChoreo on read and never stored, so the runs themselves
// are the record of how many times a (component, commit) pair has been built —
// which is exactly the automatic re-trigger budget. Deploy needs no verb here:
// components carry AutoDeploy, so a green build deploys itself.
type BuildTrigger interface {
	// TriggerBuildAtCommit creates a WorkflowRun named runName, pinned to
	// commitSHA. The caller owns the name because the name is what encodes the
	// (component, commit, attempt) triple the budget is counted on.
	TriggerBuildAtCommit(ctx context.Context, orgID, projectID, component, commitSHA, runName string) error
	// ListBuildRuns returns the component's WorkflowRuns, newest first.
	ListBuildRuns(ctx context.Context, orgID, projectID, component string) ([]BuildRun, error)
}

// ComponentEnsurer provisions a component's OpenChoreo Component CR from the
// design facts, idempotently, and emits any runtime config that rides on it.
//
// It is called by the merged-PR fan-out, immediately before the component's
// build is triggered. That is the ONLY moment left that knows a specific
// component is about to be built: a cycle is scoped to a MILESTONE and may
// touch several components, so the dispatch path — which used to run this as a
// per-component pre-flight — no longer has a component to name. Without it a
// first-ever component's build fails with "Component not found".
//
// Satisfied by an app-root adapter over the projects component service plus the
// runtime-config emitter; the event plane names neither.
type ComponentEnsurer interface {
	// EnsureComponent creates or updates the component's CR. An error blocks
	// that component's build — triggering a build for a component that does not
	// exist would only fail later and less clearly.
	EnsureComponent(ctx context.Context, orgID, projectID, component string) error
}

// RunSignaler delivers a signal to a milestone RUN.
//
// It is a port rather than the root Signaler because the root Signaler routes
// through RunningTaskByIssue on the tag-keyed workflow_runs table — the wrong
// key entirely for a run that is identified by (org, project, milestone). It
// stays an interface because that is what keeps this package free of a
// workflow engine: the supervisor satisfies it, the event plane never sees it.
//
// Signalling is best-effort by contract: an unreachable supervisor must not
// fail webhook processing, because the reconcile sweep and the cycle-boundary
// poll re-derive from ground truth.
type RunSignaler interface {
	SignalRun(ctx context.Context, run *delivery.MilestoneRun, name string, payload delivery.RunSignal) error
}

// RunStarter starts a run over a milestone that has work and no live run: the
// adoption path and the reconcile sweep's backstop. Everything this package
// starts carries delivery.RunOriginIncidentAdoption — the spec-build origin
// belongs to the plan path alone.
//
// Admission (the run row) and supervision (the workflow) must happen together
// or a run row exists that nobody is driving, so both live behind this one
// port on the supervisor's side rather than being split across packages.
// Implementations must be idempotent — the sweep re-offers the same milestone
// every pass until a run is live.
//
// The request type lives at the domain root because the plan path in `build`
// asks the same supervisor the same question and the two sub-packages may not
// import each other.
type RunStarter interface {
	StartRun(ctx context.Context, req delivery.StartRunRequest) error
}
