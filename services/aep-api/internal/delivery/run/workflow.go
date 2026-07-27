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
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// Cadences. Only two of them are load-bearing.
const (
	// activityTimeout bounds one activity call. Every activity is a single
	// GitHub, OpenChoreo or database round trip.
	activityTimeout = 2 * time.Minute

	// waitPollInterval re-reads the milestone while the run is WAITING. The wait
	// itself stays unbounded — this timer never ends it, it only re-derives the
	// predicate, so a lost `issues.closed` delivery costs ten minutes of latency
	// instead of stranding the run behind a gate that is already resolved.
	waitPollInterval = 10 * time.Minute

	// buildPollInterval re-reads the cycle's builds. Same role: the build
	// terminals arrive as signals, and this is what makes a lost one survivable.
	buildPollInterval = time.Minute

	// cycleLandingTimeout is how long ONE dispatch has to land a merged pull
	// request before the supervisor calls it agent death and spends a
	// re-dispatch. It is the only deadline in the loop, and it exists because
	// "the agent died" (including a Job that exited without opening a pull
	// request) is a named failure class with a named budget.
	cycleLandingTimeout = 2 * time.Hour
)

// RunInput starts a supervisor over one milestone. Everything in it is already
// decided by the caller: the run row exists, the milestone exists, and the
// ceiling is snapshotted so a config change cannot retroactively fail a live
// run.
type RunInput struct {
	RunID           string `json:"runId"`
	OrgID           string `json:"orgId"`
	ProjectID       string `json:"projectId"`
	MilestoneNumber int    `json:"milestoneNumber"`
	MilestoneTitle  string `json:"milestoneTitle"`
	Origin          string `json:"origin"`
	CycleCeiling    int    `json:"cycleCeiling,omitempty"`
}

// RunResult is the run's outcome, mirroring what was written to the run row.
type RunResult struct {
	State             string `json:"state"`
	TerminalReason    string `json:"terminalReason,omitempty"`
	ValidationVerdict string `json:"validationVerdict,omitempty"`
	Cycles            int    `json:"cycles"`
}

// MilestoneRunWorkflow is the run supervisor: work the open issues in one
// milestone until the milestone settles.
//
// It never returns an error for a run that reached a decision — a failed run is
// a SUCCEEDED workflow carrying a terminal reason, because "the increment could
// not be delivered" is an outcome the platform records, not a crash. A returned
// error means the supervisor itself could not function.
func MilestoneRunWorkflow(ctx workflow.Context, in RunInput) (RunResult, error) {
	l := newLoop(ctx, in)
	if err := workflow.SetQueryHandler(ctx, delivery.QueryRunStatus, func() (delivery.RunStatus, error) {
		return l.st, nil
	}); err != nil {
		return RunResult{}, err
	}
	return l.run(ctx)
}

// loop is the run's whole in-workflow state. It is the authority on the
// budgets: they are counted here, deterministically, and written outwards to
// the run row for the read model — never read back, because a replay must
// reproduce the same decisions without a database.
type loop struct {
	in RunInput
	st delivery.RunStatus

	cancel   workflow.ReceiveChannel
	workable workflow.ReceiveChannel
	merged   workflow.ReceiveChannel
	builds   workflow.ReceiveChannel
	conflict workflow.ReceiveChannel

	// lastResult is what the previous cycle produced — it selects the next
	// cycle's kind and feeds the no-progress rule.
	lastResult cycleResult
	// workBefore is the working-set size when the previous cycle was dispatched.
	workBefore int

	// prNumber / mergeSHA are the current cycle's landing, read from the cycle
	// record rather than from the signal that announced it.
	prNumber int
	mergeSHA string

	validationDone bool
}

func newLoop(ctx workflow.Context, in RunInput) *loop {
	ceiling := in.CycleCeiling
	if ceiling <= 0 {
		ceiling = delivery.RunDefaultCycleCeiling
	}
	return &loop{
		in:       in,
		cancel:   workflow.GetSignalChannel(ctx, delivery.SigRunCancel),
		workable: workflow.GetSignalChannel(ctx, delivery.SigRunWorkable),
		merged:   workflow.GetSignalChannel(ctx, delivery.SigRunPRMerged),
		builds:   workflow.GetSignalChannel(ctx, delivery.SigRunBuildTerminal),
		conflict: workflow.GetSignalChannel(ctx, delivery.SigRunConflict),
		st: delivery.RunStatus{
			RunID:           in.RunID,
			MilestoneNumber: in.MilestoneNumber,
			Origin:          in.Origin,
			State:           delivery.RunStateWaiting,
			Phase:           delivery.RunPhaseWaiting,
			CycleCeiling:    ceiling,
		},
	}
}

