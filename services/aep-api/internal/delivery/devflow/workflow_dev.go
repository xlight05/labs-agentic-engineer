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

	"github.com/wso2/aep/aep-api/models"
)

// DevFlowInput starts a per-version development workflow: re-validate the
// spec at the tag, plan tasks, fan out task workflows, validate. Tag is the
// spec version this run builds — always cut by the build endpoint (after the
// whole-spec hard gate) BEFORE the workflow starts.
type DevFlowInput struct {
	OrgID     string `json:"orgId"`
	ProjectID string `json:"projectId"`
	// Repo is the project's "owner/name" — resolved by the API before start so
	// task children can dispatch + be signaled by the webhook handlers.
	Repo  string     `json:"repo"`
	Tag   string     `json:"tag"`
	Gates GateConfig `json:"gates"`
	// Provision carries the user's build-drawer inputs (issue #164): the
	// non-secret config, staged secret references, platform-resource params, and
	// approvals the workflow's provisioning step (Task 3) authors OC bindings +
	// gate issues from. Empty when the build needs no provisioning. Secret VALUES
	// are never carried here — only SM-API references (SecretRefByEnv).
	Provision []ProvisionInput `json:"provision,omitempty"`
}

// ProvisionInput is one dependency's resolved provisioning payload, produced by
// the build endpoint from the drawer inputs and carried into the dev workflow.
// It is the shared wire contract between POST /build (which stages secrets to
// SM-API and derives references) and the workflow's provisioning step (which
// authors the OC Resource model + aep:provision gates). A raw secret value is
// NEVER placed here — SecretRefByEnv holds the SM-API reference per env instead.
type ProvisionInput struct {
	Component  string `json:"component"`
	Dependency string `json:"dependency"`
	Kind       string `json:"kind"`
	// external non-secret config by key.
	Config map[string]string `json:"config,omitempty"`
	// external: the SM-API secret reference per env (NOT the secret value).
	SecretRefByEnv map[string]string `json:"secretRefByEnv,omitempty"`
	// platform-resource: provisioning params (mixed scalar types).
	Parameters map[string]any `json:"parameters,omitempty"`
	// platform-resource / org-service: the user's approval.
	Approved bool `json:"approved,omitempty"`
}

// DevFlowStatus is the QueryStatus result for a dev workflow.
type DevFlowStatus struct {
	Phase       string       `json:"phase"`
	Tag         string       `json:"tag,omitempty"`
	PendingGate string       `json:"pendingGate,omitempty"`
	Tasks       []DevTaskRef `json:"tasks,omitempty"`
	// Validation is the validating phase's outcome (the ValidationFlowWorkflow
	// child). Nil until the validating phase runs.
	Validation *ValidationRef `json:"validation,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// DevTaskRef is a child task's summary in the dev workflow status.
type DevTaskRef struct {
	Issue      int    `json:"issue"`
	WorkflowID string `json:"workflowId,omitempty"`
	Phase      string `json:"phase,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
}

// ValidationRef is the validating phase's summary in the dev workflow status:
// the orchestrator child plus its per-lane results. Outcome carries
// "skipped: no acceptance criteria" when there was nothing to validate.
type ValidationRef struct {
	WorkflowID string       `json:"workflowId,omitempty"`
	Phase      string       `json:"phase,omitempty"`
	Outcome    string       `json:"outcome,omitempty"`
	Lanes      []DevTaskRef `json:"lanes,omitempty"`
}

// DevFlow phase values.
const (
	DevPhaseValidatingSpec = "validating-spec"
	DevPhasePlanning       = "planning"
	DevPhaseProvisioning   = "provisioning"
	DevPhaseExecuting      = "executing"
	DevPhaseValidating     = "validating"
	DevPhaseDone           = "done"
	DevPhaseFailed         = "failed"
)

