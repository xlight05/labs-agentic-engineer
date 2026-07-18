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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// The two workflows are tested in isolation with the Temporal Go SDK test
// suite: activities are mocked, so no Temporal server, DB, or aep-api service
// is needed. This is the "workflows testable separately" goal in practice.

// registerTaskActivities wires the activities the task workflow calls, mocking
// each to succeed. DispatchCoding returns a stub execution id.
func registerTaskActivities(env *testsuite.TestWorkflowEnvironment) {
	var acts *Activities
	env.RegisterActivity(acts.RecordWorkflowRun)
	env.RegisterActivity(acts.SetWorkflowRunStatus)
	env.RegisterActivity(acts.DispatchCoding)
	env.RegisterActivity(acts.MergePR)
	env.OnActivity(acts.RecordWorkflowRun, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.SetWorkflowRunStatus, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.DispatchCoding, mock.Anything, mock.Anything).Return("exec-1", nil)
	env.OnActivity(acts.MergePR, mock.Anything, mock.Anything).Return(nil)
}

func TestTaskFlowWorkflow_HappyPath_AutoGates(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerTaskActivities(env)

	// Drive the signal sequence a real webhook/watcher run would produce.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigPROpened, PRSignal{Repo: "org1/proj1", Issue: 7, PRNumber: 42})
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigPRMerged, PRSignal{Repo: "org1/proj1", Issue: 7, PRNumber: 42, MergeSHA: "abc"})
	}, 2*time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigBuildStatus, RunStatusSignal{Phase: PhaseSucceeded})
	}, 3*time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigDeployStatus, RunStatusSignal{Phase: PhaseSucceeded})
	}, 4*time.Second)

	env.ExecuteWorkflow(TaskFlowWorkflow, TaskFlowInput{
		OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Issue: 7, Tag: "v1-1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res TaskFlowResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, OutcomeSucceeded, res.Outcome)
}

func TestTaskFlowWorkflow_CodingFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerTaskActivities(env)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigJobStatus, RunStatusSignal{Phase: PhaseFailed, Message: "boom"})
	}, time.Second)

	env.ExecuteWorkflow(TaskFlowWorkflow, TaskFlowInput{
		OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Issue: 7, Tag: "v1-1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res TaskFlowResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, OutcomeFailed, res.Outcome)
	require.Contains(t, res.Error, "boom")
}

func TestTaskFlowWorkflow_PRRejected(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerTaskActivities(env)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigPROpened, PRSignal{Repo: "org1/proj1", Issue: 7, PRNumber: 42})
	}, time.Second)
	// Auto-merge activity runs, but the merge webhook reports the PR was closed
	// without merging.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigPRRejected, PRSignal{Repo: "org1/proj1", Issue: 7, PRNumber: 42})
	}, 2*time.Second)

	env.ExecuteWorkflow(TaskFlowWorkflow, TaskFlowInput{
		OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Issue: 7, Tag: "v1-1",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res TaskFlowResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, OutcomeFailed, res.Outcome)
}

func TestTaskFlowWorkflow_ManualMergeGate_Approve(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerTaskActivities(env)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigPROpened, PRSignal{Repo: "org1/proj1", Issue: 7, PRNumber: 42})
	}, time.Second)
	// Human approves the merge gate; platform then merges + the webhook confirms.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigGateDecision, GateDecisionSignal{Gate: GateMergePR, Approve: true, Actor: "alice"})
	}, 2*time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigPRMerged, PRSignal{Repo: "org1/proj1", Issue: 7, PRNumber: 42, MergeSHA: "abc"})
	}, 3*time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigBuildStatus, RunStatusSignal{Phase: PhaseSucceeded})
	}, 4*time.Second)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SigDeployStatus, RunStatusSignal{Phase: PhaseSucceeded})
	}, 5*time.Second)

	env.ExecuteWorkflow(TaskFlowWorkflow, TaskFlowInput{
		OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Issue: 7, Tag: "v1-1",
		Gates: GateConfig{Auto: map[string]bool{GateMergePR: false}},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res TaskFlowResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, OutcomeSucceeded, res.Outcome)
}

// countsLog captures every SetWorkflowRunTaskCounts payload the dev workflow
// writes, in order — the tests assert the tally is absolute and frozen right.
type countsLog struct {
	mu     sync.Mutex
	writes []SetWorkflowRunTaskCountsInput
}

func (l *countsLog) add(in SetWorkflowRunTaskCountsInput) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writes = append(l.writes, in)
}

func (l *countsLog) all() []SetWorkflowRunTaskCountsInput {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]SetWorkflowRunTaskCountsInput(nil), l.writes...)
}

