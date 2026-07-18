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
	"time"

	"github.com/wso2/aep/aep-api/models"
	"go.temporal.io/sdk/workflow"
)

// QueryStatus is the query name every devflow workflow exposes so the
// API/console can read live workflow state.
const QueryStatus = "status"

// TaskFlowInput starts a per-Task workflow: dispatch the coding agent, wait
// for the PR, merge, build, deploy. Repo is "owner/name"; Issue is the task's
// GitHub issue number.
type TaskFlowInput struct {
	OrgID            string     `json:"orgId"`
	ProjectID        string     `json:"projectId"`
	Repo             string     `json:"repo"`
	Issue            int        `json:"issue"`
	Tag              string     `json:"tag"`
	ParentWorkflowID string     `json:"parentWorkflowId,omitempty"`
	Gates            GateConfig `json:"gates"`
}

// TaskFlowStatus is the QueryStatus result for a task workflow.
type TaskFlowStatus struct {
	Phase       string `json:"phase"`
	Issue       int    `json:"issue"`
	ExecutionID string `json:"executionId,omitempty"`
	PRNumber    int    `json:"prNumber,omitempty"`
	PendingGate string `json:"pendingGate,omitempty"`
	Error       string `json:"error,omitempty"`
}

// TaskFlowResult is what the workflow returns to its parent.
type TaskFlowResult struct {
	Issue   int    `json:"issue"`
	Outcome string `json:"outcome"` // succeeded | failed | skipped-dep-failed
	Error   string `json:"error,omitempty"`
}

// TaskFlow phase values.
const (
	TaskPhaseStarting  = "starting"
	TaskPhaseCoding    = "coding"
	TaskPhaseMerging   = "merging"
	TaskPhaseBuilding  = "building"
	TaskPhaseDeploying = "deploying"
	TaskPhaseDone      = "done"
	TaskPhaseFailed    = "failed"
)

// Signal-wait deadlines. Generous — a stuck run surfaces via the status query
// rather than a silent hang; the workflow run timeout is the final backstop.
const (
	codingWaitTimeout = 2 * time.Hour
	mergeWaitTimeout  = 30 * time.Minute
	buildWaitTimeout  = 30 * time.Minute
	deployWaitTimeout = 15 * time.Minute
)

// Outcome values for TaskFlowResult.
const (
	OutcomeSucceeded     = "succeeded"
	OutcomeFailed        = "failed"
	OutcomeSkippedDepFai = "skipped-dep-failed"
)

// TaskFlowWorkflow is the per-Task lifecycle: dispatch the coding agent, wait
// for the PR, merge (auto or human-gated), wait for the build, wait for the
// deploy, then report to the parent. Every wait is driven by a signal the
// existing webhook handlers / watchers emit (see signaler.go), not polling.
func TaskFlowWorkflow(ctx workflow.Context, in TaskFlowInput) (TaskFlowResult, error) {
	status := TaskFlowStatus{Phase: TaskPhaseStarting, Issue: in.Issue}
	if err := workflow.SetQueryHandler(ctx, QueryStatus, func() (TaskFlowStatus, error) {
		return status, nil
	}); err != nil {
		return TaskFlowResult{Issue: in.Issue, Outcome: OutcomeFailed, Error: err.Error()}, err
	}
	gates := newGateKeeper(in.Gates, func(g string) { status.PendingGate = g })
	info := workflow.GetInfo(ctx)

	fail := func(msg string) (TaskFlowResult, error) {
		status.Phase, status.Error = TaskPhaseFailed, msg
		markRunStatus(ctx, info.WorkflowExecution.ID, models.WorkflowStatusFailed, msg)
		return TaskFlowResult{Issue: in.Issue, Outcome: OutcomeFailed, Error: msg}, nil
	}

	if err := workflow.ExecuteActivity(withDefaultActivityOpts(ctx), (*Activities).RecordWorkflowRun, RecordWorkflowRunInput{
		WorkflowID:       info.WorkflowExecution.ID,
		RunID:            info.WorkflowExecution.RunID,
		Kind:             models.WorkflowKindTask,
		OrgID:            in.OrgID,
		ProjectID:        in.ProjectID,
		Tag:              in.Tag,
		Repo:             in.Repo,
		IssueNumber:      in.Issue,
		ParentWorkflowID: in.ParentWorkflowID,
	}).Get(ctx, nil); err != nil {
		return fail("record workflow run: " + err.Error())
	}

	// Gate: start coding (auto by default).
	if ok, d := gates.await(ctx, GateStartCoding); !ok {
		return fail("start-coding gate rejected: " + d.Note)
	}

	// Dispatch the coding agent through the funnel and wait for the PR — the
	// coding agent's success IS the PR opening (§7).
	pr, err := runCodingPhase(ctx, in.OrgID, in.ProjectID, in.Repo, in.Issue, &status)
	if err != nil {
		return fail(err.Error())
	}

	// Merge phase (gate merge-pr).
	if err := runMergePhase(ctx, in.OrgID, in.ProjectID, pr.PRNumber, in.Gates, &status); err != nil {
		return fail(err.Error())
	}

	// Wait for the build (spawned by the merge webhook, driven by the funnel).
	status.Phase = TaskPhaseBuilding
	buildStatus := workflow.GetSignalChannel(ctx, SigBuildStatus)
	if s, ok := awaitRunStatus(ctx, buildStatus, buildWaitTimeout); !ok {
		return fail("timed out waiting for the build")
	} else if s.Phase == PhaseFailed {
		return fail("build failed: " + s.Message)
	}

	// Wait for the deploy signal.
	status.Phase = TaskPhaseDeploying
	deployStatus := workflow.GetSignalChannel(ctx, SigDeployStatus)
	if s, ok := awaitRunStatus(ctx, deployStatus, deployWaitTimeout); !ok {
		return fail("timed out waiting for the deploy")
	} else if s.Phase == PhaseFailed {
		return fail("deploy failed: " + s.Message)
	}

	status.Phase = TaskPhaseDone
	markRunStatus(ctx, info.WorkflowExecution.ID, models.WorkflowStatusCompleted, "")
	return TaskFlowResult{Issue: in.Issue, Outcome: OutcomeSucceeded}, nil
}
