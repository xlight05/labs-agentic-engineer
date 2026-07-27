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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// The run loop is tested end-to-end with the Temporal Go SDK test suite: every
// activity is mocked, so there is no Temporal server, no database, no GitHub
// and no cluster. What is exercised is the only thing this package owns — the
// DECISIONS: when to dispatch, which budget a failure spends, and which of §7's
// exits a run takes.
//
// Time is free here. The environment fast-forwards its clock whenever the
// workflow is blocked on nothing but timers, so an unbounded wait, a two-hour
// dispatch deadline and a ten-minute re-poll all cost the same as an assertion.

const (
	testOrg      = "org1"
	testProject  = "proj1"
	testRunID    = "run-1"
	testCycleID  = "cycle-1"
	testMilepost = 7
	testMergeSHA = "abc123def4567890"
	testPRNumber = 42
)

// harness records what the loop DID — the sequence of dispatches, how each
// cycle was closed, and the run's final write — so a test can assert on
// behaviour rather than on mock call counts.
type harness struct {
	env  *testsuite.TestWorkflowEnvironment
	acts *Activities

	// set records which facts a test pinned. Defaults are applied at run() time
	// rather than in the constructor because testify consumes expectations in
	// REGISTRATION order: an unlimited default registered first would swallow
	// every call and silently mask the test's own sequence.
	set map[string]bool

	mu         sync.Mutex
	dispatches []delivery.MilestoneDispatch
	finishes   []FinishCycleInput
	states     []string
	settle     SettleRunInput
	verdicts   []string
	closed     int
}

// newHarness registers the activities whose behaviour never varies — the
// writers the loop records its progress through. The facts a run turns on are
// pinned per test, or defaulted at run() time.
func newHarness(t *testing.T) *harness {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	h := &harness{env: ts.NewTestWorkflowEnvironment(), set: map[string]bool{}}
	var acts *Activities
	h.acts = acts

	h.env.RegisterActivity(acts.PollMilestone)
	h.env.RegisterActivity(acts.SetRunState)
	h.env.RegisterActivity(acts.SettleRun)
	h.env.RegisterActivity(acts.BumpRunBudget)
	h.env.RegisterActivity(acts.SetValidationVerdict)
	h.env.RegisterActivity(acts.AppendCycle)
	h.env.RegisterActivity(acts.NoteCycleDispatch)
	h.env.RegisterActivity(acts.FinishCycle)
	h.env.RegisterActivity(acts.ReadCycleFacts)
	h.env.RegisterActivity(acts.CloseMilestone)
	h.env.RegisterActivity(acts.PollCycleBuilds)
	h.env.RegisterActivity(acts.EnsureValidationIssue)
	h.env.RegisterActivity(acts.ReadValidationVerdict)
	h.env.RegisterActivity(acts.DispatchAgent)

	h.env.OnActivity(acts.SetRunState, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			in := args.Get(1).(SetRunStateInput)
			h.mu.Lock()
			defer h.mu.Unlock()
			h.states = append(h.states, in.State)
		}).Return(nil)
	h.env.OnActivity(acts.SettleRun, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.settle = args.Get(1).(SettleRunInput)
		}).Return(nil)
	h.env.OnActivity(acts.BumpRunBudget, mock.Anything, mock.Anything).Return(nil)
	h.env.OnActivity(acts.SetValidationVerdict, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			in := args.Get(1).(SetValidationVerdictInput)
			h.mu.Lock()
			defer h.mu.Unlock()
			h.verdicts = append(h.verdicts, in.Verdict)
		}).Return(nil)
	h.env.OnActivity(acts.AppendCycle, mock.Anything, mock.Anything).Return(testCycleID, nil)
	h.env.OnActivity(acts.NoteCycleDispatch, mock.Anything, mock.Anything).Return(nil)
	h.env.OnActivity(acts.FinishCycle, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.finishes = append(h.finishes, args.Get(1).(FinishCycleInput))
		}).Return(nil)
	h.env.OnActivity(acts.CloseMilestone, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.closed++
		}).Return(nil)
	h.env.OnActivity(acts.DispatchAgent, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.dispatches = append(h.dispatches, args.Get(1).(delivery.MilestoneDispatch))
		}).Return("job-1", nil)

	return h
}

