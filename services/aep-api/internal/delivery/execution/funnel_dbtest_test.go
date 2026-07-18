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
	"sync"
	"testing"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/platform/dbtest"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/models"
	"github.com/wso2/aep/aep-api/repositories"
)

// TestFunnel_AdmissionUnderConcurrency_DB drives the §5 admission invariant end
// to end through the FUNNEL over a real Postgres: two entrants (webhook ∥ sweep)
// racing the SAME aep:execute intent must yield EXACTLY ONE coding Execution row
// — the partial unique index (repo, issue_number, kind) WHERE status IN
// (queued,running) is the authoritative mutex, and TryAdmit's losing racer
// stops. This is the funnel-level proof over the repository-level admission test.
func TestFunnel_AdmissionUnderConcurrency_DB(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t) // self-skips under -short / no Docker
	repo := repositories.NewExecutionRepository(db)

	// One open, executable coding Task with no deps.
	issues := newFakeIssues([]sourcecontrol.IssueInfo{taskIssue(2, "order-service", nil, []string{taskmeta.LabelExecute}, "open")})
	exec := &fakeExecutor{} // records only — leaves the admitted row queued
	reg := NewRegistry()
	reg.Register(taskmeta.ClassCoding, exec)
	funnel := NewFunnel(repo, issues, fakeRepos{orgID: "org1", projectID: "proj1"},
		fakeDesign{names: map[string]bool{"order-service": true}}, reg)

	const racers = 8
	var wg sync.WaitGroup
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func() {
			defer wg.Done()
			_ = funnel.OnExecuteIntent(context.Background(), "o/r", 2)
		}()
	}
	wg.Wait()

	rows, err := repo.ListByIssue(context.Background(), "o/r", 2)
	if err != nil {
		t.Fatalf("ListByIssue: %v", err)
	}
	coding := 0
	for _, r := range rows {
		if r.Kind == string(taskmeta.KindCoding) {
			coding++
		}
	}
	if coding != 1 {
		t.Fatalf("admission mutex breached: %d coding execution rows for one racing intent, want exactly 1", coding)
	}
	// And the executor was invoked exactly once (only the admission winner
	// reaches dispatch).
	if len(exec.got) != 1 {
		t.Fatalf("expected exactly one dispatch, got %d", len(exec.got))
	}
	// The losing racers exit cleanly: the aep:execute label is consumed
	// (best-effort projection) regardless of who won, and it is consumed exactly
	// once per entrant without ever double-dispatching (proven by the row count).
	if got := issues.removed[2]; len(got) == 0 {
		t.Errorf("aep:execute must be consumed on the admitted issue")
	}
}

