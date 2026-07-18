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
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/models"
)

// The dev-workflow I/O vocabulary (DevFlowInput/DevFlowStatus/DevTaskRef/
// ValidationRef/ProvisionInput, the DevPhase* constants and the DevWorkflowID
// builder) lives in the delivery ROOT (delivery.workflow_vocab.go): it is the
// contract the build starter and this workflow share, and build must not import
// this sub-package. Referenced here as delivery.* (§10.3.1).

// DevFlowWorkflow is the per-version development lifecycle: re-validate the
// spec the endpoint tagged, plan the tasks, fan out dependency-aware task
// child workflows, validate, complete. Each gate can pause for human approval
// (default auto). Design generation is NOT part of the workflow — the build
// endpoint rejects an unbuildable spec before the tag is cut, so the tag this
// run receives always names a validated requirements+design pair.
func DevFlowWorkflow(ctx workflow.Context, in delivery.DevFlowInput) (delivery.DevFlowStatus, error) {
	status := delivery.DevFlowStatus{Phase: delivery.DevPhaseValidatingSpec, Tag: in.Tag}
	if err := workflow.SetQueryHandler(ctx, delivery.QueryStatus, func() (delivery.DevFlowStatus, error) {
		return status, nil
	}); err != nil {
		status.Phase, status.Error = delivery.DevPhaseFailed, err.Error()
		return status, err
	}
	gates := newGateKeeper(in.Gates, func(g string) { status.PendingGate = g })
	info := workflow.GetInfo(ctx)

	fail := func(msg string) (delivery.DevFlowStatus, error) {
		status.Phase, status.Error = delivery.DevPhaseFailed, msg
		markRunStatus(ctx, info.WorkflowExecution.ID, models.WorkflowStatusFailed, msg)
		return status, nil
	}

	if err := workflow.ExecuteActivity(withDefaultActivityOpts(ctx), (*Activities).RecordWorkflowRun, RecordWorkflowRunInput{
		WorkflowID: info.WorkflowExecution.ID,
		RunID:      info.WorkflowExecution.RunID,
		Kind:       models.WorkflowKindDev,
		OrgID:      in.OrgID,
		ProjectID:  in.ProjectID,
		Tag:        in.Tag,
	}).Get(ctx, nil); err != nil {
		return fail("record workflow run: " + err.Error())
	}

	// 1. Defensive re-validation at the pinned tag: the endpoint validated the
	// spec before cutting it, but the tag is what this run plans from — an
	// externally-cut or corrupted tag must fail here, not mid-execution.
	ref := ProjectRef{OrgID: in.OrgID, ProjectID: in.ProjectID}
	reqTag := in.Tag
	if err := workflow.ExecuteActivity(withDefaultActivityOpts(ctx), (*Activities).ValidateSpecAtTag, ValidateSpecInput{
		OrgID: in.OrgID, ProjectID: in.ProjectID, Tag: reqTag,
	}).Get(ctx, nil); err != nil {
		return fail("validate spec at tag: " + err.Error())
	}

	// 2. Plan the tasks.
	if ok, d := gates.await(ctx, GatePlan); !ok {
		return fail("plan gate rejected: " + d.Note)
	}
	status.Phase = delivery.DevPhasePlanning
	var tasks []PlannedTask
	if err := workflow.ExecuteActivity(planActivityOpts(ctx), (*Activities).RunPlan, ref).Get(ctx, &tasks); err != nil {
		return fail("run plan: " + err.Error())
	}
	if cyc := detectDepCycle(tasks); len(cyc) > 0 {
		return fail("dependency cycle detected: " + strings.Join(cyc, " → "))
	}

	// 2b. Provision dependencies (issue #164): mint the aep:provision gates and
	// author each dependency the build drawer supplied by kind — external
	// synchronously (its gate closes here), platform-resource async (the
	// readiness watcher finishes it). This runs BEFORE any coding task is
	// scheduled so the funnel's provision gates exist and the synchronous
	// external gates are closed. Provisioning failures fail the run.
	status.Phase = delivery.DevPhaseProvisioning
	var pfails []ProvisionFailure
	if err := workflow.ExecuteActivity(withDefaultActivityOpts(ctx), (*Activities).ProvisionDependencies, ProvisionDepsInput{
		OrgID: in.OrgID, ProjectID: in.ProjectID, Tag: reqTag, Inputs: in.Provision,
	}).Get(ctx, &pfails); err != nil {
		return fail("provision dependencies: " + err.Error())
	}
	if len(pfails) > 0 {
		return fail("provisioning failed: " + summarizeProvisionFailures(pfails))
	}

	// 3. Execute — dependency-aware task child workflows.
	status.Phase = delivery.DevPhaseExecuting
	scheduleTasks(ctx, in, reqTag, tasks, &status)

	// 4. Validate.
	// 4a. Quality bar: every planned task must have succeeded. A failed or
	// dependency-skipped task means the system was not fully implemented +
	// deployed, so there is nothing coherent to validate — fail fast, before
	// asking for the validate gate (a doomed run never waits for approval).
	if unmet := notSucceeded(status.Tasks); len(unmet) > 0 {
		return fail(fmt.Sprintf("validating: %d task(s) did not succeed: %s", len(unmet), strings.Join(unmet, ", ")))
	}
	// 4b. Validate gate (human pause point, auto by default).
	if ok, d := gates.await(ctx, GateValidate); !ok {
		return fail("validate gate rejected: " + d.Note)
	}
	status.Phase = delivery.DevPhaseValidating
	// 4c. Consistency check: every design component has a Ready deployment
	// (a reachable endpoint). Independent verification against OpenChoreo of
	// what the task outcomes imply.
	if err := workflow.ExecuteActivity(withDefaultActivityOpts(ctx), (*Activities).Validate, ValidateInput{
		OrgID: in.OrgID, ProjectID: in.ProjectID, Tag: reqTag,
	}).Get(ctx, nil); err != nil {
		return fail("validate: " + err.Error())
	}
	// 4d. Run the validating phase as its own child workflow tree: the
	// orchestrator resolves the project's validation issue (skips when no
	// acceptance criteria were authored), fans out the validation lanes in
	// parallel, and merges the single validation PR. A mechanical failure
	// (crash/timeout/PR rejected) fails the run; a failing test suite still
	// merges a PR + report and succeeds — that verdict is the human's to read
	// at the complete gate.
	vwid := validationFlowWorkflowID(in.OrgID, in.ProjectID, reqTag)
	status.Validation = &delivery.ValidationRef{WorkflowID: vwid, Phase: delivery.TaskPhaseStarting}
	vctx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:        vwid,
		ParentClosePolicy: enumsParentClosePolicyTerminate(),
	})
	var vres ValidationFlowResult
	if err := workflow.ExecuteChildWorkflow(vctx, ValidationFlowWorkflow, ValidationFlowInput{
		OrgID:         in.OrgID,
		ProjectID:     in.ProjectID,
		Repo:          in.Repo,
		Tag:           reqTag,
		DevWorkflowID: info.WorkflowExecution.ID,
		Gates:         in.Gates,
	}).Get(ctx, &vres); err != nil {
		status.Validation.Phase, status.Validation.Outcome = delivery.TaskPhaseFailed, delivery.OutcomeFailed
		return fail("validation run failed: " + err.Error())
	}
	for _, l := range vres.Lanes {
		status.Validation.Lanes = append(status.Validation.Lanes, delivery.DevTaskRef{Issue: l.Issue, Phase: delivery.TaskPhaseDone, Outcome: l.Outcome})
	}
	switch vres.Outcome {
	case delivery.OutcomeSucceeded:
		status.Validation.Phase, status.Validation.Outcome = delivery.TaskPhaseDone, delivery.OutcomeSucceeded
	case ValidationOutcomeSkipped:
		status.Validation.Phase, status.Validation.Outcome = delivery.TaskPhaseDone, "skipped: "+vres.Reason
	default:
		status.Validation.Phase, status.Validation.Outcome = delivery.TaskPhaseFailed, vres.Outcome
		return fail("validation run did not succeed: " + orEmpty(vres.Reason, vres.Outcome))
	}

	if ok, d := gates.await(ctx, GateComplete); !ok {
		return fail("complete gate rejected: " + d.Note)
	}
	status.Phase = delivery.DevPhaseDone
	markRunStatus(ctx, info.WorkflowExecution.ID, models.WorkflowStatusCompleted, "")
	return status, nil
}

