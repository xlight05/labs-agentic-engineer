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

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/platform/dbtest"
)

// specRun builds a spec-build run row for (org, project) on milestone number n.
func specRun(org, project string, n int, title string) *delivery.MilestoneRun {
	return &delivery.MilestoneRun{
		OrgID:           org,
		ProjectID:       project,
		MilestoneNumber: n,
		MilestoneTitle:  title,
		Origin:          delivery.RunOriginSpecBuild,
	}
}

// incidentRun builds an incident-adoption run row — the species deliberately
// left outside the spec-run mutex.
func incidentRun(org, project string, n int, title string) *delivery.MilestoneRun {
	r := specRun(org, project, n, title)
	r.Origin = delivery.RunOriginIncidentAdoption
	return r
}

// TestMilestoneRunRepository_AdmitAndReadBack pins the insert path: a fresh run
// lands waiting with the default cycle ceiling and zeroed budgets, and reads
// back through the org-fenced lookup.
func TestMilestoneRunRepository_AdmitAndReadBack(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := delivery.NewMilestoneRunRepository(db)
	ctx := context.Background()

	ok, row, err := repo.TryAdmit(ctx, specRun("orga", "proj", 7, "v3"))
	if err != nil || !ok || row == nil {
		t.Fatalf("TryAdmit = (%v, %+v, %v), want admitted", ok, row, err)
	}
	if row.ID == "" {
		t.Fatalf("TryAdmit did not populate the row id: %+v", row)
	}

	got, err := repo.GetByIDScoped(ctx, "orga", row.ID)
	if err != nil || got == nil {
		t.Fatalf("GetByIDScoped = (%+v, %v), want the row", got, err)
	}
	if got.State != delivery.RunStateWaiting {
		t.Fatalf("fresh state = %q, want %q", got.State, delivery.RunStateWaiting)
	}
	if got.CycleCeiling != delivery.RunDefaultCycleCeiling {
		t.Fatalf("cycle ceiling = %d, want the default %d", got.CycleCeiling, delivery.RunDefaultCycleCeiling)
	}
	if got.CyclesTotal != 0 || got.FixCycles != 0 || got.ConflictCycles != 0 || got.BuildRetriggers != 0 {
		t.Fatalf("fresh budgets = %d/%d/%d/%d, want all zero",
			got.CyclesTotal, got.FixCycles, got.ConflictCycles, got.BuildRetriggers)
	}
	if got.TerminalReason != "" || got.ValidationVerdict != "" {
		t.Fatalf("fresh run carries a verdict/reason: %+v", got)
	}
	if got.StartedAt != nil || got.EndedAt != nil {
		t.Fatalf("fresh run is already stamped: started=%v ended=%v", got.StartedAt, got.EndedAt)
	}
	if got.MilestoneNumber != 7 || got.MilestoneTitle != "v3" {
		t.Fatalf("milestone identity = (%d, %q), want (7, \"v3\")", got.MilestoneNumber, got.MilestoneTitle)
	}

	// An unknown origin never reaches the table: the mutex is keyed on
	// origin = 'spec-build', so a typo would silently escape it.
	if ok, _, err := repo.TryAdmit(ctx, &delivery.MilestoneRun{
		OrgID: "orga", ProjectID: "proj", MilestoneNumber: 8, MilestoneTitle: "v4", Origin: "typo",
	}); err == nil || ok {
		t.Fatalf("TryAdmit(unknown origin) = (%v, %v), want a rejection", ok, err)
	}
}