// milestoneIs queues the cycle-boundary polls, in order. The last one repeats
// for as long as the run keeps asking.
func (h *harness) milestoneIs(snaps ...MilestoneSnapshot) {
	h.set["milestone"] = true
	for i, s := range snaps {
		call := h.env.OnActivity(h.acts.PollMilestone, mock.Anything, mock.Anything).Return(s, nil)
		if i < len(snaps)-1 {
			call.Once()
		}
	}
}

// buildsAre queues the cycle-build polls, in order; the last repeats.
func (h *harness) buildsAre(states ...CycleBuildState) {
	h.set["builds"] = true
	for i, s := range states {
		call := h.env.OnActivity(h.acts.PollCycleBuilds, mock.Anything, mock.Anything).Return(s, nil)
		if i < len(states)-1 {
			call.Once()
		}
	}
}

// mergesAt makes the cycle record report a merge — the GROUND TRUTH the loop
// consults instead of believing the signal that woke it. An empty SHA means the
// agent never landed anything.
func (h *harness) mergesAt(sha string) {
	h.set["facts"] = true
	facts := CycleFacts{CycleID: testCycleID}
	if sha != "" {
		facts.MergeSHA, facts.PRNumber, facts.Ended = sha, testPRNumber, true
	}
	h.env.OnActivity(h.acts.ReadCycleFacts, mock.Anything, mock.Anything).Return(facts, nil)
}

// validationIs pins the acceptance oracle's issue number (0 = no criteria) and
// the verdict the runner's report yields.
func (h *harness) validationIs(issue int, verdict string) {
	h.set["validation"] = true
	h.env.OnActivity(h.acts.EnsureValidationIssue, mock.Anything, mock.Anything).Return(issue, nil)
	h.env.OnActivity(h.acts.ReadValidationVerdict, mock.Anything, mock.Anything).Return(verdict, nil)
}

// applyDefaults fills in the facts a test did not pin: every cycle lands, every
// build is green, and the project has no acceptance oracle.
func (h *harness) applyDefaults() {
	if !h.set["facts"] {
		h.mergesAt(testMergeSHA)
	}
	if !h.set["builds"] {
		h.buildsAre(CycleBuildState{Expected: 1, Settled: 1})
	}
	if !h.set["validation"] {
		h.validationIs(0, delivery.ValidationVerdictSkipped)
	}
	if !h.set["milestone"] {
		h.milestoneIs(MilestoneSnapshot{})
	}
}

// signal schedules one inbound run signal at a virtual offset from start.
func (h *harness) signal(name string, after time.Duration) {
	h.env.RegisterDelayedCallback(func() {
		h.env.SignalWorkflow(name, delivery.RunSignal{Signal: name, MilestoneNumber: testMilepost})
	}, after)
}

// merges schedules n merge signals, one per cycle, a second apart.
func (h *harness) merges(n int) {
	for i := 1; i <= n; i++ {
		h.signal(delivery.SigRunPRMerged, time.Duration(i)*time.Second)
	}
}

func (h *harness) run(origin string, ceiling int) {
	h.applyDefaults()
	h.env.ExecuteWorkflow(MilestoneRunWorkflow, RunInput{
		RunID:           testRunID,
		OrgID:           testOrg,
		ProjectID:       testProject,
		MilestoneNumber: testMilepost,
		MilestoneTitle:  "v3",
		Origin:          origin,
		CycleCeiling:    ceiling,
	})
}

func (h *harness) result(t *testing.T) RunResult {
	t.Helper()
	require.True(t, h.env.IsWorkflowCompleted())
	require.NoError(t, h.env.GetWorkflowError())
	var res RunResult
	require.NoError(t, h.env.GetWorkflowResult(&res))
	return res
}