// summarizeProvisionFailures renders provisioning failures as a compact
// "component/dependency: reason" list for the run's failure message.
func summarizeProvisionFailures(fs []ProvisionFailure) string {
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		parts = append(parts, f.Component+"/"+f.Dependency+": "+f.Reason)
	}
	return strings.Join(parts, "; ")
}

// scheduleTasks runs the planned tasks as child workflows, respecting the
// dependency graph: a task starts once all its dependencies have succeeded;
// tasks whose dependency failed are skipped. Independent tasks run in
// parallel. Deterministic — it iterates the stable task slice, never a map.
func scheduleTasks(ctx workflow.Context, in delivery.DevFlowInput, tag string, tasks []PlannedTask, status *delivery.DevFlowStatus) {
	// Seed the status task list in plan order.
	status.Tasks = make([]delivery.DevTaskRef, 0, len(tasks))
	for _, t := range tasks {
		status.Tasks = append(status.Tasks, delivery.DevTaskRef{Issue: t.Issue, Phase: "pending"})
	}

	present := map[string]bool{}
	for _, t := range tasks {
		present[strings.ToLower(t.Key)] = true
	}
	succeeded := map[string]bool{}
	failed := map[string]bool{}
	started := map[int]bool{}

	type childRun struct {
		task   PlannedTask
		future workflow.ChildWorkflowFuture
	}
	var running []childRun
	finished := 0

	// Task tally for the lookup index (the overview build stage): absolute
	// values DERIVED from status.Tasks — setTaskRef is the single transition
	// seam, so the tally cannot desync from the run's real progress. Flushed
	// from the loop body (never inside a selector callback, which must not
	// block); the first flush publishes the plan size with a zero tally.
	// Best-effort with bounded retries: a dropped write is rewritten by the
	// next transition's absolute values, and a DB outage must never stall
	// task dispatch.
	lastDone, lastFailed := -1, -1
	flushCounts := func() {
		done, failedCount := 0, 0
		for _, tr := range status.Tasks {
			switch {
			case tr.Outcome == delivery.OutcomeSucceeded:
				done++
			case tr.Phase == delivery.TaskPhaseFailed:
				failedCount++
			}
		}
		if done == lastDone && failedCount == lastFailed {
			return
		}
		lastDone, lastFailed = done, failedCount
		info := workflow.GetInfo(ctx).WorkflowExecution
		_ = workflow.ExecuteActivity(countsActivityOpts(ctx), (*Activities).SetWorkflowRunTaskCounts, SetWorkflowRunTaskCountsInput{
			WorkflowID: info.ID,
			RunID:      info.RunID,
			Total:      len(tasks),
			Done:       done,
			Failed:     failedCount,
		}).Get(ctx, nil)
	}

	for finished < len(tasks) {
		// Start every ready task (stable slice order).
		for i := range tasks {
			t := tasks[i]
			if started[t.Issue] {
				continue
			}
			if depFailed(t, failed) {
				started[t.Issue] = true
				failed[strings.ToLower(t.Key)] = true
				finished++
				setTaskRef(status, t.Issue, "", delivery.TaskPhaseFailed, delivery.OutcomeSkippedDepFai)
				continue
			}
			if !depsSatisfied(t, succeeded, present) {
				continue
			}
			wid := taskWorkflowID(in.OrgID, in.ProjectID, tag, t.Issue)
			cctx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
				WorkflowID:        wid,
				ParentClosePolicy: enumsParentClosePolicyTerminate(),
			})
			f := workflow.ExecuteChildWorkflow(cctx, TaskFlowWorkflow, TaskFlowInput{
				OrgID:            in.OrgID,
				ProjectID:        in.ProjectID,
				Repo:             in.Repo,
				Issue:            t.Issue,
				Tag:              tag,
				ParentWorkflowID: workflow.GetInfo(ctx).WorkflowExecution.ID,
				Gates:            in.Gates,
			})
			started[t.Issue] = true
			running = append(running, childRun{task: t, future: f})
			setTaskRef(status, t.Issue, wid, delivery.TaskPhaseStarting, "")
		}

		flushCounts()

		if len(running) == 0 {
			// No task is runnable and none is running — remaining are blocked by
			// failed deps (or an unresolved cycle the fast-fail missed). Skip them.
			for i := range tasks {
				t := tasks[i]
				if !started[t.Issue] {
					started[t.Issue] = true
					failed[strings.ToLower(t.Key)] = true
					finished++
					setTaskRef(status, t.Issue, "", delivery.TaskPhaseFailed, delivery.OutcomeSkippedDepFai)
				}
			}
			flushCounts()
			break
		}

		// Wait for one child to complete.
		completedIdx := -1
		sel := workflow.NewSelector(ctx)
		for idx := range running {
			i := idx
			cr := running[idx]
			sel.AddFuture(cr.future, func(f workflow.Future) {
				completedIdx = i
				var res TaskFlowResult
				key := strings.ToLower(cr.task.Key)
				if err := f.Get(ctx, &res); err != nil {
					failed[key] = true
					setTaskRef(status, cr.task.Issue, "", delivery.TaskPhaseFailed, delivery.OutcomeFailed)
					return
				}
				if res.Outcome == delivery.OutcomeSucceeded {
					succeeded[key] = true
				} else {
					failed[key] = true
				}
				setTaskRef(status, cr.task.Issue, "", res.Phase(), res.Outcome)
			})
		}
		sel.Select(ctx)
		if completedIdx >= 0 {
			running = append(running[:completedIdx], running[completedIdx+1:]...)
			finished++
		}
		flushCounts()
	}
}