// run is the cycle-boundary loop. Every pass begins by polling GROUND TRUTH —
// the milestone itself — and every decision below is made from that poll and
// the workflow's own counters. No branch here trusts a signal's payload.
func (l *loop) run(ctx workflow.Context) (RunResult, error) {
	for {
		if l.cancelRequested() {
			return l.settle(ctx, delivery.RunStateCancelled, "")
		}

		snap, err := l.pollMilestone(ctx)
		if err != nil {
			return l.result(), err
		}

		// SETTLE comes first, before the gate check: a stray gate holds dispatch,
		// and with an empty working set there is nothing to dispatch, so it holds
		// nothing.
		if snap.Work == 0 {
			settled, res, err := l.onEmptyWorkingSet(ctx)
			if settled || err != nil {
				return res, err
			}
			continue
		}

		if noProgress(l.lastResult, l.workBefore, snap.Work) {
			return l.settle(ctx, delivery.RunStateFailed, delivery.RunReasonNoProgress)
		}

		if !Dispatchable(snap) {
			// An open gate is a deliberate human brake. Park in the unbounded wait
			// — cancel is its only expiry — and re-derive when anything happens.
			cancelled, perr := l.park(ctx)
			if perr != nil {
				return l.result(), perr
			}
			if cancelled {
				return l.settle(ctx, delivery.RunStateCancelled, "")
			}
			continue
		}

		kind := nextCycleKind(l.lastResult)
		if reason := budgetRefusal(kind, l.st.CyclesTotal, l.st.FixCycles, l.st.ConflictCycles, l.st.CycleCeiling); reason != "" {
			return l.settle(ctx, delivery.RunStateFailed, reason)
		}

		l.workBefore = snap.Work
		res, err := l.runCycle(ctx, kind, 0)
		if err != nil {
			return l.result(), err
		}
		switch res {
		case cycleCancelled:
			return l.settle(ctx, delivery.RunStateCancelled, "")
		case cycleAgentDead:
			return l.settle(ctx, delivery.RunStateFailed, delivery.RunReasonRedispatchBudget)
		}
		l.lastResult = res
	}
}

// onEmptyWorkingSet decides what an exhausted milestone means. Four different
// things, in this order:
//
//  1. Nothing has EVER been dispatched — there is no increment to call
//     delivered, so the run waits rather than settling. See below.
//  2. The last cycle ended badly and NOTHING came back to recover it. The
//     recovery issue the event plane should have minted is not there, so the
//     run cannot proceed and fails naming the budget that ran out.
//  3. A spec run at deployed-green with no validation yet — mint the validation
//     issue and work it with a fresh dispatch of the same loop.
//  4. Otherwise the version is delivered.
//
// Case 1 is the one that must NOT settle. "Empty working set" means delivered
// only in contrast to work this run actually did; with zero cycles behind it
// the same reading is indistinguishable from a milestone whose issues have not
// been minted yet — the plan path admits the run row BEFORE its planning turn
// (so the spec mutex is armed across it), so a poll can legitimately land in
// that window and see nothing. Settling there closes a version nobody built.
// §7's wait is unbounded and cancel is its only expiry, so the run parks and
// re-derives on every `issues` webhook and at the poll backstop.
//
// What ends such a run, then: work arriving (it dispatches), a human cancelling
// (§7's only expiry), or — when the planning turn itself failed and no issue is
// ever coming — the PLAN PATH settling the row it armed with
// RunReasonPlanFailed. Those two cannot race: the plan path starts the
// supervisor only after planning returns, so a run that failed to plan has no
// workflow behind it, and a workflow that exists is past planning. The
// repository's non-terminal guard on Settle is the backstop if that ordering
// ever changes — the first settle wins, and this loop never issues one here.
//
// It returns settled=false for the two cases that continue: the zero-cycle wait
// above, and a validation cycle that passed — after which the boundary is
// re-entered so anything adopted while validation ran is picked up.
func (l *loop) onEmptyWorkingSet(ctx workflow.Context) (settled bool, res RunResult, err error) {
	if l.st.CyclesTotal == 0 {
		cancelled, perr := l.park(ctx)
		if perr != nil {
			return true, l.result(), perr
		}
		if cancelled {
			res, err = l.settle(ctx, delivery.RunStateCancelled, "")
			return true, res, err
		}
		return false, RunResult{}, nil
	}
	switch l.lastResult {
	case cycleRed:
		// The build is red, its one automatic re-trigger is spent, and no fix
		// issue joined the milestone. There is nothing left that could make it
		// green.
		res, err = l.settle(ctx, delivery.RunStateFailed, delivery.RunReasonBuildRetriggerBudget)
		return true, res, err
	case cycleConflict:
		// Same shape: the pull request would not merge and no conflict issue
		// arrived to rebase it.
		res, err = l.settle(ctx, delivery.RunStateFailed, delivery.RunReasonConflictBudget)
		return true, res, err
	}

	// Validation is a SPEC-run property: an incident run fixes one thing in an
	// already-validated version, and re-validating the whole system for it would
	// price every incident like a release.
	if l.in.Origin != delivery.RunOriginSpecBuild || l.validationDone {
		res, err = l.settle(ctx, delivery.RunStateSucceeded, "")
		return true, res, err
	}
	return l.runValidation(ctx)
}

