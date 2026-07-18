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

package execution

import (
	"context"
	"log/slog"
	"time"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/models"
)

// Sweep is the reconciliation sweep (§5 — a required component, not an
// afterthought): a periodic loop that re-gates every queued Execution (picking
// up dependencies that just deployed and holds that were lifted), and re-scans
// open Task issues for un-consumed aep:execute command labels (missed webhook
// deliveries). It is also the disaster-recovery path — everything platform-side
// is rebuildable from GitHub + the executions rows. Cycles introduced by a hand
// edit are detected here (via the funnel gate) rather than sitting queued
// forever, indistinguishable from waiting.
type Sweep struct {
	funnel   *Funnel
	events   *Events // PR-state healer (may be nil)
	store    ExecutionStore
	repos    RepoLister
	issues   IssueClient
	interval time.Duration
}

// NewSweep wires the sweep. repos may be nil (then the sweep only re-gates
// tracked executions and does not scan for missed command labels). events may
// be nil (then the PR-state healer is skipped). store is the executions rows —
// the sweep is the aep:attention clearing authority (see clearStaleAttention).
// interval defaults to 60s when non-positive (§13 cadence default).
func NewSweep(funnel *Funnel, events *Events, store ExecutionStore, repos RepoLister, issues IssueClient, interval time.Duration) *Sweep {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &Sweep{funnel: funnel, events: events, store: store, repos: repos, issues: issues, interval: interval}
}

// Run drives the sweep on its interval until ctx is canceled. Matches the
// watcher lifecycle convention (a plain goroutine per watcher; state lives in
// Postgres + GitHub).
func (s *Sweep) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Sweep(ctx); err != nil {
				slog.WarnContext(ctx, "reconciliation sweep failed", "error", err)
			}
		}
	}
}

// clearStaleAttention is the sweep's aep:attention CLEARING authority (§4/§5).
// It removes the flag when the Task's execution state is HEALTHY — every latest-
// per-kind execution is non-failed and non-canceled (a deployed Task whose EARLIER
// attempt failed/was canceled carries no live failure, so the chip that attempt
// raised is stale). This retro-heals a deployed-but-flagged Task within one cycle.
//
// It cannot permanently hide a real problem: the flag is re-raised on the next
// cycle if a genuine condition still holds — a failed/canceled dispatch re-flags
// via the funnel gate, and a mangled block re-flags on the next issues.edited. To
// avoid a clear-then-re-flag churn on GitHub, a Task whose machine block is
// currently invalid is LEFT flagged here (that IS a live attention reason).
//
// Guards: acts only when aep:attention is actually present (no needless GitHub
// call, and it never touches aep:hold or the command labels). Best-effort — a
// removal failure is logged, never propagated. The removal fires an
// issues.unlabeled delivery whose sender is the platform bot; the issues.* handler
// drops self-sender deliveries (echo suppression, §9.2), so there is no loop.
// execs is the Task's latest-per-kind rows, batch-loaded once per repo by Sweep.
func (s *Sweep) clearStaleAttention(ctx context.Context, orgID, projectID, repoFullName string, issue sourcecontrol.IssueInfo, execs map[string]*models.Execution) {
	labels := taskmeta.ParseLabels(issue.Labels)
	if !labels.IsTask || !labels.Attention {
		return // not a flagged Task — nothing to clear (no GitHub call)
	}
	if _, err := taskmeta.ParseBlock(issue.Body); err != nil {
		return // a mangled/absent block IS a live attention reason — leave it
	}
	if !executionsHealthy(execs) {
		return // a live failure/cancellation remains — keep the flag
	}
	if err := s.issues.RemoveLabel(ctx, orgID, projectID, issue.Number, taskmeta.LabelAttention); err != nil {
		slog.WarnContext(ctx, "sweep: clear stale aep:attention failed", "repo", repoFullName, "issue", issue.Number, "error", err)
	}
}

// executionsHealthy reports whether every latest-per-kind execution is in a
// non-failure terminal/active state (not failed, not canceled) — the healthy
// signal the attention-clear keys on. Canceled covers the funnel's
// missing-class / component-not-in-design / no-executor (ops) cancellations, so
// those Tasks stay flagged.
func executionsHealthy(execs map[string]*models.Execution) bool {
	for _, e := range execs {
		if e == nil {
			continue
		}
		switch taskmeta.ExecutionStatus(e.Status) {
		case taskmeta.ExecFailed, taskmeta.ExecCanceled:
			return false
		}
	}
	return true
}

// Sweep runs one reconciliation pass. Exported so a test (or an admin trigger)
// can drive a single pass without the ticker.
func (s *Sweep) Sweep(ctx context.Context) error {
	// 1. Re-gate every queued Execution: deps that deployed, holds that lifted,
	//    cycles introduced by hand edits (the gate flags attention).
	if err := s.funnel.Reevaluate(ctx); err != nil {
		return err
	}
	// 2. Pick up un-consumed aep:execute labels (missed webhook deliveries).
	if s.repos == nil {
		return nil
	}
	repos, err := s.repos.ListAll(ctx)
	if err != nil {
		slog.WarnContext(ctx, "sweep: list repos failed", "error", err)
		return nil
	}
	for _, repo := range repos {
		// One marker-labeled listing serves both the execute pickup and the PR
		// reconcile pass (IssueInfo.Labels already carries aep:execute).
		tasks, err := s.issues.ListIssues(ctx, repo.OrgID, repo.ProjectID, []string{taskmeta.LabelMarker})
		if err != nil {
			slog.WarnContext(ctx, "sweep: list task issues failed", "repo", repo.FullName, "error", err)
			continue
		}
		for _, issue := range tasks {
			if !taskmeta.ParseLabels(issue.Labels).Execute {
				continue
			}
			if err := s.funnel.OnExecuteIntent(ctx, repo.FullName, issue.Number); err != nil {
				slog.WarnContext(ctx, "sweep: pick up execute intent failed", "repo", repo.FullName, "issue", issue.Number, "error", err)
			}
		}

		// 3. PR-state reconciliation (§5): heal a missed close/merge webhook for
		//    every Task claiming an open PR — INCLUDING closed issues. A merged PR
		//    auto-closes its linked issue, but §4's merged chain completes
		//    regardless of issue close (close stops NEW dispatches; abandoned =
		//    closed WITHOUT merge). The latest-per-kind rows are batch-loaded
		//    once for the whole repo (one query per tick, not one per Task);
		//    ReconcileTaskPR's cheap local check (openPRNumber + newer-build
		//    skip) still runs BEFORE any GitHub fetch, so a settled/closed Task
		//    costs no API call.
		if s.events == nil || s.store == nil {
			continue
		}
		latest, err := s.store.LatestPerKindForRepo(ctx, repo.FullName)
		if err != nil {
			slog.WarnContext(ctx, "sweep: batch-load executions failed", "repo", repo.FullName, "error", err)
			continue
		}
		for _, issue := range tasks {
			// Clear a stale aep:attention flag once the Task's execution state
			// is healthy (a red chip must not outlive the failed attempt that
			// raised it — the Phase-4 deployed-but-flagged defect).
			s.clearStaleAttention(ctx, repo.OrgID, repo.ProjectID, repo.FullName, issue, latest[issue.Number])
			if err := s.events.ReconcileTaskPR(ctx, repo.OrgID, repo.ProjectID, repo.FullName, issue.Number, latest[issue.Number]); err != nil {
				slog.WarnContext(ctx, "sweep: PR reconcile failed", "repo", repo.FullName, "issue", issue.Number, "error", err)
			}
		}
	}
	return nil
}