// notSucceeded returns the issue labels of every task whose outcome is not
// "succeeded" (failed or dependency-skipped) — the quality bar the validating
// phase enforces before running validation.
func notSucceeded(tasks []delivery.DevTaskRef) []string {
	var out []string
	for _, t := range tasks {
		if t.Outcome != delivery.OutcomeSucceeded {
			out = append(out, fmt.Sprintf("#%d (%s)", t.Issue, orEmpty(t.Outcome, "incomplete")))
		}
	}
	return out
}

// orEmpty returns fallback when s is empty.
func orEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// setTaskRef updates (in place) the status entry for issue, filling only the
// non-empty fields so a later update does not clobber an earlier workflow id.
func setTaskRef(status *delivery.DevFlowStatus, issue int, workflowID, phase, outcome string) {
	for i := range status.Tasks {
		if status.Tasks[i].Issue != issue {
			continue
		}
		if workflowID != "" {
			status.Tasks[i].WorkflowID = workflowID
		}
		if phase != "" {
			status.Tasks[i].Phase = phase
		}
		if outcome != "" {
			status.Tasks[i].Outcome = outcome
		}
		return
	}
	status.Tasks = append(status.Tasks, delivery.DevTaskRef{Issue: issue, WorkflowID: workflowID, Phase: phase, Outcome: outcome})
}