// runValidation mints the validation issue at deployed-green and works it with
// a fresh dispatch of the same loop.
//
// Minting HERE, and not at plan time, is what makes the coverage honest:
// mid-run adoption postpones deployed-green by construction, so by the time
// this runs the validation issue covers everything the run landed.
func (l *loop) runValidation(ctx workflow.Context) (settled bool, res RunResult, err error) {
	issue, err := l.ensureValidationIssue(ctx)
	if err != nil {
		return true, l.result(), err
	}
	l.validationDone = true
	if issue == 0 {
		// No acceptance oracle — nothing to validate, which is itself a verdict.
		if err := l.setVerdict(ctx, delivery.ValidationVerdictSkipped); err != nil {
			return true, l.result(), err
		}
		res, err = l.settle(ctx, delivery.RunStateSucceeded, "")
		return true, res, err
	}
	l.st.ValidationIssue = issue

	if reason := budgetRefusal(delivery.CycleKindValidation, l.st.CyclesTotal, l.st.FixCycles, l.st.ConflictCycles, l.st.CycleCeiling); reason != "" {
		res, err = l.settle(ctx, delivery.RunStateFailed, reason)
		return true, res, err
	}
	outcome, err := l.runCycle(ctx, delivery.CycleKindValidation, issue)
	if err != nil {
		return true, l.result(), err
	}
	switch outcome {
	case cycleCancelled:
		res, err = l.settle(ctx, delivery.RunStateCancelled, "")
		return true, res, err
	case cycleAgentDead:
		res, err = l.settle(ctx, delivery.RunStateFailed, delivery.RunReasonRedispatchBudget)
		return true, res, err
	}

	verdict, err := l.readVerdict(ctx)
	if err != nil {
		return true, l.result(), err
	}
	if err := l.setVerdict(ctx, verdict); err != nil {
		return true, l.result(), err
	}
	if verdict == delivery.ValidationVerdictFailed {
		res, err = l.settle(ctx, delivery.RunStateFailed, delivery.RunReasonValidationFailed)
		return true, res, err
	}
	// A validation cycle closes no working-set issue and mints none, so the
	// no-progress rule must not see it — re-enter the boundary clean, which also
	// picks up anything adopted while validation was running.
	l.lastResult = cycleNone
	l.workBefore = 0
	return false, RunResult{}, nil
}

// settle ends the run. The milestone close is display only and happens on
// success alone: a failed or cancelled increment stays open, because the way
// forward from it is more work in the same version.
func (l *loop) settle(ctx workflow.Context, state, reason string) (RunResult, error) {
	l.st.Phase = delivery.RunPhaseSettling
	if state == delivery.RunStateSucceeded {
		if l.st.ValidationVerdict == "" {
			// "The run finished and did not validate" is an honest verdict; an
			// empty one would read as "not yet".
			if err := l.setVerdict(ctx, delivery.ValidationVerdictSkipped); err != nil {
				return l.result(), err
			}
		}
		if err := l.closeMilestone(ctx); err != nil {
			return l.result(), err
		}
	}
	if err := l.settleRun(ctx, state, reason); err != nil {
		return l.result(), err
	}
	l.st.State = state
	l.st.TerminalReason = reason
	l.st.CycleKind = ""
	l.st.CycleAttempt = 0
	return l.result(), nil
}

func (l *loop) result() RunResult {
	return RunResult{
		State:             l.st.State,
		TerminalReason:    l.st.TerminalReason,
		ValidationVerdict: l.st.ValidationVerdict,
		Cycles:            l.st.CyclesTotal,
	}
}

// park puts the run into the WAITING state — row and live status together —
// and blocks there until something worth re-deriving happens. It returns true
// only for cancel.
//
// Both of the loop's holds go through it, because they are the same state seen
// from two sides: a gate holding the next dispatch, and a milestone that has
// not produced any work yet. Neither is a run that is finished.
func (l *loop) park(ctx workflow.Context) (cancelled bool, err error) {
	if serr := l.setState(ctx, delivery.RunStateWaiting); serr != nil {
		return false, serr
	}
	l.st.Phase = delivery.RunPhaseWaiting
	l.st.CycleKind = ""
	return l.await(ctx), nil
}