// TestMilestoneRunRepository_SpecRunMutex is the §7 Concurrency invariant: at
// most ONE non-terminal spec-build run per project, while incident-adoption
// runs on other milestones execute concurrently. The DB index is the authority;
// ActiveSpecRunByProject is the read the 409 answers from.
func TestMilestoneRunRepository_SpecRunMutex(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := delivery.NewMilestoneRunRepository(db)
	ctx := context.Background()

	ok, first, err := repo.TryAdmit(ctx, specRun("orga", "proj", 1, "v1"))
	if err != nil || !ok {
		t.Fatalf("TryAdmit(first spec run) = (%v, %v), want admitted", ok, err)
	}

	// A second spec-build run for the same project — even on a DIFFERENT
	// milestone — loses the mutex.
	ok, row, err := repo.TryAdmit(ctx, specRun("orga", "proj", 2, "v2"))
	if err != nil {
		t.Fatalf("TryAdmit(second spec run): %v", err)
	}
	if ok || row != nil {
		t.Fatalf("second active spec run admitted (%+v) — the spec-run mutex is breached", row)
	}

	// An incident-adoption run on another milestone runs concurrently.
	ok, incident, err := repo.TryAdmit(ctx, incidentRun("orga", "proj", 1, "v1"))
	if err != nil || !ok || incident == nil {
		t.Fatalf("TryAdmit(incident) = (%v, %+v, %v), want admitted alongside the spec run", ok, incident, err)
	}
	// And so does a second incident run on yet another milestone.
	if ok, _, err := repo.TryAdmit(ctx, incidentRun("orga", "proj", 9, "v9")); err != nil || !ok {
		t.Fatalf("TryAdmit(second incident) = (%v, %v), want admitted", ok, err)
	}

	// A different project in the same org is unaffected.
	if ok, _, err := repo.TryAdmit(ctx, specRun("orga", "other", 1, "v1")); err != nil || !ok {
		t.Fatalf("TryAdmit(other project) = (%v, %v), want admitted", ok, err)
	}
	// So is the same project slug in a different org.
	if ok, _, err := repo.TryAdmit(ctx, specRun("orgb", "proj", 1, "v1")); err != nil || !ok {
		t.Fatalf("TryAdmit(other org) = (%v, %v), want admitted", ok, err)
	}

	// The 409 read sees the spec run, never the incidents.
	active, err := repo.ActiveSpecRunByProject(ctx, "orga", "proj")
	if err != nil || active == nil {
		t.Fatalf("ActiveSpecRunByProject = (%+v, %v), want the live spec run", active, err)
	}
	if active.ID != first.ID {
		t.Fatalf("ActiveSpecRunByProject returned %s, want the spec run %s", active.ID, first.ID)
	}

	// Settling the spec run frees the project for the next build; the still-live
	// incident runs must not hold the mutex.
	if _, err := repo.Settle(ctx, first.ID, delivery.RunStateSucceeded, ""); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if active, err := repo.ActiveSpecRunByProject(ctx, "orga", "proj"); err != nil || active != nil {
		t.Fatalf("ActiveSpecRunByProject after settle = (%+v, %v), want (nil, nil)", active, err)
	}
	if ok, _, err := repo.TryAdmit(ctx, specRun("orga", "proj", 2, "v2")); err != nil || !ok {
		t.Fatalf("TryAdmit(next build) = (%v, %v), want admitted after the previous run settled", ok, err)
	}
}

// TestMilestoneRunRepository_SpecRunMutexCoversPlanning pins the widened index
// predicate. The plan path admits PLANNING and only leaves it minutes later,
// once the milestone is filled — which is precisely the window a double-click
// lands in, so a mutex that did not cover the state would be unarmed for the
// whole of it. This is the one invariant the new state could have broken.
func TestMilestoneRunRepository_SpecRunMutexCoversPlanning(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := delivery.NewMilestoneRunRepository(db)
	ctx := context.Background()

	planning := specRun("orga", "proj", 1, "v1")
	planning.State = delivery.RunStatePlanning
	ok, first, err := repo.TryAdmit(ctx, planning)
	if err != nil || !ok || first == nil {
		t.Fatalf("TryAdmit(planning) = (%v, %+v, %v), want admitted", ok, first, err)
	}

	if ok, row, err := repo.TryAdmit(ctx, specRun("orga", "proj", 2, "v2")); err != nil || ok {
		t.Fatalf("a second spec run was admitted while one is planning (%+v, %v) — the mutex is unarmed across the plan window", row, err)
	}

	// The 409 read has to agree with the index, or the endpoint would answer
	// "free" for a project the database will refuse.
	active, err := repo.ActiveSpecRunByProject(ctx, "orga", "proj")
	if err != nil || active == nil || active.ID != first.ID {
		t.Fatalf("ActiveSpecRunByProject = (%+v, %v), want the planning run %s", active, err, first.ID)
	}

	// The supervisor's first pass leaves planning; nothing moves back into it.
	if _, err := repo.SetState(ctx, first.ID, delivery.RunStateWaiting); err != nil {
		t.Fatalf("SetState(planning → waiting): %v", err)
	}
	if _, err := repo.SetState(ctx, first.ID, delivery.RunStatePlanning); err == nil {
		t.Fatal("SetState(planning) was accepted — planning is written once, at admission")
	}
}

