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

package repositories_test

import (
	"context"
	"testing"

	"github.com/wso2/aep/aep-api/internal/platform/dbtest"
	"github.com/wso2/aep/aep-api/models"
	"github.com/wso2/aep/aep-api/repositories"
)

func devRun(org, project, wfID, tag string) *models.DevflowRun {
	return &models.DevflowRun{
		WorkflowID: wfID,
		RunID:      wfID + "-run1",
		Kind:       models.WorkflowKindDev,
		OrgID:      org,
		ProjectID:  project,
		Tag:        tag,
	}
}

// TestWorkflowRunRepository_TaskCounts pins the build-stage write path: fresh
// rows read 0/0/0, SetTaskCounts writes absolute values (a retried write must
// not sum), and Record's upsert must NOT clobber a tally written before it —
// the workflow re-Records the row the start endpoint already recorded.
func TestWorkflowRunRepository_TaskCounts(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := repositories.NewWorkflowRunRepository(db)
	ctx := context.Background()

	row := devRun("orga", "proj", "devflow-orga-proj-v1", "v1")
	if err := repo.Record(ctx, row); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := repo.GetByWorkflowID(ctx, "orga", row.WorkflowID)
	if err != nil || got == nil {
		t.Fatalf("GetByWorkflowID: (%+v, %v)", got, err)
	}
	if got.TasksTotal != 0 || got.TasksDone != 0 || got.TasksFailed != 0 {
		t.Fatalf("fresh counts = %d/%d/%d, want 0/0/0", got.TasksTotal, got.TasksDone, got.TasksFailed)
	}

	// Absolute semantics: a second write (activity retry / next transition)
	// replaces, never accumulates.
	if err := repo.SetTaskCounts(ctx, row.WorkflowID, row.RunID, 3, 0, 0); err != nil {
		t.Fatalf("SetTaskCounts: %v", err)
	}
	if err := repo.SetTaskCounts(ctx, row.WorkflowID, row.RunID, 3, 1, 1); err != nil {
		t.Fatalf("SetTaskCounts rewrite: %v", err)
	}
	got, err = repo.GetByWorkflowID(ctx, "orga", row.WorkflowID)
	if err != nil || got == nil {
		t.Fatalf("GetByWorkflowID after counts: (%+v, %v)", got, err)
	}
	if got.TasksTotal != 3 || got.TasksDone != 1 || got.TasksFailed != 1 {
		t.Fatalf("counts = %d/%d/%d, want 3/1/1", got.TasksTotal, got.TasksDone, got.TasksFailed)
	}

	// Record runs twice per run (endpoint + workflow first activity); the
	// second upsert must leave the tally untouched.
	again := devRun("orga", "proj", row.WorkflowID, "v1")
	if err := repo.Record(ctx, again); err != nil {
		t.Fatalf("re-Record: %v", err)
	}
	got, err = repo.GetByWorkflowID(ctx, "orga", row.WorkflowID)
	if err != nil || got == nil {
		t.Fatalf("GetByWorkflowID after re-Record: (%+v, %v)", got, err)
	}
	if got.TasksTotal != 3 || got.TasksDone != 1 || got.TasksFailed != 1 {
		t.Fatalf("re-Record clobbered counts: %d/%d/%d, want 3/1/1",
			got.TasksTotal, got.TasksDone, got.TasksFailed)
	}

	// Run scoping: a same-tag rebuild reuses the workflow id with a NEW run
	// id — its writes must leave the prior execution's frozen tally alone.
	rebuild := devRun("orga", "proj", row.WorkflowID, "v1")
	rebuild.RunID = row.WorkflowID + "-run2"
	if err := repo.Record(ctx, rebuild); err != nil {
		t.Fatalf("Record rebuild: %v", err)
	}
	if err := repo.SetTaskCounts(ctx, row.WorkflowID, rebuild.RunID, 3, 0, 0); err != nil {
		t.Fatalf("SetTaskCounts rebuild: %v", err)
	}
	rows, err := repo.ListByProject(ctx, "orga", "proj", models.WorkflowKindDev)
	if err != nil || len(rows) != 2 {
		t.Fatalf("ListByProject = (%d rows, %v), want 2", len(rows), err)
	}
	for _, r := range rows {
		if r.RunID == row.RunID && (r.TasksTotal != 3 || r.TasksDone != 1 || r.TasksFailed != 1) {
			t.Fatalf("rebuild write leaked into the prior run's tally: %d/%d/%d", r.TasksTotal, r.TasksDone, r.TasksFailed)
		}
		if r.RunID == rebuild.RunID && (r.TasksDone != 0 || r.TasksFailed != 0) {
			t.Fatalf("rebuild row tally = %d/%d, want 0/0", r.TasksDone, r.TasksFailed)
		}
	}
}