// await parks the run in the unbounded wait. It returns true only for cancel —
// every other wake-up is a reason to re-derive the predicate from ground truth,
// which is why the signals are drained without being read.
func (l *loop) await(ctx workflow.Context) (cancelled bool) {
	timerCtx, stop := workflow.WithCancel(ctx)
	defer stop()

	sel := workflow.NewSelector(ctx)
	sel.AddReceive(l.cancel, func(c workflow.ReceiveChannel, _ bool) {
		c.Receive(ctx, nil)
		cancelled = true
	})
	for _, ch := range []workflow.ReceiveChannel{l.workable, l.merged, l.builds, l.conflict} {
		sel.AddReceive(ch, func(c workflow.ReceiveChannel, _ bool) { c.Receive(ctx, nil) })
	}
	sel.AddFuture(workflow.NewTimer(timerCtx, waitPollInterval), func(workflow.Future) {})
	sel.Select(ctx)
	return cancelled
}

// cancelRequested drains a pending cancel without blocking. Checked at every
// boundary so a cancel that arrived mid-cycle is honoured at the first safe
// point rather than after another dispatch.
func (l *loop) cancelRequested() bool { return l.cancel.ReceiveAsync(nil) }

// ---- activity calls --------------------------------------------------------

// activityCtx is the options every activity but the dispatch runs under.
//
// The retry policy is Temporal's default — unbounded, with backoff. That is
// deliberate: none of these activities has a "give up" answer that would be
// better than waiting. A supervisor that cannot reach GitHub should stall
// visibly, not settle a run on a network blip.
func activityCtx(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: activityTimeout,
	})
}

func (l *loop) pollMilestone(ctx workflow.Context) (MilestoneSnapshot, error) {
	var out MilestoneSnapshot
	err := workflow.ExecuteActivity(activityCtx(ctx), (*Activities).PollMilestone, l.milestoneRef()).Get(ctx, &out)
	return out, err
}

func (l *loop) milestoneRef() MilestoneRef {
	return MilestoneRef{OrgID: l.in.OrgID, ProjectID: l.in.ProjectID, MilestoneNumber: l.in.MilestoneNumber}
}

// setState moves the run row AND the live status together, so a query and the
// database can never disagree about whether the run is parked or working.
func (l *loop) setState(ctx workflow.Context, state string) error {
	if err := workflow.ExecuteActivity(activityCtx(ctx), (*Activities).SetRunState,
		SetRunStateInput{RunID: l.in.RunID, State: state}).Get(ctx, nil); err != nil {
		return err
	}
	l.st.State = state
	return nil
}

func (l *loop) settleRun(ctx workflow.Context, state, reason string) error {
	return workflow.ExecuteActivity(activityCtx(ctx), (*Activities).SettleRun,
		SettleRunInput{RunID: l.in.RunID, State: state, Reason: reason}).Get(ctx, nil)
}

func (l *loop) bump(ctx workflow.Context, counter delivery.RunBudget) error {
	return workflow.ExecuteActivity(activityCtx(ctx), (*Activities).BumpRunBudget,
		BumpRunBudgetInput{RunID: l.in.RunID, Counter: string(counter)}).Get(ctx, nil)
}

func (l *loop) setVerdict(ctx workflow.Context, verdict string) error {
	if err := workflow.ExecuteActivity(activityCtx(ctx), (*Activities).SetValidationVerdict,
		SetValidationVerdictInput{RunID: l.in.RunID, Verdict: verdict}).Get(ctx, nil); err != nil {
		return err
	}
	l.st.ValidationVerdict = verdict
	return nil
}

func (l *loop) closeMilestone(ctx workflow.Context) error {
	return workflow.ExecuteActivity(activityCtx(ctx), (*Activities).CloseMilestone, l.milestoneRef()).Get(ctx, nil)
}

func (l *loop) ensureValidationIssue(ctx workflow.Context) (int, error) {
	var issue int
	err := workflow.ExecuteActivity(activityCtx(ctx), (*Activities).EnsureValidationIssue, l.milestoneRef()).Get(ctx, &issue)
	return issue, err
}

func (l *loop) readVerdict(ctx workflow.Context) (string, error) {
	var verdict string
	err := workflow.ExecuteActivity(activityCtx(ctx), (*Activities).ReadValidationVerdict,
		ProjectRef{OrgID: l.in.OrgID, ProjectID: l.in.ProjectID}).Get(ctx, &verdict)
	return verdict, err
}

// dispatchActivityCtx runs the agent launch with retries OFF. A launch that did
// not happen is agent death, and the cycle's own re-dispatch budget is the
// answer to that — a Temporal retry on top would spend it invisibly.
func dispatchActivityCtx(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: activityTimeout,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
}
