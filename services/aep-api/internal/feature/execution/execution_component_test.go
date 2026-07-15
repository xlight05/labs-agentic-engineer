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

// Component tier for the task-log SSE stream: the REAL contract-first strict
// handler (via componenttest, tenant gate in ENFORCE) over GET
// /projects/{p}/tasks/{issueNumber}/log, with the task snapshot / execution
// history / repo lookup faked. Proves the HTTP contract — the text/event-stream
// framing (task → execution → done → [DONE] for a SETTLED task, which is a
// finite stream the recorder can capture), the no-claims 401, and the org
// fence: a caller scoped to another org resolves nil through the snapshot
// reader and surfaces as 404, the S2S/read IDOR fence tasks-github-native §9.1
// relies on.
package execution_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wso2/aep/aep-api/internal/api"
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/feature/execution"
	"github.com/wso2/aep/aep-api/internal/platform/componenttest"
	"github.com/wso2/aep/aep-api/models"
)

// fakeLookup fences GetByIDScoped to one org — a cross-org read misses
// (nil, nil), exactly as the repository does. Backs the per-execution line
// source the stream walks.
type fakeLookup struct {
	org string
	row *models.Execution
}

func (f fakeLookup) GetByIDScoped(_ context.Context, orgID, id string) (*models.Execution, error) {
	if orgID != f.org || f.row == nil || f.row.ID != id {
		return nil, nil
	}
	return f.row, nil
}

// fakeStreamTask returns a Task snapshot fenced to one org; a cross-org read
// misses (nil) → the stream answers 404 before opening.
type fakeStreamTask struct {
	org  string
	snap *execution.TaskSnapshot
}

func (f fakeStreamTask) TaskSnapshot(_ context.Context, orgID, _ string, _ int) (*execution.TaskSnapshot, error) {
	if orgID != f.org {
		return nil, nil
	}
	return f.snap, nil
}

// fakeStreamExecs lists a Task's execution rows for the timeline walk.
type fakeStreamExecs struct{ rows []models.Execution }

func (f fakeStreamExecs) ByIssue(context.Context, string, string, int) ([]models.Execution, error) {
	return f.rows, nil
}

// fakeStreamRepo resolves the project repo (the hub key half).
type fakeStreamRepo struct{}

func (fakeStreamRepo) RepoFullName(context.Context, string, string) (string, error) {
	return "acme/widgets", nil
}

func snapshotJSON(t *testing.T, issue int, derived string) *execution.TaskSnapshot {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"issueNumber": issue, "title": "Implement api", "derivedStatus": derived})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	return &execution.TaskSnapshot{JSON: raw, DerivedStatus: derived}
}

// newStreamHarness wires the real TaskStreamService (org-fenced snapshot + one
// terminal coding execution) behind componenttest. oc nil: a terminal coding
// execution reports terminal-ness without an OC read.
func newStreamHarness(t *testing.T, snap *execution.TaskSnapshot, rows []models.Execution) *componenttest.Harness {
	t.Helper()
	var lookupRow *models.Execution
	if len(rows) > 0 {
		lookupRow = &rows[0]
	}
	progress := execution.NewProgressService(fakeLookup{org: "acme", row: lookupRow}, nil)
	svc := execution.NewTaskStreamService(
		progress,
		fakeStreamTask{org: "acme", snap: snap},
		fakeStreamExecs{rows: rows},
		fakeStreamRepo{},
		execution.NewTaskStreamHub(),
	)
	return componenttest.New(t, componenttest.Options{Deps: api.Deps{TaskStream: svc}})
}

const streamPath = "/api/v1/projects/widgets/tasks/7/log"

// TestTaskStream_SettledTask_StreamsFullStateThenDone: a deployed Task (settled)
// streams its whole state — a `task` frame, an `execution` frame per attempt —
// then `done` + `[DONE]`, and the server closes. A settled task is a FINITE
// stream, so the httptest recorder captures the full body.
func TestTaskStream_SettledTask_StreamsFullStateThenDone(t *testing.T) {
	row := models.Execution{ID: "e1", OrgID: "acme", ProjectID: "widgets",
		Kind: string(taskmeta.KindCoding), Status: string(taskmeta.ExecSucceeded)}
	h := newStreamHarness(t, snapshotJSON(t, 7, "deployed"), []models.Execution{row})

	rec := h.AsOrg("acme").Get(streamPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("stream: code %d (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{`"type":"task"`, `"type":"execution"`, `"type":"done"`, "data: [DONE]"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream body missing %q\n---\n%s", want, body)
		}
	}
	// The execution frame carries the attempt's id (identity the console groups on).
	if !strings.Contains(body, `"id":"e1"`) {
		t.Errorf("execution frame missing the attempt id\n---\n%s", body)
	}
}

// TestTaskStream_CrossTenant_404: the Task belongs to acme; a caller scoped to
// evil must 404 (the org fence, not a leak of "exists but forbidden").
func TestTaskStream_CrossTenant_404(t *testing.T) {
	row := models.Execution{ID: "e1", OrgID: "acme", ProjectID: "widgets",
		Kind: string(taskmeta.KindCoding), Status: string(taskmeta.ExecRunning)}
	h := newStreamHarness(t, snapshotJSON(t, 7, "in_progress"), []models.Execution{row})

	if rec := h.AsOrg("evil").Get(streamPath); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant stream: code %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// TestTaskStream_NoAuth_401: the tenant gate rejects a claimless caller before
// the handler runs.
func TestTaskStream_NoAuth_401(t *testing.T) {
	h := newStreamHarness(t, snapshotJSON(t, 7, "deployed"), nil)
	if rec := h.NoAuth().Get(streamPath); rec.Code != http.StatusUnauthorized {
		t.Errorf("no-auth stream: code %d, want 401", rec.Code)
	}
}