// TestMilestoneRunRepository_Transitions pins the guarded state machine: the
// loop oscillates waiting ⇄ running keeping its original start stamp, the first
// settle wins, and every later write is a (nil, nil) no-op rather than a
// resurrection.
func TestMilestoneRunRepository_Transitions(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := delivery.NewMilestoneRunRepository(db)
	ctx := context.Background()

	_, run, err := repo.TryAdmit(ctx, specRun("orga", "proj", 1, "v1"))
	if err != nil || run == nil {
		t.Fatalf("TryAdmit: (%+v, %v)", run, err)
	}

	running, err := repo.SetState(ctx, run.ID, delivery.RunStateRunning)
	if err != nil || running == nil {
		t.Fatalf("SetState(running) = (%+v, %v)", running, err)
	}
	if running.StartedAt == nil {
		t.Fatalf("SetState(running) did not stamp started_at: %+v", running)
	}
	startedAt := *running.StartedAt

	// Back to waiting at the cycle boundary, then running again: started_at is
	// the run's FIRST start, not the latest cycle's.
	if _, err := repo.SetState(ctx, run.ID, delivery.RunStateWaiting); err != nil {
		t.Fatalf("SetState(waiting): %v", err)
	}
	again, err := repo.SetState(ctx, run.ID, delivery.RunStateRunning)
	if err != nil || again == nil {
		t.Fatalf("SetState(running again) = (%+v, %v)", again, err)
	}
	if again.StartedAt == nil || !again.StartedAt.Equal(startedAt) {
		t.Fatalf("re-entering running moved started_at: %v, want %v", again.StartedAt, startedAt)
	}

	// SetState refuses a terminal state — that is Settle's job.
	if _, err := repo.SetState(ctx, run.ID, delivery.RunStateFailed); err == nil {
		t.Fatalf("SetState(failed) succeeded, want a rejection pointing at Settle")
	}
	// Settle refuses a non-terminal one, and refuses a reason on success.
	if _, err := repo.Settle(ctx, run.ID, delivery.RunStateRunning, ""); err == nil {
		t.Fatalf("Settle(running) succeeded, want a rejection pointing at SetState")
	}
	if _, err := repo.Settle(ctx, run.ID, delivery.RunStateSucceeded, delivery.RunReasonNoProgress); err == nil {
		t.Fatalf("Settle(succeeded, reason) succeeded, want a rejection")
	}

	settled, err := repo.Settle(ctx, run.ID, delivery.RunStateFailed, delivery.RunReasonCycleCeiling)
	if err != nil || settled == nil {
		t.Fatalf("Settle = (%+v, %v)", settled, err)
	}
	if settled.State != delivery.RunStateFailed ||
		settled.TerminalReason != delivery.RunReasonCycleCeiling || settled.EndedAt == nil {
		t.Fatalf("settled row = %+v, want failed/cycle-ceiling/ended", settled)
	}

	// Every guarded write on a settled run is a no-op returning (nil, nil): a
	// duplicate signal must never resurrect or rewrite a recorded outcome.
	if got, err := repo.SetState(ctx, run.ID, delivery.RunStateRunning); err != nil || got != nil {
		t.Fatalf("SetState on a terminal run = (%+v, %v), want (nil, nil)", got, err)
	}
	if got, err := repo.Settle(ctx, run.ID, delivery.RunStateSucceeded, ""); err != nil || got != nil {
		t.Fatalf("second Settle = (%+v, %v), want (nil, nil)", got, err)
	}
	if got, err := repo.BumpBudget(ctx, run.ID, delivery.RunBudgetCycles); err != nil || got != nil {
		t.Fatalf("BumpBudget on a terminal run = (%+v, %v), want (nil, nil)", got, err)
	}
	if got, err := repo.SetValidationVerdict(ctx, run.ID, delivery.ValidationVerdictPassed); err != nil || got != nil {
		t.Fatalf("SetValidationVerdict on a terminal run = (%+v, %v), want (nil, nil)", got, err)
	}
	// An unknown id is the same no-op, not an error.
	if got, err := repo.SetState(ctx, "00000000-0000-0000-0000-000000000000", delivery.RunStateRunning); err != nil || got != nil {
		t.Fatalf("SetState(unknown id) = (%+v, %v), want (nil, nil)", got, err)
	}

	// The recorded outcome survives untouched.
	final, err := repo.GetByIDScoped(ctx, "orga", run.ID)
	if err != nil || final == nil {
		t.Fatalf("GetByIDScoped: (%+v, %v)", final, err)
	}
	if final.State != delivery.RunStateFailed || final.TerminalReason != delivery.RunReasonCycleCeiling {
		t.Fatalf("outcome was overwritten: %+v", final)
	}
}