// TestFunnel_ReAdmittableAfterFinish_DB proves the admission mutex is scoped to
// ACTIVE rows only (§5, §7 "retry = a new row"): while an Execution is
// queued/running a second intent is a no-op, but once it Finishes the next
// intent admits a FRESH row over the real partial unique index.
func TestFunnel_ReAdmittableAfterFinish_DB(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := context.Background()
	repo := repositories.NewExecutionRepository(db)

	issues := newFakeIssues([]sourcecontrol.IssueInfo{taskIssue(2, "order-service", nil, []string{taskmeta.LabelExecute}, "open")})
	exec := &fakeExecutor{} // records only — the admitted row stays queued (active)
	reg := NewRegistry()
	reg.Register(taskmeta.ClassCoding, exec)
	funnel := NewFunnel(repo, issues, fakeRepos{orgID: "org1", projectID: "proj1"},
		fakeDesign{names: map[string]bool{"order-service": true}}, reg)

	// First intent admits + dispatches one coding row.
	if err := funnel.OnExecuteIntent(ctx, "o/r", 2); err != nil {
		t.Fatalf("first intent: %v", err)
	}
	// A second intent while that row is active is a no-op (the mutex holds).
	if err := funnel.OnExecuteIntent(ctx, "o/r", 2); err != nil {
		t.Fatalf("second intent: %v", err)
	}
	if rows, _ := repo.ListByIssue(ctx, "o/r", 2); len(rows) != 1 {
		t.Fatalf("an active row must block re-admit, got %d rows", len(rows))
	}
	if len(exec.got) != 1 {
		t.Fatalf("second intent must not dispatch, got %d dispatches", len(exec.got))
	}

	// Finish the attempt (failed) — the mutex releases.
	rows, _ := repo.ListByIssue(ctx, "o/r", 2)
	if _, err := repo.Finish(ctx, rows[0].ID, string(taskmeta.ExecFailed), "boom"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	// Retry: a fresh intent admits a NEW row (never mutates the terminal one).
	if err := funnel.OnExecuteIntent(ctx, "o/r", 2); err != nil {
		t.Fatalf("retry intent: %v", err)
	}
	rows, _ = repo.ListByIssue(ctx, "o/r", 2)
	if len(rows) != 2 {
		t.Fatalf("a retry after Finish must admit a new row, got %d", len(rows))
	}
	active := 0
	for _, r := range rows {
		if taskmeta.ExecutionStatus(r.Status).IsActive() {
			active++
		}
	}
	if active != 1 {
		t.Errorf("exactly one active row after retry, got %d", active)
	}
	if len(exec.got) != 2 {
		t.Errorf("retry must reach dispatch, got %d total dispatches", len(exec.got))
	}
}

// TestFunnel_ReevaluateAdmitsWhenDepsSatisfied_DB proves the queued-row release
// path (§5): an intent whose dependency is not yet deployed is admitted but held
// queued (no dispatch); once the dependency's build succeeds, Reevaluate re-gates
// the queued row and dispatches it — the path build-success and the sweep drive.
func TestFunnel_ReevaluateAdmitsWhenDepsSatisfied_DB(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := context.Background()
	repo := repositories.NewExecutionRepository(db)

	issues := newFakeIssues([]sourcecontrol.IssueInfo{
		taskIssue(1, "user-service", nil, nil, "open"),
		taskIssue(2, "order-service", []string{"user-service"}, []string{taskmeta.LabelExecute}, "open"),
	})
	exec := &fakeExecutor{}
	reg := NewRegistry()
	reg.Register(taskmeta.ClassCoding, exec)
	funnel := NewFunnel(repo, issues, fakeRepos{orgID: "org1", projectID: "proj1"},
		fakeDesign{names: map[string]bool{"user-service": true, "order-service": true}}, reg)

	// Dep unmet → admitted queued, NOT dispatched.
	if err := funnel.OnExecuteIntent(ctx, "o/r", 2); err != nil {
		t.Fatalf("intent (dep unmet): %v", err)
	}
	if len(exec.got) != 0 {
		t.Fatalf("an unmet dependency must hold the row queued (no dispatch), got %d", len(exec.got))
	}
	rows, _ := repo.ListByIssue(ctx, "o/r", 2)
	if len(rows) != 1 || rows[0].Status != string(taskmeta.ExecQueued) {
		t.Fatalf("expected one queued row, got %+v", rows)
	}

	// Deploy the dependency: a succeeded build on user-service (#1) derives deployed.
	_, dep, err := repo.TryAdmit(ctx, &models.Execution{Repo: "o/r", IssueNumber: 1, Kind: string(taskmeta.KindBuild)})
	if err != nil {
		t.Fatalf("seed dep build: %v", err)
	}
	if _, err := repo.Finish(ctx, dep.ID, string(taskmeta.ExecSucceeded), ""); err != nil {
		t.Fatalf("finish dep build: %v", err)
	}

	// Reevaluate → deps now satisfied → the queued row dispatches.
	if err := funnel.Reevaluate(ctx); err != nil {
		t.Fatalf("reevaluate: %v", err)
	}
	if len(exec.got) != 1 {
		t.Fatalf("reevaluate must dispatch once the dependency deploys, got %d", len(exec.got))
	}
	if exec.got[0].Execution.IssueNumber != 2 {
		t.Errorf("dispatched the wrong task: %+v", exec.got[0].Execution)
	}
}