func (h *harness) dispatchKinds() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.dispatches))
	for _, d := range h.dispatches {
		out = append(out, d.Kind)
	}
	return out
}

func (h *harness) dispatchCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.dispatches)
}

// closedCount is the milestone-close tally, read safely. Tests that assert
// after the workflow completed read h.closed directly; a test that asserts
// MID-RUN, from a delayed callback, races the activity goroutine and must not.
func (h *harness) closedCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

// settledState is the run row's terminal state so far, read safely, for the
// same mid-run reason. Empty means nothing has settled the run.
func (h *harness) settledState() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.settle.State
}

// assertSettled checks the run's terminal write and the workflow result agree —
// the row is what the console reads, the result is what Temporal records, and a
// run that disagreed with itself would be unexplainable.
func (h *harness) assertSettled(t *testing.T, res RunResult, state, reason string) {
	t.Helper()
	require.Equal(t, state, res.State, "workflow result state")
	require.Equal(t, reason, res.TerminalReason, "workflow result reason")
	h.mu.Lock()
	defer h.mu.Unlock()
	require.Equal(t, testRunID, h.settle.RunID)
	require.Equal(t, state, h.settle.State, "run row state")
	require.Equal(t, reason, h.settle.Reason, "run row reason")
}

// ---- the §7 exits ----------------------------------------------------------

// TestHappyPath_OneCycleDeliversTheVersion is the loop's whole point: work the
// milestone, land the pull request, watch the builds go green, find nothing
// left, close the milestone.
func TestHappyPath_OneCycleDeliversTheVersion(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{Work: 2, Total: 2}, MilestoneSnapshot{})
	h.merges(1)

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateSucceeded, "")
	require.Equal(t, []string{delivery.CycleKindCoding}, h.dispatchKinds())
	require.Equal(t, 1, res.Cycles)
	require.Equal(t, 1, h.closed, "a settled version closes its milestone")
	require.Equal(t, []FinishCycleInput{{CycleID: testCycleID, MergeSHA: testMergeSHA}}, h.finishes)
	// The run row never says "waiting" here: it parks only when something
	// actually holds it, and a boundary the loop passes straight through is not
	// a wait a human could act on.
	require.Equal(t, []string{delivery.RunStateRunning}, dedupeStates(h.states))
	// No acceptance oracle: skip-if-no-criteria is a verdict, not a silence.
	require.Equal(t, delivery.ValidationVerdictSkipped, res.ValidationVerdict)
}

// TestFixCycle_RedBuildBecomesTheNextCyclesWork proves recovery is
// indistinguishable from normal work: the red build's fix issue joins the
// working set and the next cycle picks it up.
func TestFixCycle_RedBuildBecomesTheNextCyclesWork(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(
		MilestoneSnapshot{Work: 2, Total: 2}, // dispatch
		MilestoneSnapshot{Work: 1, Total: 1}, // the fix issue eventcore minted
		MilestoneSnapshot{},                  // delivered
	)
	h.buildsAre(
		CycleBuildState{Expected: 1, Settled: 1, Red: []string{"order-service"}},
		CycleBuildState{Expected: 1, Settled: 1},
	)
	h.merges(2)

	h.run(delivery.RunOriginIncidentAdoption, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateSucceeded, "")
	require.Equal(t, []string{delivery.CycleKindCoding, delivery.CycleKindFix}, h.dispatchKinds())
}

