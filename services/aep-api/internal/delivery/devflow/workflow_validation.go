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

package devflow

import (
	"fmt"
	"time"

	"github.com/wso2/aep/aep-api/models"
	"go.temporal.io/sdk/workflow"
)

// The validating phase runs as its own child workflow tree: the dev workflow
// spawns ONE ValidationFlowWorkflow (the phase orchestrator), which resolves
// the project's validation issue, fans out one ValidationTaskWorkflow child
// per lane (in parallel), and — once every lane finished — merges the SINGLE
// validation PR. One issue, one PR, N lanes.
//
// The orchestrator owns the validation issue's run row (kind=validation), so
// the webhook signaler routes all of the issue's signals (pr-opened, job
// failures, pr-merged) to it; it forwards each lane's terminal state to the
// lane child via SigLaneStatus, correlated by the ExecutionID it dispatched.

// Lane kinds. The acceptance oracle's criterion.Method (e2e | scenario |
// manual) is the discriminator; only the e2e lane is automated today — its
// runner authors/runs the Playwright suite and opens the validation PR, so
// pr-opened doubles as the e2e lane's completion signal. A future scenario
// lane completes on a job-succeeded signal instead (watcher follow-up).
const LaneE2E = "e2e"

// ValidationFlowInput starts the validating phase's orchestrator.
type ValidationFlowInput struct {
	OrgID     string `json:"orgId"`
	ProjectID string `json:"projectId"`
	Repo      string `json:"repo"` // "owner/name"
	Tag       string `json:"tag"`
	// DevWorkflowID is the spawning dev run's workflow id — stamped as the
	// orchestrator row's ParentWorkflowID so the status builder's
	// ValidationRunByParent(devRunID) join keeps working.
	DevWorkflowID string     `json:"devWorkflowId"`
	Gates         GateConfig `json:"gates"`
}

// ValidationLane is one automated validation lane: a lane kind plus the
// validation issue it validates. Today every lane shares the project's single
// aep:validation issue.
type ValidationLane struct {
	Kind  string `json:"kind"`
	Issue int    `json:"issue"`
}

// ValidationLaneResult is one lane's terminal state.
type ValidationLaneResult struct {
	Kind    string `json:"kind"`
	Issue   int    `json:"issue"`
	Outcome string `json:"outcome"` // succeeded | failed
	Error   string `json:"error,omitempty"`
}

// ValidationFlowResult is what the orchestrator returns to the dev workflow.
type ValidationFlowResult struct {
	Outcome string `json:"outcome"` // succeeded | failed | skipped
	// Reason is "no acceptance criteria" on skip, the failure summary on
	// failed, empty on success.
	Reason   string                 `json:"reason,omitempty"`
	PRNumber int                    `json:"prNumber,omitempty"`
	Lanes    []ValidationLaneResult `json:"lanes,omitempty"`
}

// ValidationFlowResult outcomes (succeeded/failed reuse the task Outcome*
// values so the dev status maps them uniformly).
const ValidationOutcomeSkipped = "skipped"

// ValidationFlowStatus is the orchestrator's QueryStatus payload.
type ValidationFlowStatus struct {
	Phase       string       `json:"phase"` // starting | running | merging | done | failed
	Issue       int          `json:"issue,omitempty"`
	PRNumber    int          `json:"prNumber,omitempty"`
	Lanes       []DevTaskRef `json:"lanes,omitempty"`
	PendingGate string       `json:"pendingGate,omitempty"`
	Error       string       `json:"error,omitempty"`
}

// ValidationTaskInput starts one lane child. The parent dispatched the lane's
// runner already (ExecutionID); the child is the lane's wait point and the
// extension seam for lane-specific steps later.
type ValidationTaskInput struct {
	Lane        string `json:"lane"`
	Issue       int    `json:"issue"`
	ExecutionID string `json:"executionId"`
	// WaitTimeout bounds the wait for the forwarded terminal signal; zero
	// means codingWaitTimeout (the lane's runner is a coding-shaped job).
	WaitTimeout time.Duration `json:"waitTimeout,omitempty"`
}

