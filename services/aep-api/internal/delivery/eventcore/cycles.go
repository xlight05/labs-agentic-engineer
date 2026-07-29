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

package eventcore

import (
	"context"
	"log/slog"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// The cycle record learns branch, pull request and merge SHA from WEBHOOKS,
// never from dispatch: the agent derives its own branch identity (and reuses
// an unmerged one on crash resume), so the platform records what actually
// happened rather than what it asked for.
//
// Both writers are best-effort. The repository guards every mutator on the
// cycle still being open, so a redelivered webhook changes no row and returns
// (nil, nil) — and a bookkeeping failure must never fail webhook processing,
// because the merge and the build fan-out are the parts that matter.

// noteCyclePR records the pull request the agent opened on the run's open cycle.
func (e *Events) noteCyclePR(ctx context.Context, run *delivery.MilestoneRun, pr delivery.CyclePullRequest) {
	cycle := e.openCycle(ctx, run)
	if cycle == nil {
		return
	}
	pr = keepKnownURL(pr, cycle)
	// The whole identity is compared, not just the number: a pull request marked
	// ready is the SAME pull request, so skipping the write on a number match is
	// what would leave the cycle reading "draft" forever — and the same is true of
	// a link the cycle has not learned yet.
	if cycle.Branch == pr.Branch && cycle.PRNumber == pr.Number &&
		cycle.PRURL == pr.URL && cycle.PRDraft == pr.Draft {
		return
	}
	if err := e.p.Cycles.NotePullRequest(ctx, cycle.ID, pr); err != nil {
		slog.WarnContext(ctx, "eventcore: note cycle pull request failed",
			"cycle", cycle.ID, "pr", pr.Number, "error", err)
	}
}

// keepKnownURL stops a delivery that carries no `html_url` from blanking one the
// cycle already recorded. The URL is the console's ONLY link to the pull request
// — it is never composed from the repo row — so losing it to a sparse payload
// (or to a redelivery of an older one) would silently un-link a session.
func keepKnownURL(pr delivery.CyclePullRequest, cycle *delivery.RunCycle) delivery.CyclePullRequest {
	if pr.URL == "" {
		pr.URL = cycle.PRURL
	}
	return pr
}

// noteCycleMergeDecision records what the merge policy decided about the open
// cycle's pull request — the matched issue set, and the verdict when it did not
// merge.
//
// This is the cycle's only record of WHAT IT WORKED. The merge closes the
// matched issues, and the boundary read the supervisor dispatches on returns
// counts, so an unrecorded matched set is unrecoverable the moment the merge
// lands.
func (e *Events) noteCycleMergeDecision(ctx context.Context, run *delivery.MilestoneRun, decision mergeDecision) {
	cycle := e.openCycle(ctx, run)
	if cycle == nil {
		return
	}
	verdict := ""
	if !decision.Merge {
		verdict = delivery.CycleMergeDeclined
	}
	if err := e.p.Cycles.NoteMergeDecision(ctx, cycle.ID, decision.Matched, verdict, decision.Reason); err != nil {
		slog.WarnContext(ctx, "eventcore: note cycle merge decision failed",
			"cycle", cycle.ID, "verdict", verdict, "error", err)
	}
}

// noteCycleMergeRefused records the HOST's refusal on a pull request the policy
// had already approved. The matched set is left alone: the policy's decision
// still stands, and the next cycle is the one that rebases.
func (e *Events) noteCycleMergeRefused(ctx context.Context, run *delivery.MilestoneRun, reason string) {
	cycle := e.openCycle(ctx, run)
	if cycle == nil {
		return
	}
	if err := e.p.Cycles.NoteMergeDecision(ctx, cycle.ID, cycle.Resolves, delivery.CycleMergeRefused, reason); err != nil {
		slog.WarnContext(ctx, "eventcore: note cycle merge refusal failed",
			"cycle", cycle.ID, "error", err)
	}
}

// closeCycle stamps the merge onto the run's open cycle and closes it. It also
// backfills branch/PR when the pull_request.opened delivery was missed, so a
// cycle that only ever saw its merge still records what landed.
func (e *Events) closeCycle(ctx context.Context, run *delivery.MilestoneRun, pr delivery.CyclePullRequest, mergeSHA string) {
	cycle := e.openCycle(ctx, run)
	if cycle == nil {
		return
	}
	// A merged pull request is by definition not a draft, so the backfill clears
	// the flag as well as filling the identity in.
	pr.Draft = false
	pr = keepKnownURL(pr, cycle)
	if cycle.PRNumber != pr.Number || cycle.Branch != pr.Branch ||
		cycle.PRURL != pr.URL || cycle.PRDraft {
		if err := e.p.Cycles.NotePullRequest(ctx, cycle.ID, pr); err != nil {
			slog.WarnContext(ctx, "eventcore: backfill cycle pull request failed",
				"cycle", cycle.ID, "pr", pr.Number, "error", err)
		}
	}
	if err := e.p.Cycles.FinishCycle(ctx, cycle.ID, mergeSHA); err != nil {
		slog.WarnContext(ctx, "eventcore: finish cycle failed",
			"cycle", cycle.ID, "merge", delivery.ShortSHA(mergeSHA), "error", err)
	}
}

// openCycle returns the run's latest cycle when it is still open, or nil. A
// closed latest cycle means the event arrived after the supervisor moved on
// (or is a redelivery of one it already recorded), and rewriting it would
// overwrite a recorded outcome.
func (e *Events) openCycle(ctx context.Context, run *delivery.MilestoneRun) *delivery.RunCycle {
	if e.p.Cycles == nil || run == nil {
		return nil
	}
	cycle, err := e.p.Cycles.Latest(ctx, run.OrgID, run.ID)
	if err != nil {
		slog.WarnContext(ctx, "eventcore: read latest cycle failed", "run", run.ID, "error", err)
		return nil
	}
	if cycle == nil || cycle.EndedAt != nil {
		return nil
	}
	return cycle
}