// DevFlowWorkflow is the per-version development lifecycle: re-validate the
// spec the endpoint tagged, plan the tasks, fan out dependency-aware task
// child workflows, validate, complete. Each gate can pause for human approval
// (default auto). Design generation is NOT part of the workflow — the build
// endpoint rejects an unbuildable spec before the tag is cut, so the tag this
// run receives always names a validated requirements+design pair.
func DevFlowWorkflow(ctx workflow.Context, in DevFlowInput) (DevFlowStatus, error) {
	status := DevFlowStatus{Phase: DevPhaseValidatingSpec, Tag: in.Tag}
	if err := workflow.SetQueryHandler(ctx, QueryStatus, func() (DevFlowStatus, error) {
		return status, nil
	}); err != nil {
		status.Phase, status.Error = DevPhaseFailed, err.Error()
		return status, err
	}
	gates := newGateKeeper(in.Gates, func(g string) { status.PendingGate = g })
	info := workflow.GetInfo(ctx)

	fail := func(msg string) (DevFlowStatus, error) {
		status.Phase, status.Error = DevPhaseFailed, msg
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
	status.Phase = DevPhasePlanning
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
	status.Phase = DevPhaseProvisioning
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
	status.Phase = DevPhaseExecuting
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
	status.Phase = DevPhaseValidating
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
	status.Validation = &ValidationRef{WorkflowID: vwid, Phase: TaskPhaseStarting}
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
		status.Validation.Phase, status.Validation.Outcome = TaskPhaseFailed, OutcomeFailed
		return fail("validation run failed: " + err.Error())
	}
	for _, l := range vres.Lanes {
		status.Validation.Lanes = append(status.Validation.Lanes, DevTaskRef{Issue: l.Issue, Phase: TaskPhaseDone, Outcome: l.Outcome})
	}
	switch vres.Outcome {
	case OutcomeSucceeded:
		status.Validation.Phase, status.Validation.Outcome = TaskPhaseDone, OutcomeSucceeded
	case ValidationOutcomeSkipped:
		status.Validation.Phase, status.Validation.Outcome = TaskPhaseDone, "skipped: "+vres.Reason
	default:
		status.Validation.Phase, status.Validation.Outcome = TaskPhaseFailed, vres.Outcome
		return fail("validation run did not succeed: " + orEmpty(vres.Reason, vres.Outcome))
	}

	if ok, d := gates.await(ctx, GateComplete); !ok {
		return fail("complete gate rejected: " + d.Note)
	}
	status.Phase = DevPhaseDone
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

// DevWorkflowID builds the deterministic dev workflow id
// (devflow-<org>-<project>-<tag>) — shared by the build endpoint (start +
// status lookup) and the workflow_runs index.
func DevWorkflowID(orgID, projectID, tag string) string {
	return fmt.Sprintf("devflow-%s-%s-%s", orgID, projectID, tag)
}

// scheduleTasks runs the planned tasks as child workflows, respecting the
// dependency graph: a task starts once all its dependencies have succeeded;
// tasks whose dependency failed are skipped. Independent tasks run in
// parallel. Deterministic — it iterates the stable task slice, never a map.
func scheduleTasks(ctx workflow.Context, in DevFlowInput, tag string, tasks []PlannedTask, status *DevFlowStatus) {
	// Seed the status task list in plan order.
	status.Tasks = make([]DevTaskRef, 0, len(tasks))
	for _, t := range tasks {
		status.Tasks = append(status.Tasks, DevTaskRef{Issue: t.Issue, Phase: "pending"})
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
			case tr.Outcome == OutcomeSucceeded:
				done++
			case tr.Phase == TaskPhaseFailed:
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
				setTaskRef(status, t.Issue, "", TaskPhaseFailed, OutcomeSkippedDepFai)
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
			setTaskRef(status, t.Issue, wid, TaskPhaseStarting, "")
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
					setTaskRef(status, t.Issue, "", TaskPhaseFailed, OutcomeSkippedDepFai)
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
					setTaskRef(status, cr.task.Issue, "", TaskPhaseFailed, OutcomeFailed)
					return
				}
				if res.Outcome == OutcomeSucceeded {
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
func notSucceeded(tasks []DevTaskRef) []string {
	var out []string
	for _, t := range tasks {
		if t.Outcome != OutcomeSucceeded {
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
func setTaskRef(status *DevFlowStatus, issue int, workflowID, phase, outcome string) {
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
	status.Tasks = append(status.Tasks, DevTaskRef{Issue: issue, WorkflowID: workflowID, Phase: phase, Outcome: outcome})
}

// Phase returns a display phase for a finished task result.
func (r TaskFlowResult) Phase() string {
	if r.Outcome == OutcomeSucceeded {
		return TaskPhaseDone
	}
	return TaskPhaseFailed
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
