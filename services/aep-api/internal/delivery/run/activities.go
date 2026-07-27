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

package run

import (
	"context"
	"errors"
	"log/slog"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// errNotConfigured is returned by an activity whose port was not wired. It is
// deliberately an ERROR for the stores (a supervisor that cannot record its own
// outcome must not pretend it did) and deliberately NOT one for the optional
// collaborators, which degrade to "nothing to do".
var errNotConfigured = errors.New("run: activity dependency not configured")

// Activities is every I/O the run loop performs. Each one is a thin adapter
// over a port — there is no loop logic here, and no decision: the workflow
// decides, the activity fetches or records.
type Activities struct {
	runs       RunStore
	cycles     CycleStore
	milestones MilestoneReader
	prs        PRReader
	design     DesignReader
	builds     BuildReader
	validation ValidationCoordinator
	dispatcher delivery.MilestoneDispatcher
}

// Deps carries the activity adapters. runs/cycles/milestones are required; the
// rest degrade (see each activity).
type Deps struct {
	Runs       RunStore
	Cycles     CycleStore
	Milestones MilestoneReader
	PRs        PRReader
	Design     DesignReader
	Builds     BuildReader
	Validation ValidationCoordinator
	Dispatcher delivery.MilestoneDispatcher
}

// NewActivities wires the activity adapters.
func NewActivities(d Deps) *Activities {
	return &Activities{
		runs:       d.Runs,
		cycles:     d.Cycles,
		milestones: d.Milestones,
		prs:        d.PRs,
		design:     d.Design,
		builds:     d.Builds,
		validation: d.Validation,
		dispatcher: d.Dispatcher,
	}
}

// ---- run row ---------------------------------------------------------------

// SetRunStateInput moves a run between the two non-terminal states.
type SetRunStateInput struct {
	RunID string `json:"runId"`
	State string `json:"state"`
}

// SetRunState mirrors the loop's waiting ⇄ running oscillation onto the run
// row, which is what the console polls. A run that has already settled is a
// no-op in the repository, so a late write cannot resurrect it.
func (a *Activities) SetRunState(ctx context.Context, in SetRunStateInput) error {
	if a.runs == nil {
		return errNotConfigured
	}
	return a.runs.SetState(ctx, in.RunID, in.State)
}

// SettleRunInput ends a run with its terminal state and reason.
type SettleRunInput struct {
	RunID  string `json:"runId"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// SettleRun writes the run's outcome. Guarded in the repository on the run not
// already being terminal, so the first settle wins.
func (a *Activities) SettleRun(ctx context.Context, in SettleRunInput) error {
	if a.runs == nil {
		return errNotConfigured
	}
	return a.runs.Settle(ctx, in.RunID, in.State, in.Reason)
}

// BumpRunBudgetInput names the counter to increment.
type BumpRunBudgetInput struct {
	RunID   string `json:"runId"`
	Counter string `json:"counter"`
}

// BumpRunBudget increments one budget counter on the run row. It is READ-MODEL
// BOOKKEEPING only: the workflow counts its own budgets deterministically, so a
// failed bump costs the console a number, never the loop a decision.
func (a *Activities) BumpRunBudget(ctx context.Context, in BumpRunBudgetInput) error {
	if a.runs == nil {
		return errNotConfigured
	}
	return a.runs.BumpBudget(ctx, in.RunID, delivery.RunBudget(in.Counter))
}

// SetValidationVerdictInput records the validation cycle's outcome.
type SetValidationVerdictInput struct {
	RunID   string `json:"runId"`
	Verdict string `json:"verdict"`
}

// SetValidationVerdict writes the verdict onto the run. It is a RUN property,
// not a per-issue one — the deployment surface reads it from here.
func (a *Activities) SetValidationVerdict(ctx context.Context, in SetValidationVerdictInput) error {
	if a.runs == nil {
		return errNotConfigured
	}
	return a.runs.SetValidationVerdict(ctx, in.RunID, in.Verdict)
}

// ---- cycle record ----------------------------------------------------------

// AppendCycleInput opens a new cycle record under a run.
type AppendCycleInput struct {
	RunID     string `json:"runId"`
	OrgID     string `json:"orgId"`
	ProjectID string `json:"projectId"`
	Kind      string `json:"kind"`
}

// AppendCycle opens the cycle record for a dispatch and returns its id.
func (a *Activities) AppendCycle(ctx context.Context, in AppendCycleInput) (string, error) {
	if a.cycles == nil {
		return "", errNotConfigured
	}
	return a.cycles.Append(ctx, &delivery.RunCycle{
		RunID:     in.RunID,
		OrgID:     in.OrgID,
		ProjectID: in.ProjectID,
		Kind:      in.Kind,
	})
}

// NoteCycleDispatchInput records one dispatch attempt against a cycle.
type NoteCycleDispatchInput struct {
	CycleID string `json:"cycleId"`
	JobRef  string `json:"jobRef"`
}

// NoteCycleDispatch increments the cycle's attempt count and re-points it at
// the newly launched Job.
func (a *Activities) NoteCycleDispatch(ctx context.Context, in NoteCycleDispatchInput) error {
	if a.cycles == nil {
		return errNotConfigured
	}
	return a.cycles.NoteDispatch(ctx, in.CycleID, in.JobRef)
}

// FinishCycleInput closes a cycle record.
type FinishCycleInput struct {
	CycleID  string `json:"cycleId"`
	MergeSHA string `json:"mergeSha,omitempty"`
}

// FinishCycle closes the cycle. Usually a no-op: the event plane already closed
// it on the merge webhook, and the repository's open-cycle guard makes the
// second close change nothing. The supervisor calls it anyway because a cycle
// that ended WITHOUT a merge — agent death, a conflict, a cancel — has no
// webhook to close it.
func (a *Activities) FinishCycle(ctx context.Context, in FinishCycleInput) error {
	if a.cycles == nil {
		return errNotConfigured
	}
	return a.cycles.Finish(ctx, in.CycleID, in.MergeSHA)
}

// CycleFactsInput asks for the run's current cycle record.
type CycleFactsInput struct {
	OrgID string `json:"orgId"`
	RunID string `json:"runId"`
}

// CycleFacts is what the EVENT PLANE learned about the cycle from webhooks —
// the supervisor's ground truth for "did this cycle land?".
type CycleFacts struct {
	CycleID  string `json:"cycleId"`
	Attempts int    `json:"attempts"`
	Branch   string `json:"branch,omitempty"`
	PRNumber int    `json:"prNumber,omitempty"`
	MergeSHA string `json:"mergeSha,omitempty"`
	Ended    bool   `json:"ended"`
}

// ReadCycleFacts reads the cycle record back.
//
// This is the poll behind "never trust the signal payload alone": a merge
// signal wakes the loop, and THIS is what tells it a merge really happened —
// which is also how a cycle whose merge webhook was lost still finishes, off
// the deadline path.
func (a *Activities) ReadCycleFacts(ctx context.Context, in CycleFactsInput) (CycleFacts, error) {
	if a.cycles == nil {
		return CycleFacts{}, errNotConfigured
	}
	row, err := a.cycles.Latest(ctx, in.OrgID, in.RunID)
	if err != nil || row == nil {
		return CycleFacts{}, err
	}
	return CycleFacts{
		CycleID:  row.ID,
		Attempts: row.Attempts,
		Branch:   row.Branch,
		PRNumber: row.PRNumber,
		MergeSHA: row.MergeSHA,
		Ended:    row.EndedAt != nil,
	}, nil
}

// ---- milestone -------------------------------------------------------------

// MilestoneRef identifies the milestone a poll or a close is about.
type MilestoneRef struct {
	OrgID           string `json:"orgId"`
	ProjectID       string `json:"projectId"`
	MilestoneNumber int    `json:"milestoneNumber"`
}

// PollMilestone is the cycle-boundary read of ground truth — ONE GraphQL round
// trip returning the gate count, the working set and the total.
//
// Every boundary decision is made from this and nothing else: whether to
// dispatch, whether a gate is holding, whether the version is finished, and
// whether the last cycle made progress.
func (a *Activities) PollMilestone(ctx context.Context, in MilestoneRef) (MilestoneSnapshot, error) {
	if a.milestones == nil {
		return MilestoneSnapshot{}, errNotConfigured
	}
	counts, err := a.milestones.MilestoneIssueCounts(ctx, in.OrgID, in.ProjectID, in.MilestoneNumber)
	if err != nil {
		return MilestoneSnapshot{}, err
	}
	if counts == nil {
		return MilestoneSnapshot{}, nil
	}
	return MilestoneSnapshot{
		Work:  counts.OpenNonGateWork(),
		Gates: counts.OpenProvision,
		Total: counts.OpenTotal,
	}, nil
}

// CloseMilestone closes the settled version's milestone. Display only, and
// best-effort by contract: the run's outcome is the run row, so a close that
// fails must not turn a succeeded run into a failed one.
func (a *Activities) CloseMilestone(ctx context.Context, in MilestoneRef) error {
	if a.milestones == nil {
		return nil
	}
	if err := a.milestones.CloseMilestone(ctx, in.OrgID, in.ProjectID, in.MilestoneNumber); err != nil {
		slog.WarnContext(ctx, "run: closing the settled milestone failed — the run still succeeded",
			"project", in.ProjectID, "milestone", in.MilestoneNumber, "error", err)
	}
	return nil
}

// ---- builds ----------------------------------------------------------------

// CycleBuildsInput asks how far a cycle's build fan-out has got.
type CycleBuildsInput struct {
	OrgID     string `json:"orgId"`
	ProjectID string `json:"projectId"`
	PRNumber  int    `json:"prNumber"`
	MergeSHA  string `json:"mergeSha"`
}

// PollCycleBuilds derives the cycle's build state from OpenChoreo.
//
// It recomputes the SAME path diff the event plane fanned out over — from the
// merged pull request's files and the design's App Paths, through the root
// delivery.DiffComponents — because the expected set has to match the triggered
// set exactly or the loop would either hang on a component nobody built or
// settle before one reported.
//
// Nothing here is stored: per-component build state is derived on read, always.
func (a *Activities) PollCycleBuilds(ctx context.Context, in CycleBuildsInput) (CycleBuildState, error) {
	if a.builds == nil || a.prs == nil || a.design == nil || in.MergeSHA == "" || in.PRNumber == 0 {
		// Nothing to observe is genuinely green: a cycle whose merge touched no
		// component (a validation run's tests-and-report pull request) has no
		// build to wait for.
		return CycleBuildState{}, nil
	}
	files, err := a.prs.ListPullRequestFiles(ctx, in.OrgID, in.ProjectID, in.PRNumber)
	if err != nil {
		return CycleBuildState{}, err
	}
	paths, err := a.design.ComponentPaths(ctx, in.OrgID, in.ProjectID)
	if err != nil {
		return CycleBuildState{}, err
	}
	diff := delivery.DiffComponents(files, paths)
	out := CycleBuildState{Expected: len(diff.Components)}
	for _, component := range diff.Components {
		runs, lerr := a.builds.ListBuildRuns(ctx, in.OrgID, in.ProjectID, component)
		if lerr != nil {
			return CycleBuildState{}, lerr
		}
		switch classifyComponentBuild(runs, delivery.BuildRunNamePrefix(in.ProjectID, component, in.MergeSHA)) {
		case buildGreen:
			out.Settled++
		case buildRed:
			out.Settled++
			out.Red = append(out.Red, component)
		case buildPending:
		}
	}
	return out, nil
}

// ---- validation ------------------------------------------------------------

// EnsureValidationIssue mints the run's validation issue into the milestone and
// returns its number, or 0 when there is nothing to validate.
//
// An unwired coordinator returns 0 rather than an error: "this deployment has
// no acceptance oracle" and "this deployment has no validation feature" are the
// same thing from the loop's point of view, and neither is a failed run.
func (a *Activities) EnsureValidationIssue(ctx context.Context, in MilestoneRef) (int, error) {
	if a.validation == nil {
		return 0, nil
	}
	return a.validation.EnsureValidationIssue(ctx, in.OrgID, in.ProjectID, in.MilestoneNumber)
}

// ProjectRef identifies a project for the verdict read.
type ProjectRef struct {
	OrgID     string `json:"orgId"`
	ProjectID string `json:"projectId"`
}

// ReadValidationVerdict reads the runner's committed report and returns one of
// the delivery.ValidationVerdict* values. A missing or unreadable report is
// "skipped", not "failed" — the run landed its work either way, and calling an
// absent report a failure would make the report's own plumbing a quality gate.
func (a *Activities) ReadValidationVerdict(ctx context.Context, in ProjectRef) (string, error) {
	if a.validation == nil {
		return delivery.ValidationVerdictSkipped, nil
	}
	return a.validation.Verdict(ctx, in.OrgID, in.ProjectID)
}

// ---- dispatch --------------------------------------------------------------

// DispatchAgent launches the cycle's agent run and returns the Job reference.
//
// It is the ONE activity whose failure is not retried by Temporal: a launch
// that did not happen is agent death, which the cycle's own re-dispatch budget
// already answers. Letting Temporal retry it as well would spend that budget
// invisibly.
func (a *Activities) DispatchAgent(ctx context.Context, in delivery.MilestoneDispatch) (string, error) {
	if a.dispatcher == nil {
		return "", errNotConfigured
	}
	return a.dispatcher.Dispatch(ctx, in)
}