// TestConflictCycle_AnUnmergeablePRBecomesTheNextCyclesWork is the same shape
// for the other recovery class — and proves the conflicted cycle is closed with
// NO merge SHA, because nothing landed.
func TestConflictCycle_AnUnmergeablePRBecomesTheNextCyclesWork(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(
		MilestoneSnapshot{Work: 2, Total: 2},
		MilestoneSnapshot{Work: 1, Total: 1}, // the conflict issue
		MilestoneSnapshot{},
	)
	h.signal(delivery.SigRunConflict, time.Second)
	h.signal(delivery.SigRunPRMerged, 2*time.Second)

	h.run(delivery.RunOriginIncidentAdoption, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateSucceeded, "")
	require.Equal(t, []string{delivery.CycleKindCoding, delivery.CycleKindConflict}, h.dispatchKinds())
	require.Equal(t, "", h.finishes[0].MergeSHA, "a conflicted cycle landed nothing")
	require.Equal(t, testMergeSHA, h.finishes[1].MergeSHA)
}

// TestValidationCycle_Passes covers §7's validation arm: at deployed-green with
// an empty working set, a SPEC run mints the validation issue and works it with
// a fresh dispatch anchored to that issue alone.
func TestValidationCycle_Passes(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(
		MilestoneSnapshot{Work: 1, Total: 1},
		MilestoneSnapshot{}, // deployed-green, nothing left → validation
		MilestoneSnapshot{}, // after validation → settle
	)
	h.validationIs(77, delivery.ValidationVerdictPassed)
	h.merges(2)

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateSucceeded, "")
	require.Equal(t, []string{delivery.CycleKindCoding, delivery.CycleKindValidation}, h.dispatchKinds())
	require.Equal(t, delivery.ValidationVerdictPassed, res.ValidationVerdict)
	h.mu.Lock()
	defer h.mu.Unlock()
	require.Equal(t, []string{delivery.ValidationVerdictPassed}, h.verdicts,
		"the verdict is written once, as a run property")
	require.Equal(t, 77, h.dispatches[1].IssueNumber, "the validation dispatch is anchored to one issue")
	require.Equal(t, 0, h.dispatches[0].IssueNumber, "a coding dispatch is a milestone reference only")
}

// TestValidationCycle_Fails proves a failed verdict is a failed RUN with its own
// named reason — the milestone stays open, because the way forward is more work
// in the same version.
func TestValidationCycle_Fails(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{Work: 1, Total: 1}, MilestoneSnapshot{})
	h.validationIs(77, delivery.ValidationVerdictFailed)
	h.merges(2)

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateFailed, delivery.RunReasonValidationFailed)
	require.Equal(t, delivery.ValidationVerdictFailed, res.ValidationVerdict)
	require.Equal(t, 0, h.closed, "a failed increment keeps its milestone open")
}

// TestIncidentRun_GetsNoValidationCycle pins the origin split: an incident fixes
// one thing in an already-validated version, and re-validating the whole system
// for it would price every incident like a release.
func TestIncidentRun_GetsNoValidationCycle(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{Work: 1, Total: 1}, MilestoneSnapshot{})
	h.merges(1)

	h.run(delivery.RunOriginIncidentAdoption, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateSucceeded, "")
	require.Equal(t, []string{delivery.CycleKindCoding}, h.dispatchKinds())
	h.env.AssertNotCalled(t, "EnsureValidationIssue", mock.Anything, mock.Anything)
}

// TestRedispatchBudget_AgentDeathEndsTheRun: the dispatch never lands a pull
// request, so the cycle spends its whole per-cycle allowance and the run fails
// naming that budget. Nothing here needs a real two hours — the environment
// fast-forwards both deadlines.
func TestRedispatchBudget_AgentDeathEndsTheRun(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{Work: 1, Total: 1})
	h.mergesAt("") // the cycle record never learns a merge

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateFailed, delivery.RunReasonRedispatchBudget)
	require.Equal(t, delivery.RunMaxRedispatchPerCycle, h.dispatchCount())
	require.Equal(t, "", h.finishes[0].MergeSHA)
}

// TestBuildRetriggerBudget_RedWithNothingToFix is the exit for a build that
// stayed red through its one automatic re-trigger and produced no fix issue:
// the allowance is spent and nothing came back that could make it green.
func TestBuildRetriggerBudget_RedWithNothingToFix(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{Work: 1, Total: 1}, MilestoneSnapshot{})
	h.buildsAre(CycleBuildState{Expected: 1, Settled: 1, Red: []string{"order-service"}})
	h.merges(1)

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateFailed, delivery.RunReasonBuildRetriggerBudget)
	require.Equal(t, 1, h.dispatchCount())
}