// registerDevActivities mocks every dev-workflow activity. plannedTasks tunes
// the plan the fan-out schedules. The returned log records the task-count
// writes.
func registerDevActivities(env *testsuite.TestWorkflowEnvironment, plannedTasks []PlannedTask) *countsLog {
	var acts *Activities
	log := &countsLog{}
	env.RegisterActivity(acts.RecordWorkflowRun)
	env.RegisterActivity(acts.SetWorkflowRunStatus)
	env.RegisterActivity(acts.SetWorkflowRunTaskCounts)
	env.RegisterActivity(acts.ValidateSpecAtTag)
	env.RegisterActivity(acts.RunPlan)
	env.RegisterActivity(acts.ProvisionDependencies)
	env.RegisterActivity(acts.Validate)
	env.OnActivity(acts.RecordWorkflowRun, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.SetWorkflowRunStatus, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.SetWorkflowRunTaskCounts, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { log.add(args.Get(1).(SetWorkflowRunTaskCountsInput)) }).
		Return(nil)
	env.OnActivity(acts.ValidateSpecAtTag, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.RunPlan, mock.Anything, mock.Anything).Return(plannedTasks, nil)
	env.OnActivity(acts.ProvisionDependencies, mock.Anything, mock.Anything).Return([]ProvisionFailure(nil), nil)
	env.OnActivity(acts.Validate, mock.Anything, mock.Anything).Return(nil)
	return log
}

// mockValidationFlow registers + mocks the validating phase's orchestrator
// child so dev tests pin the dev workflow's handling of its RESULT; the
// orchestration itself is covered by workflow_validation_test.go.
func mockValidationFlow(env *testsuite.TestWorkflowEnvironment, res ValidationFlowResult, err error) {
	env.RegisterWorkflow(ValidationFlowWorkflow)
	env.OnWorkflow(ValidationFlowWorkflow, mock.Anything, mock.Anything).Return(res, err)
}

// validationSkipped is the orchestrator result for a project with no
// acceptance criteria — the default for dev tests not exercising validation.
func validationSkipped() ValidationFlowResult {
	return ValidationFlowResult{Outcome: ValidationOutcomeSkipped, Reason: "no acceptance criteria"}
}

func TestDevFlowWorkflow_HappyPath(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	tasks := []PlannedTask{{Issue: 1, Key: "api"}, {Issue: 2, Key: "web"}}
	registerDevActivities(env, tasks)
	// Mock the child workflows so this test stays a dev-workflow unit test.
	env.RegisterWorkflow(TaskFlowWorkflow)
	env.OnWorkflow(TaskFlowWorkflow, mock.Anything, mock.Anything).Return(TaskFlowResult{Outcome: OutcomeSucceeded}, nil)
	mockValidationFlow(env, validationSkipped(), nil)

	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res DevFlowStatus
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, DevPhaseDone, res.Phase)
	require.Equal(t, "v1", res.Tag)
	require.Len(t, res.Tasks, 2)
}

// TestDevFlowWorkflow_SpecValidationFails pins the validation-only design
// step: an unbuildable tag fails the run before any planning happens.
func TestDevFlowWorkflow_SpecValidationFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var acts *Activities
	env.RegisterActivity(acts.RecordWorkflowRun)
	env.RegisterActivity(acts.SetWorkflowRunStatus)
	env.RegisterActivity(acts.ValidateSpecAtTag)
	env.RegisterActivity(acts.RunPlan)
	env.OnActivity(acts.RecordWorkflowRun, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.SetWorkflowRunStatus, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(acts.ValidateSpecAtTag, mock.Anything, mock.Anything).
		Return(errors.New("spec validation failed: specs/design/design.md missing"))
	env.OnActivity(acts.RunPlan, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { t.Fatal("RunPlan called though the spec failed validation") }).
		Return([]PlannedTask{}, nil).Maybe()

	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res DevFlowStatus
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, DevPhaseFailed, res.Phase)
	require.Contains(t, res.Error, "validate spec at tag")
}

func TestDevFlowWorkflow_FailedDepSkipsDependent(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	// web depends on api; api fails → web is skipped, never started.
	tasks := []PlannedTask{
		{Issue: 1, Key: "api"},
		{Issue: 2, Key: "web", DependsOn: []string{"api"}},
	}
	registerDevActivities(env, tasks)
	env.RegisterWorkflow(TaskFlowWorkflow)
	// api (issue 1) fails; web (issue 2) must never be invoked.
	env.OnWorkflow(TaskFlowWorkflow, mock.Anything, mock.MatchedBy(func(in TaskFlowInput) bool { return in.Issue == 1 })).
		Return(TaskFlowResult{Outcome: OutcomeFailed}, nil)
	env.OnWorkflow(TaskFlowWorkflow, mock.Anything, mock.MatchedBy(func(in TaskFlowInput) bool { return in.Issue == 2 })).
		Run(func(mock.Arguments) { t.Fatal("dependent task started though its dependency failed") }).
		Return(TaskFlowResult{Outcome: OutcomeSucceeded}, nil).Maybe()

	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res DevFlowStatus
	require.NoError(t, env.GetWorkflowResult(&res))
	// The dev workflow still completes; the skipped dependent shows in its tasks.
	var web DevTaskRef
	for _, tr := range res.Tasks {
		if tr.Issue == 2 {
			web = tr
		}
	}
	require.Equal(t, OutcomeSkippedDepFai, web.Outcome)
}

