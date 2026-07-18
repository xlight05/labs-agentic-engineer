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
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/wso2/aep/aep-api/models"
	"go.temporal.io/sdk/testsuite"
)

// The lane child is a pure wait point: the orchestrator owns the issue's
// webhook signals and forwards the lane's terminal state via SigLaneStatus.

func TestValidationTaskWorkflow_CompletesOnForwardedSuccess(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigLaneStatus, LaneStatusSignal{Lane: LaneE2E, Phase: PhaseSucceeded})
	}, time.Second)

	env.ExecuteWorkflow(ValidationTaskWorkflow, ValidationTaskInput{
		Lane: LaneE2E, Issue: 9, ExecutionID: "exec-1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res ValidationLaneResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, LaneE2E, res.Kind)
	require.Equal(t, 9, res.Issue)
	require.Equal(t, OutcomeSucceeded, res.Outcome)
}

func TestValidationTaskWorkflow_FailsOnForwardedFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigLaneStatus, LaneStatusSignal{
			Lane: LaneE2E, Phase: PhaseFailed, Message: "coding job failed: boom",
		})
	}, time.Second)

	env.ExecuteWorkflow(ValidationTaskWorkflow, ValidationTaskInput{
		Lane: LaneE2E, Issue: 9, ExecutionID: "exec-1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res ValidationLaneResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, OutcomeFailed, res.Outcome)
	require.Contains(t, res.Error, "boom")
}

// No forwarded signal at all → the lane fails on its wait timeout (the test
// env auto-advances time while idle).
func TestValidationTaskWorkflow_TimesOut(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(ValidationTaskWorkflow, ValidationTaskInput{
		Lane: LaneE2E, Issue: 9, ExecutionID: "exec-1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res ValidationLaneResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, OutcomeFailed, res.Outcome)
	require.Contains(t, res.Error, "timed out")
}

// registerValidationFlowActivities mocks the orchestrator's activities and
// registers the REAL lane child (the pump → child forwarding is the behavior
// under test). validationIssue is what ResolveValidationTask returns (0 = no
// acceptance criteria). The returned pointer captures the last
// RecordWorkflowRun input so tests can pin the phase row's shape.
func registerValidationFlowActivities(env *testsuite.TestWorkflowEnvironment, validationIssue int) *RecordWorkflowRunInput {
	var acts *Activities
	recorded := &RecordWorkflowRunInput{}
	env.RegisterActivity(acts.RecordWorkflowRun)
	env.RegisterActivity(acts.SetWorkflowRunStatus)
	env.RegisterActivity(acts.ResolveValidationTask)
	env.RegisterActivity(acts.DispatchCoding)
	env.RegisterActivity(acts.MergePR)
	env.OnActivity(acts.RecordWorkflowRun, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { *recorded = args.Get(1).(RecordWorkflowRunInput) }).
		Return(nil)
	env.OnActivity(acts.SetWorkflowRunStatus, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.ResolveValidationTask, mock.Anything, mock.Anything).Return(validationIssue, nil)
	env.OnActivity(acts.DispatchCoding, mock.Anything, mock.Anything).Return("exec-e2e", nil)
	env.OnActivity(acts.MergePR, mock.Anything, mock.Anything).Return(nil)
	env.RegisterWorkflow(ValidationTaskWorkflow)
	return recorded
}

// No acceptance criteria (issue 0) → skipped result, and neither a run row
// nor a lane dispatch happens.
func TestValidationFlowWorkflow_SkipsWhenNoCriteria(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerValidationFlowActivities(env, 0)

	env.ExecuteWorkflow(ValidationFlowWorkflow, ValidationFlowInput{
		OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1",
		DevWorkflowID: "devflow-org1-proj1-v1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res ValidationFlowResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, ValidationOutcomeSkipped, res.Outcome)
	require.Contains(t, res.Reason, "no acceptance criteria")
	env.AssertNotCalled(t, "RecordWorkflowRun", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "DispatchCoding", mock.Anything, mock.Anything)
}

// Happy path: the e2e lane's runner opens the PR (pr-opened = lane success,
// forwarded to the real lane child), the single PR auto-merges, and the phase
// row is recorded as the dev run's kind=validation child.
func TestValidationFlowWorkflow_HappyPath(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	recorded := registerValidationFlowActivities(env, 99)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigPROpened, PRSignal{Repo: "org1/proj1", Issue: 99, PRNumber: 55})
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigPRMerged, PRSignal{Repo: "org1/proj1", Issue: 99, PRNumber: 55, MergeSHA: "abc"})
	}, 2*time.Second)

	env.ExecuteWorkflow(ValidationFlowWorkflow, ValidationFlowInput{
		OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1",
		DevWorkflowID: "devflow-org1-proj1-v1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res ValidationFlowResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, OutcomeSucceeded, res.Outcome)
	require.Equal(t, 55, res.PRNumber)
	require.Len(t, res.Lanes, 1)
	require.Equal(t, LaneE2E, res.Lanes[0].Kind)
	require.Equal(t, 99, res.Lanes[0].Issue)
	require.Equal(t, OutcomeSucceeded, res.Lanes[0].Outcome)
	// The phase row: kind=validation, the issue, parented to the DEV run.
	require.Equal(t, models.WorkflowKindValidation, recorded.Kind)
	require.Equal(t, 99, recorded.IssueNumber)
	require.Equal(t, "devflow-org1-proj1-v1", recorded.ParentWorkflowID)
}

// A failed lane runner (job-status routed by ExecutionID, forwarded to the
// lane child) fails the phase before any merge is attempted.
func TestValidationFlowWorkflow_LaneFailsOnJobFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerValidationFlowActivities(env, 99)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigJobStatus, RunStatusSignal{
			ExecutionID: "exec-e2e", Phase: PhaseFailed, Message: "chromium crashed",
		})
	}, time.Second)

	env.ExecuteWorkflow(ValidationFlowWorkflow, ValidationFlowInput{
		OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1",
		DevWorkflowID: "devflow-org1-proj1-v1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res ValidationFlowResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, OutcomeFailed, res.Outcome)
	require.Contains(t, res.Reason, "lane e2e (#99)")
	require.Contains(t, res.Reason, "chromium crashed")
	env.AssertNotCalled(t, "MergePR", mock.Anything, mock.Anything)
}

// The lanes succeed but the merge does not stick (pr-rejected after the
// auto-merge activity) → the phase fails.
func TestValidationFlowWorkflow_PRRejectedFailsMerge(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerValidationFlowActivities(env, 99)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigPROpened, PRSignal{Repo: "org1/proj1", Issue: 99, PRNumber: 55})
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigPRRejected, PRSignal{Repo: "org1/proj1", Issue: 99, PRNumber: 55})
	}, 2*time.Second)

	env.ExecuteWorkflow(ValidationFlowWorkflow, ValidationFlowInput{
		OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1",
		DevWorkflowID: "devflow-org1-proj1-v1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res ValidationFlowResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, OutcomeFailed, res.Outcome)
	require.Contains(t, res.Reason, "pull request was not merged")
}