// TestFixChainBudget_TwoFixCyclesIsTheLimit walks the fix chain to exhaustion.
func TestFixChainBudget_TwoFixCyclesIsTheLimit(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{Work: 1, Total: 1})
	h.buildsAre(CycleBuildState{Expected: 1, Settled: 1, Red: []string{"order-service"}})
	h.merges(3)

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateFailed, delivery.RunReasonFixChainBudget)
	require.Equal(t, []string{
		delivery.CycleKindCoding, delivery.CycleKindFix, delivery.CycleKindFix,
	}, h.dispatchKinds())
}

// TestConflictBudget_TwoConflictCyclesIsTheLimit does the same for the other
// chain, and is what keeps the two reasons apart: a run that could not merge is
// never reported as a run that could not build.
func TestConflictBudget_TwoConflictCyclesIsTheLimit(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{Work: 1, Total: 1})
	for i := 1; i <= 3; i++ {
		h.signal(delivery.SigRunConflict, time.Duration(i)*time.Second)
	}

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateFailed, delivery.RunReasonConflictBudget)
	require.Equal(t, []string{
		delivery.CycleKindCoding, delivery.CycleKindConflict, delivery.CycleKindConflict,
	}, h.dispatchKinds())
}

// TestNoProgress_AGreenCycleThatChangedNothing: the agent merged, everything
// built, and the milestone is exactly as it was. Another cycle would be the
// same dispatch against the same working set.
func TestNoProgress_AGreenCycleThatChangedNothing(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{Work: 2, Total: 2})
	h.merges(1)

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateFailed, delivery.RunReasonNoProgress)
	require.Equal(t, 1, h.dispatchCount())
}

// TestCycleCeiling_StopsARunThatIsStillMakingProgress proves the ceiling is a
// backstop over and above the per-class budgets: every cycle here closes an
// issue, so no other budget would ever fire.
func TestCycleCeiling_StopsARunThatIsStillMakingProgress(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(
		MilestoneSnapshot{Work: 3, Total: 3},
		MilestoneSnapshot{Work: 2, Total: 2},
		MilestoneSnapshot{Work: 1, Total: 1},
	)
	h.merges(2)

	h.run(delivery.RunOriginSpecBuild, 2)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateFailed, delivery.RunReasonCycleCeiling)
	require.Equal(t, 2, h.dispatchCount())
}

// TestCancel_FromWaiting: cancel is the ONLY expiry the unbounded wait has.
// The run is parked behind a gate and never dispatches.
func TestCancel_FromWaiting(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{Work: 1, Gates: 1, Total: 2})
	h.signal(delivery.SigRunCancel, time.Second)

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateCancelled, "")
	require.Equal(t, 0, h.dispatchCount(), "a cancelled wait never dispatched")
	require.Equal(t, 0, h.closed, "an abandoned increment keeps its milestone open")
}

// TestCancel_FromRunning: cancel mid-cycle settles the run and closes the cycle
// with no merge, so the timeline shows a dispatch that was abandoned rather than
// one still in flight.
func TestCancel_FromRunning(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{Work: 1, Total: 1})
	h.signal(delivery.SigRunCancel, time.Second)

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateCancelled, "")
	require.Equal(t, 1, h.dispatchCount())
	require.Equal(t, []FinishCycleInput{{CycleID: testCycleID}}, h.finishes)
}