// TestMilestoneRunRepository_Budgets pins the counter bumps the supervisor
// checks its limits against, and the validation verdict write.
func TestMilestoneRunRepository_Budgets(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := delivery.NewMilestoneRunRepository(db)
	ctx := context.Background()

	_, run, err := repo.TryAdmit(ctx, specRun("orga", "proj", 1, "v1"))
	if err != nil || run == nil {
		t.Fatalf("TryAdmit: (%+v, %v)", run, err)
	}

	// Each counter moves independently — a budget that named more than one
	// failure class would make the terminal reasons dishonest.
	for i := 0; i < 3; i++ {
		if _, err := repo.BumpBudget(ctx, run.ID, delivery.RunBudgetCycles); err != nil {
			t.Fatalf("BumpBudget(cycles) #%d: %v", i, err)
		}
	}
	if _, err := repo.BumpBudget(ctx, run.ID, delivery.RunBudgetFixCycles); err != nil {
		t.Fatalf("BumpBudget(fix): %v", err)
	}
	if _, err := repo.BumpBudget(ctx, run.ID, delivery.RunBudgetConflictCycles); err != nil {
		t.Fatalf("BumpBudget(conflict): %v", err)
	}
	got, err := repo.BumpBudget(ctx, run.ID, delivery.RunBudgetBuildRetriggers)
	if err != nil || got == nil {
		t.Fatalf("BumpBudget(build re-trigger) = (%+v, %v)", got, err)
	}
	if got.CyclesTotal != 3 || got.FixCycles != 1 || got.ConflictCycles != 1 || got.BuildRetriggers != 1 {
		t.Fatalf("budgets = %d/%d/%d/%d, want 3/1/1/1",
			got.CyclesTotal, got.FixCycles, got.ConflictCycles, got.BuildRetriggers)
	}

	// An unknown counter never reaches SQL.
	if _, err := repo.BumpBudget(ctx, run.ID, delivery.RunBudget("drop table")); err == nil {
		t.Fatalf("BumpBudget(unknown counter) succeeded, want a rejection")
	}

	// The verdict is a run property, written when the validation cycle settles.
	if _, err := repo.SetValidationVerdict(ctx, run.ID, "maybe"); err == nil {
		t.Fatalf("SetValidationVerdict(unknown) succeeded, want a rejection")
	}
	verdicted, err := repo.SetValidationVerdict(ctx, run.ID, delivery.ValidationVerdictFailed)
	if err != nil || verdicted == nil {
		t.Fatalf("SetValidationVerdict = (%+v, %v)", verdicted, err)
	}
	if verdicted.ValidationVerdict != delivery.ValidationVerdictFailed {
		t.Fatalf("verdict = %q, want %q", verdicted.ValidationVerdict, delivery.ValidationVerdictFailed)
	}
}

