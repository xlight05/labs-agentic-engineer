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
	"errors"
	"time"

	"go.temporal.io/sdk/workflow"
)

// Shared per-issue lifecycle phases. TaskFlowWorkflow composes both;
// ValidationFlowWorkflow reuses runMergePhase for the single validation PR.
// The helpers mutate the caller's status so the QueryStatus view stays live,
// and return the failure message as an error (the caller owns the terminal
// bookkeeping via its own fail path).

// runCodingPhase dispatches the coding agent through the funnel and blocks
// until the PR opens, the coding job fails, or codingWaitTimeout elapses.
// Mutates status (Phase→coding, ExecutionID, PRNumber). The coding agent's
// success IS the PR opening (§7).
func runCodingPhase(ctx workflow.Context, orgID, projectID, repo string, issue int, status *TaskFlowStatus) (PRSignal, error) {
	status.Phase = TaskPhaseCoding
	var executionID string
	if err := workflow.ExecuteActivity(withDefaultActivityOpts(ctx), (*Activities).DispatchCoding, DispatchCodingInput{
		OrgID: orgID, ProjectID: projectID, Repo: repo, Issue: issue,
	}).Get(ctx, &executionID); err != nil {
		return PRSignal{}, errors.New("dispatch coding: " + err.Error())
	}
	status.ExecutionID = executionID

	// Wait for the coding attempt to end: pr-opened (success) or job-status
	// failed.
	prOpened := workflow.GetSignalChannel(ctx, SigPROpened)
	jobStatus := workflow.GetSignalChannel(ctx, SigJobStatus)
	var pr PRSignal
	var jobFailed *RunStatusSignal
	timer := workflow.NewTimer(ctx, codingWaitTimeout)
	done := false
	for !done {
		sel := workflow.NewSelector(ctx)
		sel.AddReceive(prOpened, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(ctx, &pr)
			done = true
		})
		sel.AddReceive(jobStatus, func(c workflow.ReceiveChannel, _ bool) {
			var s RunStatusSignal
			c.Receive(ctx, &s)
			if s.Phase == PhaseFailed {
				jobFailed = &s
				done = true
			}
		})
		sel.AddFuture(timer, func(workflow.Future) { done = true })
		sel.Select(ctx)
	}
	if jobFailed != nil {
		return PRSignal{}, errors.New("coding job failed: " + jobFailed.Message)
	}
	if pr.PRNumber == 0 {
		return PRSignal{}, errors.New("timed out waiting for the pull request")
	}
	status.PRNumber = pr.PRNumber
	return pr, nil
}

// runMergePhase drives the merge phase for prNumber (gate merge-pr). Auto →
// platform squash-merges. Manual → wait for an approve decision (then merge)
// OR an external human merge (pr-merged); a reject or pr-rejected fails the
// phase. Unless a human merge already arrived, it then awaits the merge
// webhook confirmation (pr-rejected there means the merge did not stick).
// Mutates status (Phase→merging, PendingGate, Error). nil == merged.
func runMergePhase(ctx workflow.Context, orgID, projectID string, prNumber int, gates GateConfig, status *TaskFlowStatus) error {
	status.Phase = TaskPhaseMerging
	prMerged := workflow.GetSignalChannel(ctx, SigPRMerged)
	prRejected := workflow.GetSignalChannel(ctx, SigPRRejected)
	merged := false
	if gates.IsAuto(GateMergePR) {
		if err := workflow.ExecuteActivity(withDefaultActivityOpts(ctx), (*Activities).MergePR, MergePRInput{
			OrgID: orgID, ProjectID: projectID, PRNumber: prNumber,
		}).Get(ctx, nil); err != nil {
			return errors.New("merge pr: " + err.Error())
		}
	} else {
		status.PendingGate = GateMergePR
		gateCh := workflow.GetSignalChannel(ctx, SigGateDecision)
		timer := workflow.NewTimer(ctx, mergeWaitTimeout)
		decided := false
		for !decided {
			sel := workflow.NewSelector(ctx)
			sel.AddReceive(gateCh, func(c workflow.ReceiveChannel, _ bool) {
				var d GateDecisionSignal
				c.Receive(ctx, &d)
				if d.Gate != GateMergePR {
					return
				}
				decided = true
				if !d.Approve {
					status.Error = "merge-pr gate rejected: " + d.Note
					return
				}
				if err := workflow.ExecuteActivity(withDefaultActivityOpts(ctx), (*Activities).MergePR, MergePRInput{
					OrgID: orgID, ProjectID: projectID, PRNumber: prNumber,
				}).Get(ctx, nil); err != nil {
					status.Error = "merge pr: " + err.Error()
				}
			})
			sel.AddReceive(prMerged, func(c workflow.ReceiveChannel, _ bool) {
				var m PRSignal
				c.Receive(ctx, &m)
				merged, decided = true, true // human merged on GitHub
			})
			sel.AddReceive(prRejected, func(c workflow.ReceiveChannel, _ bool) {
				var r PRSignal
				c.Receive(ctx, &r)
				decided = true
				status.Error = "pull request closed without merging"
			})
			sel.AddFuture(timer, func(workflow.Future) {
				decided = true
				status.Error = "timed out waiting for merge approval"
			})
			sel.Select(ctx)
		}
		status.PendingGate = ""
		if status.Error != "" {
			return errors.New(status.Error)
		}
	}

	// Await the merge webhook confirmation (unless a human merge already
	// arrived above). pr-rejected here means the merge did not stick.
	if !merged {
		timer := workflow.NewTimer(ctx, mergeWaitTimeout)
		done := false
		for !done {
			sel := workflow.NewSelector(ctx)
			sel.AddReceive(prMerged, func(c workflow.ReceiveChannel, _ bool) {
				var m PRSignal
				c.Receive(ctx, &m)
				merged, done = true, true
			})
			sel.AddReceive(prRejected, func(c workflow.ReceiveChannel, _ bool) {
				var r PRSignal
				c.Receive(ctx, &r)
				done = true
			})
			sel.AddFuture(timer, func(workflow.Future) { done = true })
			sel.Select(ctx)
		}
		if !merged {
			return errors.New("pull request was not merged")
		}
	}
	return nil
}

// awaitRunStatus blocks for one RunStatusSignal or the timeout. ok=false on
// timeout.
func awaitRunStatus(ctx workflow.Context, ch workflow.ReceiveChannel, timeout time.Duration) (RunStatusSignal, bool) {
	var got RunStatusSignal
	received := false
	timer := workflow.NewTimer(ctx, timeout)
	sel := workflow.NewSelector(ctx)
	sel.AddReceive(ch, func(c workflow.ReceiveChannel, _ bool) {
		c.Receive(ctx, &got)
		received = true
	})
	sel.AddFuture(timer, func(workflow.Future) {})
	sel.Select(ctx)
	return got, received
}

// markRunStatus best-effort records the run's terminal status in the lookup
// index (the workflow's own truth is Temporal; this keeps the DB index fresh
// for signalers and the list endpoint).
func markRunStatus(ctx workflow.Context, workflowID, statusStr, reason string) {
	_ = workflow.ExecuteActivity(withDefaultActivityOpts(ctx), (*Activities).SetWorkflowRunStatus, SetWorkflowRunStatusInput{
		WorkflowID: workflowID,
		Status:     statusStr,
		Reason:     reason,
	}).Get(ctx, nil)
}

// withDefaultActivityOpts returns a context carrying the default activity
// options for short adapter activities (2m start-to-close per attempt; no
// explicit RetryPolicy, so the Temporal SERVER default applies — unlimited
// attempts with backoff).
func withDefaultActivityOpts(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
	})
}