// validationFlowWorkflowID builds the orchestrator's deterministic workflow id
// (validationflow-<org>-<project>-<tag>) — one validating phase per dev run.
func validationFlowWorkflowID(orgID, projectID, tag string) string {
	return fmt.Sprintf("validationflow-%s-%s-%s", orgID, projectID, tag)
}

// validationTaskWorkflowID builds a lane child's deterministic workflow id
// (valtask-<org>-<project>-<tag>-<lane>-<issueNumber>).
func validationTaskWorkflowID(orgID, projectID, tag, lane string, issue int) string {
	return fmt.Sprintf("valtask-%s-%s-%s-%s-%d", orgID, projectID, tag, lane, issue)
}

// ValidationPhaseRunning is the orchestrator's phase while lanes execute
// (the other phases reuse the TaskPhase* values).
const ValidationPhaseRunning = "running"

// laneRun is the orchestrator's bookkeeping for one spawned lane child.
// Slice-ordered (never a map) so the pump iterates deterministically.
type laneRun struct {
	lane   ValidationLane
	execID string
	future workflow.ChildWorkflowFuture
	done   bool
	result ValidationLaneResult
}

// ValidationFlowWorkflow orchestrates the validating phase: resolve the
// project's validation issue (0 ⇒ skip), record the phase's run row (the
// issue's signal owner), dispatch every lane and fan out one lane child each,
// pump the issue's webhook signals to the lanes, and — once all lanes
// succeeded — merge the SINGLE validation PR. A failing lane or merge fails
// the phase; failures are returned as data (the dev workflow decides).
func ValidationFlowWorkflow(ctx workflow.Context, in ValidationFlowInput) (ValidationFlowResult, error) {
	status := ValidationFlowStatus{Phase: TaskPhaseStarting}
	// runMergePhase mutates a TaskFlowStatus; mirror its live gate into the
	// query view so a manual merge gate stays visible on the orchestrator.
	var ms TaskFlowStatus
	if err := workflow.SetQueryHandler(ctx, QueryStatus, func() (ValidationFlowStatus, error) {
		out := status
		if out.PendingGate == "" {
			out.PendingGate = ms.PendingGate
		}
		return out, nil
	}); err != nil {
		return ValidationFlowResult{Outcome: OutcomeFailed, Reason: err.Error()}, err
	}
	gates := newGateKeeper(in.Gates, func(g string) { status.PendingGate = g })
	info := workflow.GetInfo(ctx)

	recorded := false
	var laneResults []ValidationLaneResult
	fail := func(msg string) (ValidationFlowResult, error) {
		status.Phase, status.Error = TaskPhaseFailed, msg
		if recorded {
			markRunStatus(ctx, info.WorkflowExecution.ID, models.WorkflowStatusFailed, msg)
		}
		return ValidationFlowResult{Outcome: OutcomeFailed, Reason: msg, PRNumber: status.PRNumber, Lanes: laneResults}, nil
	}

	// 1. Resolve the validation issue (idempotent ensure + find). 0 ⇒ no
	// acceptance criteria — nothing to validate, and no run row is recorded
	// (the deployments board keeps showing no validation for this build).
	var issue int
	if err := workflow.ExecuteActivity(withDefaultActivityOpts(ctx), (*Activities).ResolveValidationTask, ProjectRef{
		OrgID: in.OrgID, ProjectID: in.ProjectID,
	}).Get(ctx, &issue); err != nil {
		return fail("resolve validation task: " + err.Error())
	}
	if issue == 0 {
		status.Phase = TaskPhaseDone
		return ValidationFlowResult{Outcome: ValidationOutcomeSkipped, Reason: "no acceptance criteria"}, nil
	}
	status.Issue = issue

	// 2. Record the phase's run row. kind=validation + the issue number make
	// this row the issue's webhook-signal owner (RunningTaskByIssue) and the
	// status builder's phase read (ValidationRunByParent on the dev run id).
	if err := workflow.ExecuteActivity(withDefaultActivityOpts(ctx), (*Activities).RecordWorkflowRun, RecordWorkflowRunInput{
		WorkflowID:       info.WorkflowExecution.ID,
		RunID:            info.WorkflowExecution.RunID,
		Kind:             models.WorkflowKindValidation,
		OrgID:            in.OrgID,
		ProjectID:        in.ProjectID,
		Tag:              in.Tag,
		Repo:             in.Repo,
		IssueNumber:      issue,
		ParentWorkflowID: in.DevWorkflowID,
	}).Get(ctx, nil); err != nil {
		return fail("record workflow run: " + err.Error())
	}
	recorded = true

	// 3. Gate: start coding (auto by default) — one gate for the whole phase.
	if ok, d := gates.await(ctx, GateStartCoding); !ok {
		return fail("start-coding gate rejected: " + d.Note)
	}

	// 4. Dispatch each lane's runner, then spawn its lane child. Every lane
	// shares the project's single validation issue today.
	lanes := []ValidationLane{{Kind: LaneE2E, Issue: issue}}
	status.Phase = ValidationPhaseRunning
	runs := make([]*laneRun, 0, len(lanes))
	for _, lane := range lanes {
		var executionID string
		if err := workflow.ExecuteActivity(withDefaultActivityOpts(ctx), (*Activities).DispatchCoding, DispatchCodingInput{
			OrgID: in.OrgID, ProjectID: in.ProjectID, Repo: in.Repo, Issue: lane.Issue,
		}).Get(ctx, &executionID); err != nil {
			return fail(fmt.Sprintf("dispatch %s lane: %s", lane.Kind, err.Error()))
		}
		wid := validationTaskWorkflowID(in.OrgID, in.ProjectID, in.Tag, lane.Kind, lane.Issue)
		cctx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID:        wid,
			ParentClosePolicy: enumsParentClosePolicyTerminate(),
		})
		cf := workflow.ExecuteChildWorkflow(cctx, ValidationTaskWorkflow, ValidationTaskInput{
			Lane: lane.Kind, Issue: lane.Issue, ExecutionID: executionID,
		})
		// Block until the child is actually started so forwarded signals can
		// never race its registration.
		if err := cf.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
			return fail(fmt.Sprintf("start %s lane child: %s", lane.Kind, err.Error()))
		}
		runs = append(runs, &laneRun{lane: lane, execID: executionID, future: cf})
		status.Lanes = append(status.Lanes, DevTaskRef{Issue: lane.Issue, WorkflowID: wid, Phase: TaskPhaseCoding})
	}

	// 5. Signal pump: route the issue's webhook signals to the lanes until
	// every lane child returned. Today the e2e runner opens the validation PR
	// at job end, so pr-opened IS the e2e lane's success; a job-status routes
	// to its lane by ExecutionID (failure today; a future lane whose runner
	// opens no PR completes on a job-succeeded signal the same way).
	forward := func(r *laneRun, phase, message string) {
		if r == nil || r.done {
			return
		}
		_ = r.future.SignalChildWorkflow(ctx, SigLaneStatus, LaneStatusSignal{
			Lane: r.lane.Kind, Phase: phase, Message: message,
		}).Get(ctx, nil)
	}
	byExec := func(execID string) *laneRun {
		for _, r := range runs {
			if r.execID == execID {
				return r
			}
		}
		return nil
	}
	byKind := func(kind string) *laneRun {
		for _, r := range runs {
			if r.lane.Kind == kind {
				return r
			}
		}
		return nil
	}
	jobStatus := workflow.GetSignalChannel(ctx, SigJobStatus)
	prOpened := workflow.GetSignalChannel(ctx, SigPROpened)
	timer := workflow.NewTimer(ctx, codingWaitTimeout)
	remaining, timedOut := len(runs), false
	for remaining > 0 && !timedOut {
		sel := workflow.NewSelector(ctx)
		sel.AddReceive(jobStatus, func(c workflow.ReceiveChannel, _ bool) {
			var s RunStatusSignal
			c.Receive(ctx, &s)
			forward(byExec(s.ExecutionID), s.Phase, s.Message)
		})
		sel.AddReceive(prOpened, func(c workflow.ReceiveChannel, _ bool) {
			var pr PRSignal
			c.Receive(ctx, &pr)
			status.PRNumber = pr.PRNumber
			forward(byKind(LaneE2E), PhaseSucceeded, "")
		})
		for i := range runs {
			if runs[i].done {
				continue
			}
			r := runs[i]
			idx := i
			sel.AddFuture(r.future, func(f workflow.Future) {
				var lr ValidationLaneResult
				if err := f.Get(ctx, &lr); err != nil {
					lr = ValidationLaneResult{Kind: r.lane.Kind, Issue: r.lane.Issue, Outcome: OutcomeFailed, Error: err.Error()}
				}
				r.result, r.done = lr, true
				remaining--
				status.Lanes[idx].Phase = TaskPhaseDone
				if lr.Outcome != OutcomeSucceeded {
					status.Lanes[idx].Phase = TaskPhaseFailed
				}
				status.Lanes[idx].Outcome = lr.Outcome
			})
		}
		sel.AddFuture(timer, func(workflow.Future) { timedOut = true })
		sel.Select(ctx)
	}
	if remaining > 0 {
		return fail("timed out waiting for validation lanes")
	}
	for _, r := range runs {
		laneResults = append(laneResults, r.result)
	}
	for _, r := range runs {
		if r.result.Outcome != OutcomeSucceeded {
			return fail(fmt.Sprintf("lane %s (#%d): %s", r.lane.Kind, r.lane.Issue, orEmpty(r.result.Error, r.result.Outcome)))
		}
	}

	// 6. Merge the single validation PR (its merged content — committed e2e
	// tests + report — is the phase's reviewable artifact; a validation issue
	// spawns no post-merge build, so merge is the terminal step).
	if status.PRNumber == 0 {
		return fail("validation lanes finished but no pull request was opened")
	}
	status.Phase = TaskPhaseMerging
	if err := runMergePhase(ctx, in.OrgID, in.ProjectID, status.PRNumber, in.Gates, &ms); err != nil {
		return fail(err.Error())
	}

	status.Phase = TaskPhaseDone
	markRunStatus(ctx, info.WorkflowExecution.ID, models.WorkflowStatusCompleted, "")
	return ValidationFlowResult{Outcome: OutcomeSucceeded, PRNumber: status.PRNumber, Lanes: laneResults}, nil
}

