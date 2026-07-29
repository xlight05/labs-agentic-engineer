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

package delivery_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/platform/dbtest"
)

// appendCycle inserts one dispatched cycle under a run and returns it.
func appendCycle(t *testing.T, cycles delivery.RunCycleRepository, run *delivery.MilestoneRun, kind, jobRef string) *delivery.RunCycle {
	t.Helper()
	ctx := context.Background()
	row := &delivery.RunCycle{OrgID: run.OrgID, ProjectID: run.ProjectID, RunID: run.ID, Kind: kind}
	if err := cycles.Append(ctx, row); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if jobRef != "" {
		if _, err := cycles.NoteDispatch(ctx, row.ID, jobRef); err != nil {
			t.Fatalf("NoteDispatch: %v", err)
		}
		row.JobRef = jobRef
	}
	return row
}

// TestRunCycleLogRepository_RoundTrip: the snapshot is keyed by the CYCLE, which
// is the whole point — a milestone run mints no execution row, so the
// execution-keyed coding_agent_logs has nothing to hang a cycle's log off.
func TestRunCycleLogRepository_RoundTrip(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	runs := delivery.NewMilestoneRunRepository(db)
	cycles := delivery.NewRunCycleRepository(db, nil)
	logs := delivery.NewRunCycleLogRepository(db)
	ctx := context.Background()

	run := admitRun(t, runs, "orga", "proj", 4, "v4")
	cycle := appendCycle(t, cycles, run, delivery.CycleKindCoding, "ca-abc-2607010900")
	cycleUUID := uuid.MustParse(cycle.ID)

	// A miss is (nil, nil) — the pre-capture live-tail window, not an error.
	got, err := logs.GetByRun(ctx, cycleUUID, cycle.JobRef)
	if err != nil || got != nil {
		t.Fatalf("pre-capture GetByRun = (%v, %v), want (nil, nil)", got, err)
	}

	if err := logs.Create(ctx, &delivery.RunCycleLog{
		CycleID: cycleUUID, RunName: cycle.JobRef, FinalPhase: "Succeeded",
		LogText: "line one\nline two", SizeBytes: 17,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err = logs.GetByRun(ctx, cycleUUID, cycle.JobRef)
	if err != nil || got == nil {
		t.Fatalf("GetByRun after capture = (%v, %v)", got, err)
	}
	if got.FinalPhase != "Succeeded" || got.LogText != "line one\nline two" || got.SizeBytes != 17 {
		t.Errorf("captured log round-tripped wrong: %+v", got)
	}
	if got.CapturedAt.IsZero() {
		t.Error("captured_at must default to now()")
	}

	// A re-dispatch of the SAME cycle keeps its own log: the key is
	// (cycle, run name), so a second Job under one cycle does not overwrite.
	if err := logs.Create(ctx, &delivery.RunCycleLog{
		CycleID: cycleUUID, RunName: "ca-abc-2607010905", FinalPhase: "Failed",
		LogText: "retry", SizeBytes: 5,
	}); err != nil {
		t.Fatalf("Create second attempt: %v", err)
	}
	second, err := logs.GetByRun(ctx, cycleUUID, "ca-abc-2607010905")
	if err != nil || second == nil || second.FinalPhase != "Failed" {
		t.Fatalf("second attempt's log = (%v, %v)", second, err)
	}
	first, _ := logs.GetByRun(ctx, cycleUUID, cycle.JobRef)
	if first == nil || first.FinalPhase != "Succeeded" {
		t.Errorf("the first attempt's log must survive the second, got %v", first)
	}
}

// TestRunCycleLogRepository_CascadesWithTheProject: deleting a project's cycles
// takes their logs, so a recreated same-named project starts with a clean
// timeline rather than another run's pod output.
func TestRunCycleLogRepository_CascadesWithTheProject(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	runs := delivery.NewMilestoneRunRepository(db)
	cycles := delivery.NewRunCycleRepository(db, nil)
	logs := delivery.NewRunCycleLogRepository(db)
	ctx := context.Background()

	run := admitRun(t, runs, "orgb", "proj", 4, "v4")
	cycle := appendCycle(t, cycles, run, delivery.CycleKindCoding, "ca-del")
	cycleUUID := uuid.MustParse(cycle.ID)
	if err := logs.Create(ctx, &delivery.RunCycleLog{
		CycleID: cycleUUID, RunName: cycle.JobRef, FinalPhase: "Succeeded", LogText: "x", SizeBytes: 1,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := cycles.DeleteByProject(ctx, "orgb", "proj"); err != nil {
		t.Fatalf("DeleteByProject: %v", err)
	}
	got, err := logs.GetByRun(ctx, cycleUUID, cycle.JobRef)
	if err != nil || got != nil {
		t.Fatalf("log survived the cycle delete: (%v, %v)", got, err)
	}
}

// TestRunCycleRepository_ListRecentDispatched pins the watcher's claim set: it
// must include a cycle that has already CLOSED. The agent Job exits the instant
// it opens its pull request and the auto-merge closes the cycle seconds later —
// long before the next 30s tick — so an open-cycles-only query would capture
// almost nothing.
func TestRunCycleRepository_ListRecentDispatched(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	runs := delivery.NewMilestoneRunRepository(db)
	cycles := delivery.NewRunCycleRepository(db, nil)
	ctx := context.Background()

	run := admitRun(t, runs, "orgc", "proj", 4, "v4")
	open := appendCycle(t, cycles, run, delivery.CycleKindCoding, "ca-open")
	closed := appendCycle(t, cycles, run, delivery.CycleKindFix, "ca-closed")
	if _, err := cycles.Finish(ctx, closed.ID, "sha1"); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	// A cycle that never launched a Job has no pod to read.
	undispatched := appendCycle(t, cycles, run, delivery.CycleKindConflict, "")

	got, err := cycles.ListRecentDispatched(ctx, time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatalf("ListRecentDispatched: %v", err)
	}
	seen := map[string]bool{}
	for i := range got {
		seen[got[i].ID] = true
	}
	if !seen[open.ID] {
		t.Error("an open dispatched cycle must be claimed")
	}
	if !seen[closed.ID] {
		t.Error("a recently closed cycle must still be claimed — its log outlives the cycle")
	}
	if seen[undispatched.ID] {
		t.Error("a cycle with no Job must not be claimed")
	}

	// A window that starts after the close excludes it again.
	later, err := cycles.ListRecentDispatched(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("ListRecentDispatched(future): %v", err)
	}
	for i := range later {
		if later[i].ID == closed.ID {
			t.Error("a cycle closed before the window must be excluded")
		}
	}
	// …but the still-open one is always in scope.
	found := false
	for i := range later {
		if later[i].ID == open.ID {
			found = true
		}
	}
	if !found {
		t.Error("an open cycle is in scope regardless of the window")
	}
}