// TestDevFlowWorkflow_TaskCountsWritten pins the build-stage tally the
// overview reads from the workflow_runs row: total is published once after
// plan (before any task runs), every subsequent write is an absolute
// snapshot (total fixed, done+failed never exceeding it), and the last write
// is the frozen terminal tally.
func TestDevFlowWorkflow_TaskCountsWritten(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	// api fails, web (independent) succeeds → terminal tally 1 done / 1 failed.
	tasks := []PlannedTask{{Issue: 1, Key: "api"}, {Issue: 2, Key: "web"}}
	counts := registerDevActivities(env, tasks)
	env.RegisterWorkflow(TaskFlowWorkflow)
	env.OnWorkflow(TaskFlowWorkflow, mock.Anything, mock.MatchedBy(func(in TaskFlowInput) bool { return in.Issue == 1 })).
		Return(TaskFlowResult{Outcome: OutcomeFailed}, nil)
	env.OnWorkflow(TaskFlowWorkflow, mock.Anything, mock.MatchedBy(func(in TaskFlowInput) bool { return in.Issue == 2 })).
		Return(TaskFlowResult{Outcome: OutcomeSucceeded}, nil)

	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	writes := counts.all()
	require.NotEmpty(t, writes)
	require.NotEmpty(t, writes[0].WorkflowID)
	require.Equal(t, SetWorkflowRunTaskCountsInput{WorkflowID: writes[0].WorkflowID, RunID: writes[0].RunID, Total: 2}, writes[0],
		"first write must be the plan size with a zero tally")
	for _, w := range writes {
		require.Equal(t, 2, w.Total, "total is fixed after plan")
		require.LessOrEqual(t, w.Done+w.Failed, 2, "tally never exceeds the plan")
	}
	last := writes[len(writes)-1]
	require.Equal(t, 1, last.Done)
	require.Equal(t, 1, last.Failed)
}

// TestDevFlowWorkflow_SkippedDepCountsAsFailed pins the tally bucket for
// dep-skipped tasks: a task never started because its dependency failed is
// counted failed, so the frozen tally always sums to total.
func TestDevFlowWorkflow_SkippedDepCountsAsFailed(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	tasks := []PlannedTask{
		{Issue: 1, Key: "api"},
		{Issue: 2, Key: "web", DependsOn: []string{"api"}},
	}
	counts := registerDevActivities(env, tasks)
	env.RegisterWorkflow(TaskFlowWorkflow)
	env.OnWorkflow(TaskFlowWorkflow, mock.Anything, mock.Anything).
		Return(TaskFlowResult{Outcome: OutcomeFailed}, nil)

	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	writes := counts.all()
	require.NotEmpty(t, writes)
	last := writes[len(writes)-1]
	require.Equal(t, 2, last.Total)
	require.Equal(t, 0, last.Done)
	require.Equal(t, 2, last.Failed, "failed child + dep-skipped dependent both count failed")
}

func TestDevFlowWorkflow_CycleFastFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	// a → b → a is an unsatisfiable cycle.
	cyclic := []PlannedTask{
		{Issue: 1, Key: "a", DependsOn: []string{"b"}},
		{Issue: 2, Key: "b", DependsOn: []string{"a"}},
	}
	registerDevActivities(env, cyclic)

	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res DevFlowStatus
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, DevPhaseFailed, res.Phase)
	require.Contains(t, res.Error, "cycle")
}