// TestMilestoneRunRepository_ReadsAndTagResolution pins the read surface: the
// project ledger newest-first, the per-milestone list (a milestone sees
// sequential runs across its life), and the `?tag=` → milestone-number
// resolution that replaces title-matching against GitHub.
func TestMilestoneRunRepository_ReadsAndTagResolution(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := delivery.NewMilestoneRunRepository(db)
	ctx := context.Background()

	// Explicit CreatedAt stamps: back-to-back inserts would otherwise rely on
	// Postgres microsecond resolution to separate them.
	base := time.Now().UTC().Add(-time.Hour)
	mk := func(run *delivery.MilestoneRun, offset time.Duration) *delivery.MilestoneRun {
		run.CreatedAt = base.Add(offset)
		ok, row, err := repo.TryAdmit(ctx, run)
		if err != nil || !ok || row == nil {
			t.Fatalf("TryAdmit(%s/%d) = (%v, %v)", run.MilestoneTitle, run.MilestoneNumber, ok, err)
		}
		return row
	}

	v1 := mk(specRun("orga", "proj", 11, "v1"), 0)
	if _, err := repo.Settle(ctx, v1.ID, delivery.RunStateSucceeded, ""); err != nil {
		t.Fatalf("Settle v1: %v", err)
	}
	// A later incident adopts into v1's milestone — same milestone, second run.
	v1Incident := mk(incidentRun("orga", "proj", 11, "v1"), time.Minute)
	v2 := mk(specRun("orga", "proj", 12, "v2"), 2*time.Minute)

	rows, err := repo.ListByProject(ctx, "orga", "proj")
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("ListByProject = %d rows, want 3", len(rows))
	}
	if rows[0].ID != v2.ID || rows[1].ID != v1Incident.ID || rows[2].ID != v1.ID {
		t.Fatalf("ListByProject order = [%s %s %s], want newest-first [v2, v1-incident, v1]",
			rows[0].MilestoneTitle, rows[1].MilestoneTitle, rows[2].MilestoneTitle)
	}

	byMilestone, err := repo.ListByMilestone(ctx, "orga", "proj", 11)
	if err != nil {
		t.Fatalf("ListByMilestone: %v", err)
	}
	if len(byMilestone) != 2 || byMilestone[0].ID != v1Incident.ID || byMilestone[1].ID != v1.ID {
		t.Fatalf("ListByMilestone(11) = %d rows %+v, want the two v1 runs newest-first",
			len(byMilestone), byMilestone)
	}

	// `?tag=` resolves through the run rows, never by title-matching GitHub.
	num, found, err := repo.MilestoneNumberForTag(ctx, "orga", "proj", "v2")
	if err != nil || !found || num != 12 {
		t.Fatalf("MilestoneNumberForTag(v2) = (%d, %v, %v), want (12, true, nil)", num, found, err)
	}
	if num, found, err := repo.MilestoneNumberForTag(ctx, "orga", "proj", "v1"); err != nil || !found || num != 11 {
		t.Fatalf("MilestoneNumberForTag(v1) = (%d, %v, %v), want (11, true, nil)", num, found, err)
	}
	// A tag the project never built is a clean miss, not an error.
	if num, found, err := repo.MilestoneNumberForTag(ctx, "orga", "proj", "v99"); err != nil || found || num != 0 {
		t.Fatalf("MilestoneNumberForTag(v99) = (%d, %v, %v), want (0, false, nil)", num, found, err)
	}

	// Org scoping: another org sees none of it — a cross-org read MISSES, so the
	// HTTP layer renders 404 rather than 403.
	if got, err := repo.GetByIDScoped(ctx, "orgb", v2.ID); err != nil || got != nil {
		t.Fatalf("GetByIDScoped(cross-org) = (%+v, %v), want (nil, nil)", got, err)
	}
	if rows, err := repo.ListByProject(ctx, "orgb", "proj"); err != nil || len(rows) != 0 {
		t.Fatalf("ListByProject(cross-org) = (%d rows, %v), want 0", len(rows), err)
	}
	if rows, err := repo.ListByMilestone(ctx, "orgb", "proj", 11); err != nil || len(rows) != 0 {
		t.Fatalf("ListByMilestone(cross-org) = (%d rows, %v), want 0", len(rows), err)
	}
	if _, found, err := repo.MilestoneNumberForTag(ctx, "orgb", "proj", "v2"); err != nil || found {
		t.Fatalf("MilestoneNumberForTag(cross-org) found = %v, want false", found)
	}
	if got, err := repo.ActiveSpecRunByProject(ctx, "orgb", "proj"); err != nil || got != nil {
		t.Fatalf("ActiveSpecRunByProject(cross-org) = (%+v, %v), want (nil, nil)", got, err)
	}

	// The project-delete cascade leaves nothing behind, so a recreated
	// same-named project cannot resolve a tag to a milestone it never had.
	if err := repo.DeleteByProject(ctx, "orga", "proj"); err != nil {
		t.Fatalf("DeleteByProject: %v", err)
	}
	if rows, err := repo.ListByProject(ctx, "orga", "proj"); err != nil || len(rows) != 0 {
		t.Fatalf("after purge: (%d rows, %v), want 0", len(rows), err)
	}
	if _, found, err := repo.MilestoneNumberForTag(ctx, "orga", "proj", "v2"); err != nil || found {
		t.Fatalf("MilestoneNumberForTag after purge found = %v, want false", found)
	}
}
