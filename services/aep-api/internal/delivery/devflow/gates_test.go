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
	"go.temporal.io/sdk/testsuite"
)

func TestGateConfig_IsAuto(t *testing.T) {
	// Nil config → everything auto (the default).
	require.True(t, GateConfig{}.IsAuto(GatePlan))
	// A gate absent from the map is auto; present=false is manual.
	c := GateConfig{Auto: map[string]bool{GateValidate: false, GatePlan: true}}
	require.False(t, c.IsAuto(GateValidate))
	require.True(t, c.IsAuto(GatePlan))
	require.True(t, c.IsAuto(GateComplete))
}

func TestDevFlowWorkflow_ManualPlanGate_Reject(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerDevActivities(env, []PlannedTask{{Issue: 1, Key: "api"}})

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigGateDecision, GateDecisionSignal{Gate: GatePlan, Approve: false, Note: "not yet"})
	}, time.Second)

	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{
		OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1",
		Gates: GateConfig{Auto: map[string]bool{GatePlan: false}},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res DevFlowStatus
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, DevPhaseFailed, res.Phase)
	require.Contains(t, res.Error, "plan gate rejected")
}

func TestDevFlowWorkflow_ManualGate_ApprovalTimeout(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerDevActivities(env, []PlannedTask{{Issue: 1, Key: "api"}})

	// plan gate is manual with a 60s approval timeout; send no decision so the
	// timeout fires (the test env auto-advances time while idle) → rejection.
	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{
		OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1",
		Gates: GateConfig{Auto: map[string]bool{GatePlan: false}, ApprovalTimeoutSeconds: 60},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res DevFlowStatus
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, DevPhaseFailed, res.Phase)
	require.Contains(t, res.Error, "plan gate rejected")
}

func TestDevFlowWorkflow_PendingGate_VisibleInQuery(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerDevActivities(env, []PlannedTask{{Issue: 1, Key: "api"}})
	env.RegisterWorkflow(TaskFlowWorkflow)
	env.OnWorkflow(TaskFlowWorkflow, mock.Anything, mock.Anything).Return(TaskFlowResult{Outcome: OutcomeSucceeded}, nil)
	mockValidationFlow(env, validationSkipped(), nil)

	// While paused at the manual plan gate, the status query must surface it.
	env.RegisterDelayedCallback(func() {
		res, err := env.QueryWorkflow(QueryStatus)
		require.NoError(t, err)
		var st DevFlowStatus
		require.NoError(t, res.Get(&st))
		require.Equal(t, GatePlan, st.PendingGate)
		// Release the gate so the workflow completes.
		env.SignalWorkflow(SigGateDecision, GateDecisionSignal{Gate: GatePlan, Approve: true})
	}, time.Second)

	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{
		OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1",
		Gates: GateConfig{Auto: map[string]bool{GatePlan: false}},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res DevFlowStatus
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, DevPhaseDone, res.Phase)
	require.Empty(t, res.PendingGate) // cleared after the gate resolved
}

func TestTaskFlowWorkflow_ManualMergeGate_Reject(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerTaskActivities(env)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigPROpened, PRSignal{Repo: "org1/proj1", Issue: 7, PRNumber: 42})
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigGateDecision, GateDecisionSignal{Gate: GateMergePR, Approve: false, Note: "hold off"})
	}, 2*time.Second)

	env.ExecuteWorkflow(TaskFlowWorkflow, TaskFlowInput{
		OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Issue: 7, Tag: "v1-1",
		Gates: GateConfig{Auto: map[string]bool{GateMergePR: false}},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res TaskFlowResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, OutcomeFailed, res.Outcome)
}
