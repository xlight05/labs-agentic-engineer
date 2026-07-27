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

// noteCyclePR records the branch and pull request on the run's open cycle.
func (e *Events) noteCyclePR(ctx context.Context, run *delivery.MilestoneRun, branch string, prNumber int) {
	cycle := e.openCycle(ctx, run)
	if cycle == nil || (cycle.Branch == branch && cycle.PRNumber == prNumber) {
		return
	}
	if err := e.p.Cycles.NotePullRequest(ctx, cycle.ID, branch, prNumber); err != nil {
		slog.WarnContext(ctx, "eventcore: note cycle pull request failed",
			"cycle", cycle.ID, "pr", prNumber, "error", err)
	}
}

// closeCycle stamps the merge onto the run's open cycle and closes it. It also
// backfills branch/PR when the pull_request.opened delivery was missed, so a
// cycle that only ever saw its merge still records what landed.
func (e *Events) closeCycle(ctx context.Context, run *delivery.MilestoneRun, branch string, prNumber int, mergeSHA string) {
	cycle := e.openCycle(ctx, run)
	if cycle == nil {
		return
	}
	if cycle.PRNumber != prNumber || cycle.Branch != branch {
		if err := e.p.Cycles.NotePullRequest(ctx, cycle.ID, branch, prNumber); err != nil {
			slog.WarnContext(ctx, "eventcore: backfill cycle pull request failed",
				"cycle", cycle.ID, "pr", prNumber, "error", err)
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