// Phase returns a display phase for a finished task result.
func (r TaskFlowResult) Phase() string {
	if r.Outcome == delivery.OutcomeSucceeded {
		return delivery.TaskPhaseDone
	}
	return delivery.TaskPhaseFailed
}

// enumsParentClosePolicyTerminate returns the TERMINATE parent-close policy so
// canceling a dev run tears down its still-running task children.
func enumsParentClosePolicyTerminate() enumspb.ParentClosePolicy {
	return enumspb.PARENT_CLOSE_POLICY_TERMINATE
}

// planActivityOpts returns the activity options for the long-running plan
// activity (heartbeating, single attempt — issue creation is not blind-retry
// safe; the plan service's own lock guards concurrent duplicates).
func planActivityOpts(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
}

// countsActivityOpts bounds the informational tally write: without an
// explicit RetryPolicy the SERVER default applies (unlimited attempts), and
// flushCounts blocks the dispatch loop on .Get — a DB outage would stall
// task fan-out for a write whose loss the next transition heals anyway.
func countsActivityOpts(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
}

// taskWorkflowID builds the deterministic child workflow id
// (taskflow-<org>-<project>-<tag>-<issueNumber>).
func taskWorkflowID(orgID, projectID, tag string, issue int) string {
	return fmt.Sprintf("taskflow-%s-%s-%s-%d", orgID, projectID, tag, issue)
}