// TestWorkflowRunRepository_ValidationRunByParent pins the status builder's
// validation read: the lookup returns only the validation-kind child (the
// validation-phase orchestrator) of a given dev run, not its coding-task
// siblings — and the signaler's RunningTaskByIssue resolves the orchestrator
// by the validation issue's number just like a coding task's row.
func TestWorkflowRunRepository_ValidationRunByParent(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := repositories.NewWorkflowRunRepository(db)
	ctx := context.Background()

	dev := devRun("orga", "proj", "devflow-orga-proj-v1", "v1")
	if err := repo.Record(ctx, dev); err != nil {
		t.Fatalf("Record dev: %v", err)
	}
	// A coding-task child and the validation-phase orchestrator, both parented
	// to the dev run.
	coding := &models.DevflowRun{
		WorkflowID: "taskflow-orga-proj-5", RunID: "r-coding",
		Kind: models.WorkflowKindTask, OrgID: "orga", ProjectID: "proj",
		Repo: "acme/proj", IssueNumber: 5, ParentWorkflowID: dev.WorkflowID,
	}
	validation := &models.DevflowRun{
		WorkflowID: "validationflow-orga-proj-v1", RunID: "r-validation",
		Kind:  models.WorkflowKindValidation,
		OrgID: "orga", ProjectID: "proj", Repo: "acme/proj", IssueNumber: 9,
		ParentWorkflowID: dev.WorkflowID,
	}
	if err := repo.Record(ctx, coding); err != nil {
		t.Fatalf("Record coding: %v", err)
	}
	if err := repo.Record(ctx, validation); err != nil {
		t.Fatalf("Record validation: %v", err)
	}

	got, err := repo.ValidationRunByParent(ctx, "orga", "proj", dev.WorkflowID)
	if err != nil {
		t.Fatalf("ValidationRunByParent: %v", err)
	}
	if got == nil || got.IssueNumber != 9 || got.Kind != models.WorkflowKindValidation {
		t.Fatalf("ValidationRunByParent = %+v, want the validation orchestrator (issue 9)", got)
	}

	// The signaler's lookup owns the validation issue's webhook signals via the
	// orchestrator's running row (kind IN task, validation).
	if got, err := repo.RunningTaskByIssue(ctx, "acme/proj", 9); err != nil || got == nil ||
		got.WorkflowID != validation.WorkflowID {
		t.Fatalf("RunningTaskByIssue(validation issue) = (%+v, %v), want the orchestrator row", got, err)
	}

	// A parent with no validation child misses cleanly (nil, nil).
	if got, err := repo.ValidationRunByParent(ctx, "orga", "proj", "some-other-wf"); err != nil || got != nil {
		t.Fatalf("ValidationRunByParent(no match) = (%+v, %v), want (nil, nil)", got, err)
	}
}

// TestWorkflowRunRepository_ListByProject pins the status read path: newest
// first, kind-filtered — the overview's build stage reads rows[0] of the dev
// kind and the deploy version scans for the newest completed one.
func TestWorkflowRunRepository_ListByProject(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := repositories.NewWorkflowRunRepository(db)
	ctx := context.Background()

	v1 := devRun("orga", "proj", "devflow-orga-proj-v1", "v1")
	if err := repo.Record(ctx, v1); err != nil {
		t.Fatalf("Record v1: %v", err)
	}
	if err := repo.SetStatus(ctx, v1.WorkflowID, models.WorkflowStatusCompleted, ""); err != nil {
		t.Fatalf("SetStatus v1: %v", err)
	}
	// A failed status persists its reason; a completed one leaves it empty.
	failed := devRun("orga", "proj", "devflow-orga-proj-vfail", "vfail")
	if err := repo.Record(ctx, failed); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.SetStatus(ctx, failed.WorkflowID, models.WorkflowStatusFailed, "provisioning failed: org-service provider not found"); err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}
	if got, err := repo.GetByWorkflowID(ctx, "orga", failed.WorkflowID); err != nil {
		t.Fatalf("GetByWorkflowID failed: %v", err)
	} else if got == nil || got.Reason != "provisioning failed: org-service provider not found" {
		t.Fatalf("reason not persisted, got %+v", got)
	}
	v2 := devRun("orga", "proj", "devflow-orga-proj-v2", "v2")
	if err := repo.Record(ctx, v2); err != nil {
		t.Fatalf("Record v2: %v", err)
	}
	task := &models.DevflowRun{
		WorkflowID: "taskflow-orga-proj-7", RunID: "r1",
		Kind: models.WorkflowKindTask, OrgID: "orga", ProjectID: "proj",
		Repo: "acme/proj", IssueNumber: 7,
	}
	if err := repo.Record(ctx, task); err != nil {
		t.Fatalf("Record task: %v", err)
	}

	rows, err := repo.ListByProject(ctx, "orga", "proj", models.WorkflowKindDev)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("ListByProject(dev) = %d rows, want 3 (v2, vfail, v1 — the task row filtered)", len(rows))
	}
	if rows[0].Tag != "v2" || rows[1].Tag != "vfail" || rows[2].Tag != "v1" {
		t.Fatalf("order = [%s, %s, %s], want newest-first [v2, vfail, v1]", rows[0].Tag, rows[1].Tag, rows[2].Tag)
	}
	if rows[0].Status != models.WorkflowStatusRunning ||
		rows[1].Status != models.WorkflowStatusFailed ||
		rows[2].Status != models.WorkflowStatusCompleted {
		t.Fatalf("statuses = [%s, %s, %s], want [running, failed, completed]",
			rows[0].Status, rows[1].Status, rows[2].Status)
	}

	// The delete cascade purges every row (dev AND task kinds) so a
	// recreated same-named project starts with a clean index.
	if err := repo.DeleteByProject(ctx, "orga", "proj"); err != nil {
		t.Fatalf("DeleteByProject: %v", err)
	}
	rows, err = repo.ListByProject(ctx, "orga", "proj", "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("after purge: (%d rows, %v), want 0", len(rows), err)
	}
}