// The validating phase spawns the orchestrator child once every
// implementation task succeeded, and surfaces its outcome + lanes.
func TestDevFlowWorkflow_Validating_RunsValidationChild(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerDevActivities(env, []PlannedTask{{Issue: 1, Key: "api"}})
	env.RegisterWorkflow(TaskFlowWorkflow)
	// Implementation task (issue 1) succeeds.
	env.OnWorkflow(TaskFlowWorkflow, mock.Anything, mock.MatchedBy(func(in TaskFlowInput) bool { return in.Issue == 1 })).
		Return(TaskFlowResult{Issue: 1, Outcome: OutcomeSucceeded}, nil)
	// The orchestrator child gets the dev run's identity and reports one
	// succeeded e2e lane.
	env.RegisterWorkflow(ValidationFlowWorkflow)
	env.OnWorkflow(ValidationFlowWorkflow, mock.Anything, mock.MatchedBy(func(in ValidationFlowInput) bool {
		return in.Repo == "org1/proj1" && in.Tag == "v1" && in.DevWorkflowID != ""
	})).Return(ValidationFlowResult{
		Outcome: OutcomeSucceeded, PRNumber: 55,
		Lanes: []ValidationLaneResult{{Kind: LaneE2E, Issue: 99, Outcome: OutcomeSucceeded}},
	}, nil)

	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res DevFlowStatus
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, DevPhaseDone, res.Phase)
	require.NotNil(t, res.Validation)
	require.Equal(t, "validationflow-org1-proj1-v1", res.Validation.WorkflowID)
	require.Equal(t, OutcomeSucceeded, res.Validation.Outcome)
	require.Len(t, res.Validation.Lanes, 1)
	require.Equal(t, 99, res.Validation.Lanes[0].Issue)
}

// The quality bar fails the run BEFORE the validate gate when a task did not
// succeed: the gate is manual + never approved, so a fail-fast that ran before
// the gate is the only way this completes (it never blocks on the gate) — and
// the orchestrator child is never spawned.
func TestDevFlowWorkflow_Validating_FailsBeforeGateWhenTaskFailed(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerDevActivities(env, []PlannedTask{{Issue: 1, Key: "api"}})
	env.RegisterWorkflow(TaskFlowWorkflow)
	env.OnWorkflow(TaskFlowWorkflow, mock.Anything, mock.Anything).Return(TaskFlowResult{Issue: 1, Outcome: OutcomeFailed}, nil)
	// No orchestrator registered: spawning it would error the run differently
	// than the asserted quality-bar failure.

	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{
		OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1",
		Gates: GateConfig{Auto: map[string]bool{GateValidate: false}},
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res DevFlowStatus
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, DevPhaseFailed, res.Phase)
	require.Contains(t, res.Error, "did not succeed")
}

// No acceptance criteria → the orchestrator reports a skip; the dev run
// records the note and completes.
func TestDevFlowWorkflow_Validating_SkipsWhenNoCriteria(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerDevActivities(env, []PlannedTask{{Issue: 1, Key: "api"}})
	env.RegisterWorkflow(TaskFlowWorkflow)
	env.OnWorkflow(TaskFlowWorkflow, mock.Anything, mock.Anything).Return(TaskFlowResult{Issue: 1, Outcome: OutcomeSucceeded}, nil)
	mockValidationFlow(env, validationSkipped(), nil)

	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res DevFlowStatus
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, DevPhaseDone, res.Phase)
	require.NotNil(t, res.Validation)
	require.Contains(t, res.Validation.Outcome, "skipped")
}

// A failed validation phase (lane or merge failure, reported as data) fails
// the run with the orchestrator's reason.
func TestDevFlowWorkflow_Validating_FailsOnChildFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerDevActivities(env, []PlannedTask{{Issue: 1, Key: "api"}})
	env.RegisterWorkflow(TaskFlowWorkflow)
	env.OnWorkflow(TaskFlowWorkflow, mock.Anything, mock.Anything).Return(TaskFlowResult{Issue: 1, Outcome: OutcomeSucceeded}, nil)
	mockValidationFlow(env, ValidationFlowResult{
		Outcome: OutcomeFailed, Reason: "lane e2e (#99): coding job failed",
	}, nil)

	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res DevFlowStatus
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, DevPhaseFailed, res.Phase)
	require.Contains(t, res.Error, "validation run did not succeed")
	require.Contains(t, res.Error, "lane e2e (#99)")
}

// A mechanical orchestrator failure (crash/timeout) fails the run.
func TestDevFlowWorkflow_Validating_FailsOnChildError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerDevActivities(env, []PlannedTask{{Issue: 1, Key: "api"}})
	env.RegisterWorkflow(TaskFlowWorkflow)
	env.OnWorkflow(TaskFlowWorkflow, mock.Anything, mock.Anything).Return(TaskFlowResult{Issue: 1, Outcome: OutcomeSucceeded}, nil)
	mockValidationFlow(env, ValidationFlowResult{}, errors.New("boom"))

	env.ExecuteWorkflow(DevFlowWorkflow, DevFlowInput{OrgID: "org1", ProjectID: "proj1", Repo: "org1/proj1", Tag: "v1"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var res DevFlowStatus
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, DevPhaseFailed, res.Phase)
	require.Contains(t, res.Error, "validation run failed")
}