// TestMidRunGate_HoldsTheNextDispatch is the human brake: a gate filed while the
// run is live stops the NEXT cycle, and only the next cycle. The assertion that
// matters is inside the callback — at the moment the gate was open, nothing new
// had been dispatched.
func TestMidRunGate_HoldsTheNextDispatch(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(
		MilestoneSnapshot{Work: 2, Total: 2},           // cycle 1 dispatches
		MilestoneSnapshot{Work: 1, Gates: 1, Total: 2}, // a gate appears → hold
		MilestoneSnapshot{Work: 1, Total: 1},           // the gate closed → cycle 2
		MilestoneSnapshot{},                            // delivered
	)
	h.merges(1)

	heldAt := -1
	h.env.RegisterDelayedCallback(func() {
		heldAt = h.dispatchCount()
		h.env.SignalWorkflow(delivery.SigRunWorkable, delivery.RunSignal{Signal: delivery.SigRunWorkable})
	}, 2*time.Second)
	h.signal(delivery.SigRunPRMerged, 3*time.Second)

	h.run(delivery.RunOriginIncidentAdoption, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateSucceeded, "")
	require.Equal(t, 1, heldAt, "the gate must hold the second dispatch")
	require.Equal(t, 2, h.dispatchCount(), "the closed gate releases it")
}

// TestSettle_WithAStrayGateStillOpen: gates hold DISPATCH. With an empty working
// set there is nothing to dispatch, so an open gate holds nothing and the
// version still settles.
func TestSettle_WithAStrayGateStillOpen(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(
		MilestoneSnapshot{Work: 1, Total: 1},
		MilestoneSnapshot{Work: 0, Gates: 1, Total: 1},
	)
	h.merges(1)

	h.run(delivery.RunOriginIncidentAdoption, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateSucceeded, "")
	require.Equal(t, 1, h.closed)
}

// ---- ground truth and liveness --------------------------------------------

// TestMergeSignalIsNotEvidence pins the rule that a signal is a wake-up, never
// evidence: a merge signal whose cycle record shows no merge (a HUMAN's pull
// request landing during the cycle raises the very same signal) must not end
// the agent's cycle.
func TestMergeSignalIsNotEvidence(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{Work: 1, Total: 1})
	h.mergesAt("") // ground truth: this cycle landed nothing
	h.merges(3)    // three merge signals arrive anyway

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	// The run ends on the re-dispatch budget, not on a phantom green cycle.
	h.assertSettled(t, res, delivery.RunStateFailed, delivery.RunReasonRedispatchBudget)
}

// TestBuildTerminalSignalWakesTheBuildWait exercises the build phase's wait: the
// first poll finds a component still building, the signal wakes the loop, and
// the re-poll settles it.
func TestBuildTerminalSignalWakesTheBuildWait(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{Work: 1, Total: 1}, MilestoneSnapshot{})
	h.buildsAre(
		CycleBuildState{Expected: 2, Settled: 1},
		CycleBuildState{Expected: 2, Settled: 2},
	)
	h.merges(1)
	h.signal(delivery.SigRunBuildTerminal, 2*time.Second)

	h.run(delivery.RunOriginIncidentAdoption, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateSucceeded, "")
}

// TestQueryRunStatus proves the loop reports its POSITION live — the thing no
// database column holds, because fix and conflict cycles re-enter earlier phases
// and a stored phase enum would lie mid-loop.
func TestQueryRunStatus(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{Work: 2, Total: 2}, MilestoneSnapshot{})

	var midRun delivery.RunStatus
	h.env.RegisterDelayedCallback(func() {
		resp, err := h.env.QueryWorkflow(delivery.QueryRunStatus)
		require.NoError(t, err)
		require.NoError(t, resp.Get(&midRun))
		h.env.SignalWorkflow(delivery.SigRunPRMerged, delivery.RunSignal{Signal: delivery.SigRunPRMerged})
	}, time.Second)

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	require.Equal(t, delivery.RunStateRunning, midRun.State)
	require.Equal(t, delivery.RunPhaseCoding, midRun.Phase)
	require.Equal(t, delivery.CycleKindCoding, midRun.CycleKind)
	require.Equal(t, 1, midRun.CycleAttempt)
	require.Equal(t, 1, midRun.CyclesTotal)
	require.Equal(t, delivery.RunDefaultCycleCeiling, midRun.CycleCeiling)
	require.Equal(t, testMilepost, midRun.MilestoneNumber)

	var settled delivery.RunStatus
	resp, err := h.env.QueryWorkflow(delivery.QueryRunStatus)
	require.NoError(t, err)
	require.NoError(t, resp.Get(&settled))
	require.Equal(t, delivery.RunStateSucceeded, settled.State)
	require.Equal(t, delivery.RunPhaseSettling, settled.Phase)
	require.Equal(t, delivery.RunStateSucceeded, res.State)
}