// ValidationTaskWorkflow is one validation lane: it waits for the terminal
// state the orchestrator forwards via SigLaneStatus (the orchestrator owns
// the issue's webhook signals) and reports it back as the lane result.
// Failures are data, not workflow errors — the parent aggregates them.
func ValidationTaskWorkflow(ctx workflow.Context, in ValidationTaskInput) (ValidationLaneResult, error) {
	status := TaskFlowStatus{Phase: TaskPhaseCoding, Issue: in.Issue, ExecutionID: in.ExecutionID}
	if err := workflow.SetQueryHandler(ctx, QueryStatus, func() (TaskFlowStatus, error) {
		return status, nil
	}); err != nil {
		return ValidationLaneResult{Kind: in.Lane, Issue: in.Issue, Outcome: OutcomeFailed, Error: err.Error()}, err
	}

	timeout := in.WaitTimeout
	if timeout <= 0 {
		timeout = codingWaitTimeout
	}

	laneCh := workflow.GetSignalChannel(ctx, SigLaneStatus)
	var got LaneStatusSignal
	received := false
	timer := workflow.NewTimer(ctx, timeout)
	sel := workflow.NewSelector(ctx)
	sel.AddReceive(laneCh, func(c workflow.ReceiveChannel, _ bool) {
		c.Receive(ctx, &got)
		received = true
	})
	sel.AddFuture(timer, func(workflow.Future) {})
	sel.Select(ctx)

	res := ValidationLaneResult{Kind: in.Lane, Issue: in.Issue}
	switch {
	case !received:
		res.Outcome, res.Error = OutcomeFailed, "timed out waiting for lane completion"
	case got.Phase == PhaseSucceeded:
		res.Outcome = OutcomeSucceeded
	default:
		res.Outcome, res.Error = OutcomeFailed, got.Message
	}
	if res.Outcome == OutcomeSucceeded {
		status.Phase = TaskPhaseDone
	} else {
		status.Phase, status.Error = TaskPhaseFailed, res.Error
	}
	return res, nil
}