// TestZeroCycleRun_WaitsForWorkInsteadOfSettling is the regression for a run
// that closed its version having never dispatched anything.
//
// The plan path admits the run row BEFORE its planning turn, so the supervisor
// can legitimately poll a milestone whose issues have not been minted yet. An
// empty working set at that moment means "not planned yet", not "delivered" —
// the run must park in §7's unbounded wait, and the work that arrives
// afterwards must still be worked.
func TestZeroCycleRun_WaitsForWorkInsteadOfSettling(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(
		MilestoneSnapshot{},                  // the planning turn has not minted its issues yet
		MilestoneSnapshot{Work: 1, Total: 1}, // they land
		MilestoneSnapshot{},                  // and the cycle delivers them
	)

	var waiting delivery.RunStatus
	var closedWhileWaiting, dispatchedWhileWaiting int
	var settledWhileWaiting string
	h.env.RegisterDelayedCallback(func() {
		resp, err := h.env.QueryWorkflow(delivery.QueryRunStatus)
		require.NoError(t, err)
		require.NoError(t, resp.Get(&waiting))
		closedWhileWaiting, dispatchedWhileWaiting = h.closedCount(), h.dispatchCount()
		settledWhileWaiting = h.settledState()
		h.env.SignalWorkflow(delivery.SigRunWorkable, delivery.RunSignal{Signal: delivery.SigRunWorkable})
	}, time.Second)
	h.signal(delivery.SigRunPRMerged, 2*time.Second)

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	require.Equal(t, delivery.RunStateWaiting, waiting.State,
		"a run that has never dispatched must park on an empty working set, not settle")
	require.Equal(t, delivery.RunPhaseWaiting, waiting.Phase)
	require.Equal(t, "", settledWhileWaiting, "nothing may write the run's outcome while it waits")
	require.Equal(t, 0, closedWhileWaiting, "the version's milestone must still be open")
	require.Equal(t, 0, dispatchedWhileWaiting)

	// The work that arrived later is picked up by the very next boundary.
	h.assertSettled(t, res, delivery.RunStateSucceeded, "")
	require.Equal(t, []string{delivery.CycleKindCoding}, h.dispatchKinds())
	require.Equal(t, 1, res.Cycles)
	require.Equal(t, 1, h.closed, "the milestone closes only once the run delivered something")
}

// TestZeroCycleRun_WaitsUntilCancelled is the other half of the same rule: the
// wait is UNBOUNDED. A milestone that never receives work is not a delivered
// version, however many poll backstops pass — only a human cancelling ends it,
// and a cancelled increment keeps its milestone open.
func TestZeroCycleRun_WaitsUntilCancelled(t *testing.T) {
	h := newHarness(t)
	h.milestoneIs(MilestoneSnapshot{})
	h.signal(delivery.SigRunCancel, time.Hour) // several poll backstops later

	h.run(delivery.RunOriginSpecBuild, 0)
	res := h.result(t)

	h.assertSettled(t, res, delivery.RunStateCancelled, "")
	require.Equal(t, 0, h.dispatchCount())
	require.Equal(t, 0, h.closed, "a run that delivered nothing must not close the version")
}

// dedupeStates collapses repeated run-state writes so a test can assert the
// oscillation rather than the write count.
func dedupeStates(states []string) []string {
	var out []string
	for _, s := range states {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}
