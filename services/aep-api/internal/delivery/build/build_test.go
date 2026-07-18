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

// COMPONENT tier: the REAL build service behind
// the REAL production handler chain — faked auth at the claims seam →
// contract validation → the deny-by-default tenant gate in ENFORCE → the
// strict handlers (handlers_build.go) — driven in-process via the
// componenttest harness with the service's out-of-process ports faked. The
// non-HTTP StartProjectBuild trigger keeps its direct service-level tests.
//
// External test package: the harness imports api, which imports build — an
// in-package test file would be an import cycle.
package build_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/gen"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/delivery/build"
	deliveryhttpapi "github.com/wso2/aep/aep-api/internal/delivery/httpapi"
	"github.com/wso2/aep/aep-api/internal/edge"
	"github.com/wso2/aep/aep-api/internal/platform/componenttest"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/internal/spec"
)

// ----- fakes -----------------------------------------------------------------

type fakeRunner struct {
	readyErr  error
	startErr  error
	started   []delivery.DevFlowInput
	startedID string
	status    delivery.DevFlowStatus
	statusErr error
}

func (f *fakeRunner) Ready() error { return f.readyErr }
func (f *fakeRunner) StartBuild(_ context.Context, workflowID string, in delivery.DevFlowInput) (string, error) {
	if f.startErr != nil {
		return "", f.startErr
	}
	f.startedID = workflowID
	f.started = append(f.started, in)
	return "run-1", nil
}
func (f *fakeRunner) BuildStatus(context.Context, string) (delivery.DevFlowStatus, error) {
	return f.status, f.statusErr
}

type fakeStore struct {
	running  *delivery.DevflowRun
	row      *delivery.DevflowRun
	rows     []delivery.DevflowRun
	listErr  error
	recorded []*delivery.DevflowRun
}

func (f *fakeStore) RunningDevByProject(context.Context, string, string) (*delivery.DevflowRun, error) {
	return f.running, nil
}
func (f *fakeStore) GetByWorkflowID(context.Context, string, string) (*delivery.DevflowRun, error) {
	return f.row, nil
}
func (f *fakeStore) Record(_ context.Context, row *delivery.DevflowRun) error {
	f.recorded = append(f.recorded, row)
	return nil
}
func (f *fakeStore) ListByProject(_ context.Context, _, _, kind string) ([]delivery.DevflowRun, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	// Mirror the real read: the endpoint asks for dev-kind rows only.
	out := make([]delivery.DevflowRun, 0, len(f.rows))
	for _, r := range f.rows {
		if kind == "" || r.Kind == kind {
			out = append(out, r)
		}
	}
	return out, nil
}

type fakeRepos struct{ err error }

func (f fakeRepos) RepoFullName(context.Context, string, string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "acme/shop", nil
}

type fakeTagger struct {
	res    *spec.SpecSaveResult
	err    error
	called int
}

func (f *fakeTagger) TagSpec(context.Context, string, string) (*spec.SpecSaveResult, error) {
	f.called++
	return f.res, f.err
}

type fakeTasks struct {
	views []delivery.TaskView
	err   error
}

func (f fakeTasks) ListByTag(_ context.Context, _, _, _, tag string) ([]delivery.TaskView, error) {
	if f.err != nil {
		return nil, f.err
	}
	if tag == "" {
		return f.views, nil
	}
	// Mirror the real read: the aep:spec/<tag> label scopes to one version, so
	// every returned row carries that specTag.
	out := make([]delivery.TaskView, 0, len(f.views))
	for _, v := range f.views {
		if v.Lineage.SpecTag == tag {
			out = append(out, v)
		}
	}
	return out, nil
}

func newSvc(runner *fakeRunner, store *fakeStore, repos fakeRepos, tagger *fakeTagger, tasks build.TaskReader) *build.Service {
	return build.NewService(build.Deps{Runner: runner, Store: store, Repos: repos, Tagger: tagger, Tasks: tasks})
}

// newHarness assembles the real handler chain around the real build service.
func newHarness(t *testing.T, svc *build.Service) *componenttest.Harness {
	t.Helper()
	return componenttest.New(t, componenttest.Options{Deps: edge.Deps{
		Delivery: mustDelivery(deliveryhttpapi.New(deliveryhttpapi.Deps{BuildSvc: svc})),
	}})
}

// mustDelivery assembles the delivery domain aggregator for the harness (New
// never errors today; panic keeps the wiring honest if that changes).
func mustDelivery(h *deliveryhttpapi.Handlers, err error) *deliveryhttpapi.Handlers {
	if err != nil {
		panic(err)
	}
	return h
}

func postBuild(t *testing.T, svc *build.Service, project string) (int, string) {
	t.Helper()
	resp := newHarness(t, svc).AsOrg("acme").Post("/api/v1/projects/"+project+"/build", `{}`)
	return resp.Code, resp.Body.String()
}

func getBuild(t *testing.T, svc *build.Service, project, tag string) (int, string) {
	t.Helper()
	resp := newHarness(t, svc).AsOrg("acme").Get("/api/v1/projects/" + project + "/build/" + tag)
	return resp.Code, resp.Body.String()
}

func listBuilds(t *testing.T, svc *build.Service, project string) (int, string) {
	t.Helper()
	resp := newHarness(t, svc).AsOrg("acme").Get("/api/v1/projects/" + project + "/builds")
	return resp.Code, resp.Body.String()
}

func decodeBody[T any](t *testing.T, body string) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode body: %v\n%s", err, body)
	}
	return out
}

// ----- POST /build ------------------------------------------------------------

func TestBuild_TagsAndStartsWorkflow(t *testing.T) {
	runner := &fakeRunner{}
	store := &fakeStore{}
	tagger := &fakeTagger{res: &spec.SpecSaveResult{Status: "approved", Tag: "v1", Version: 1}}
	svc := newSvc(runner, store, fakeRepos{}, tagger, fakeTasks{})

	code, body := postBuild(t, svc, "shop")
	if code != 200 {
		t.Fatalf("build: got %d body=%s", code, body)
	}
	out := decodeBody[gen.BuildResponse](t, body)
	if out.Tag != "v1" {
		t.Errorf("tag = %q, want v1", out.Tag)
	}
	if runner.startedID != "devflow-acme-shop-v1" {
		t.Errorf("workflow id = %q, want devflow-acme-shop-v1", runner.startedID)
	}
	if len(runner.started) != 1 {
		t.Fatalf("started %d workflows, want 1", len(runner.started))
	}
	in := runner.started[0]
	if in.OrgID != "acme" || in.ProjectID != "shop" || in.Repo != "acme/shop" || in.Tag != "v1" {
		t.Errorf("workflow input = %+v", in)
	}
	if in.Gates.Auto != nil {
		t.Errorf("gates must be the zero config (all auto), got %+v", in.Gates)
	}
	// The run row is recorded synchronously so an immediate status GET (the
	// tasks page lands right after) never 404s on the org fence.
	if len(store.recorded) != 1 {
		t.Fatalf("recorded %d run rows, want 1", len(store.recorded))
	}
	row := store.recorded[0]
	if row.WorkflowID != "devflow-acme-shop-v1" || row.RunID != "run-1" ||
		row.Kind != delivery.WorkflowKindDev || row.OrgID != "acme" ||
		row.Tag != "v1" || row.Status != delivery.WorkflowStatusRunning {
		t.Errorf("recorded row = %+v", row)
	}
}

func TestBuild_UnchangedSpec_ReturnsExistingTagAndStillStarts(t *testing.T) {
	runner := &fakeRunner{}
	tagger := &fakeTagger{res: &spec.SpecSaveResult{Status: "unchanged", Tag: "v2", Version: 2}}
	svc := newSvc(runner, &fakeStore{}, fakeRepos{}, tagger, fakeTasks{})

	code, body := postBuild(t, svc, "shop")
	if code != 200 {
		t.Fatalf("build: got %d body=%s", code, body)
	}
	if out := decodeBody[gen.BuildResponse](t, body); out.Tag != "v2" {
		t.Errorf("tag = %q, want the existing v2", out.Tag)
	}
	if len(runner.started) != 1 {
		t.Errorf("rebuild of an unchanged spec must still start the workflow")
	}
}

// The spec gate's failure was a Huma 422 problem; on the contract-first edge
// it is a 400 validation_failed whose details keep the per-file path +
// code:message (the error-model break).
func TestBuild_SpecValidationFails_400_NoWorkflow(t *testing.T) {
	runner := &fakeRunner{}
	tagger := &fakeTagger{err: &spec.SpecValidationError{Files: []spec.FileValidationError{
		{Path: "specs/requirements/requirements.md", Code: "MISSING_REQUIREMENTS", Message: "missing"},
		{Path: "specs/design/design.md", Code: "MISSING_DESIGN", Message: "missing"},
	}}}
	svc := newSvc(runner, &fakeStore{}, fakeRepos{}, tagger, fakeTasks{})

	code, body := postBuild(t, svc, "shop")
	if code != 400 {
		t.Fatalf("status = %d, want 400 (was Huma's 422)", code)
	}
	e := componenttest.DecodeEnvelope(t, body)
	if e.Code != "validation_failed" || e.Message != "spec validation failed" {
		t.Fatalf("envelope = %+v, want validation_failed / spec validation failed", e)
	}
	if len(e.Details) != 2 ||
		e.Details[0].Field != "specs/requirements/requirements.md" ||
		!strings.Contains(e.Details[0].Message, "MISSING_REQUIREMENTS") {
		t.Fatalf("details = %+v, want the per-file locations + code:message", e.Details)
	}
	if len(runner.started) != 0 {
		t.Errorf("workflow started despite a failed spec gate")
	}
}

func TestBuild_AlreadyRunning_409_TaggerUntouched(t *testing.T) {
	runner := &fakeRunner{}
	tagger := &fakeTagger{res: &spec.SpecSaveResult{Tag: "v1"}}
	store := &fakeStore{running: &delivery.DevflowRun{WorkflowID: "devflow-acme-shop-v1"}}
	svc := newSvc(runner, store, fakeRepos{}, tagger, fakeTasks{})

	code, body := postBuild(t, svc, "shop")
	if code != 409 {
		t.Fatalf("status = %d, want 409 (body=%s)", code, body)
	}
	if e := componenttest.DecodeEnvelope(t, body); e.Code != "conflict" ||
		e.Message != "a build is already running for this project" {
		t.Fatalf("409 envelope = %+v", e)
	}
	if tagger.called != 0 {
		t.Errorf("tagger called %d times while a build is running, want 0", tagger.called)
	}
}

func TestBuild_NoRepo_404(t *testing.T) {
	svc := newSvc(&fakeRunner{}, &fakeStore{}, fakeRepos{err: sourcecontrol.ErrRepoNotFound},
		&fakeTagger{res: &spec.SpecSaveResult{Tag: "v1"}}, fakeTasks{})
	code, body := postBuild(t, svc, "shop")
	if code != 404 {
		t.Fatalf("status = %d, want 404 (body=%s)", code, body)
	}
	if e := componenttest.DecodeEnvelope(t, body); e.Code != "not_found" ||
		e.Message != "project repository not found" {
		t.Fatalf("404 envelope = %+v", e)
	}
}

func TestBuild_TemporalDown_503_NoTag(t *testing.T) {
	runner := &fakeRunner{readyErr: build.ErrTemporalUnavailable}
	tagger := &fakeTagger{res: &spec.SpecSaveResult{Tag: "v1"}}
	svc := newSvc(runner, &fakeStore{}, fakeRepos{}, tagger, fakeTasks{})

	code, body := postBuild(t, svc, "shop")
	if code != 503 {
		t.Fatalf("status = %d, want 503 (body=%s)", code, body)
	}
	if e := componenttest.DecodeEnvelope(t, body); e.Code != "service_unavailable" ||
		e.Message != "temporal_unavailable" {
		t.Fatalf("503 envelope = %+v", e)
	}
	if tagger.called != 0 {
		t.Errorf("a tag was cut while Temporal was unavailable — the probe must run first")
	}
}

func TestBuild_RepoNotReady_409(t *testing.T) {
	tagger := &fakeTagger{err: sourcecontrol.ErrRepoNotReady}
	svc := newSvc(&fakeRunner{}, &fakeStore{}, fakeRepos{}, tagger, fakeTasks{})
	code, body := postBuild(t, svc, "shop")
	if code != 409 {
		t.Fatalf("status = %d, want 409 (body=%s)", code, body)
	}
}

// The gate outranks the handler: a claimless build request is the tenant
// gate's ENFORCE 401 — the service is never reached.
func TestBuild_NoClaims401(t *testing.T) {
	runner := &fakeRunner{}
	svc := newSvc(runner, &fakeStore{}, fakeRepos{}, &fakeTagger{res: &spec.SpecSaveResult{Tag: "v1"}}, fakeTasks{})
	resp := newHarness(t, svc).NoAuth().Post("/api/v1/projects/shop/build", `{}`)
	if resp.Code != 401 {
		t.Fatalf("no-claims build: want 401, got %d body=%s", resp.Code, resp.Body.String())
	}
	if len(runner.started) != 0 {
		t.Errorf("claimless request must never reach the service")
	}
}

// ----- StartProjectBuild (non-HTTP provider-build trigger) --------------------

func TestStartProjectBuild_HappyPath_StartsWorkflow(t *testing.T) {
	runner := &fakeRunner{}
	store := &fakeStore{}
	tagger := &fakeTagger{res: &spec.SpecSaveResult{Status: "approved", Tag: "v1", Version: 1}}
	svc := newSvc(runner, store, fakeRepos{}, tagger, fakeTasks{})

	if err := svc.StartProjectBuild(context.Background(), "acme", "shop"); err != nil {
		t.Fatalf("StartProjectBuild: %v", err)
	}
	if runner.startedID != "devflow-acme-shop-v1" {
		t.Errorf("workflow id = %q, want devflow-acme-shop-v1", runner.startedID)
	}
	if len(runner.started) != 1 {
		t.Fatalf("started %d workflows, want 1", len(runner.started))
	}
	if len(store.recorded) != 1 {
		t.Errorf("recorded %d run rows, want 1", len(store.recorded))
	}
}

func TestStartProjectBuild_AlreadyRunning_Nil(t *testing.T) {
	runner := &fakeRunner{}
	tagger := &fakeTagger{res: &spec.SpecSaveResult{Tag: "v1"}}
	store := &fakeStore{running: &delivery.DevflowRun{WorkflowID: "devflow-acme-shop-v1"}}
	svc := newSvc(runner, store, fakeRepos{}, tagger, fakeTasks{})

	// The trigger is idempotent: a running provider build already satisfies it.
	if err := svc.StartProjectBuild(context.Background(), "acme", "shop"); err != nil {
		t.Fatalf("already-running must be treated as success, got: %v", err)
	}
	if tagger.called != 0 {
		t.Errorf("tagger called %d times while a build is running, want 0", tagger.called)
	}
	if len(runner.started) != 0 {
		t.Errorf("started a workflow despite a build already running")
	}
}

// ----- GET /build/{tag} --------------------------------------------------------

func TestGetBuild_MapsPhasesAndSourcesTasksFromLineage(t *testing.T) {
	cases := []struct {
		phase string
		want  string
	}{
		{delivery.DevPhaseValidatingSpec, "started"},
		{delivery.DevPhasePlanning, "in_progress"},
		{delivery.DevPhaseExecuting, "in_progress"},
		{delivery.DevPhaseValidating, "in_progress"},
		{delivery.DevPhaseDone, "completed"},
		{delivery.DevPhaseFailed, "failed"},
	}
	for _, tc := range cases {
		runner := &fakeRunner{status: delivery.DevFlowStatus{
			Phase: tc.phase,
			Tasks: []delivery.DevTaskRef{
				{Issue: 7, Phase: delivery.TaskPhaseCoding},
				{Issue: 8, Phase: delivery.TaskPhaseDone, Outcome: delivery.OutcomeSucceeded},
			},
		}}
		store := &fakeStore{row: &delivery.DevflowRun{WorkflowID: "devflow-acme-shop-v1", Status: delivery.WorkflowStatusRunning}}
		// The DURABLE source is the lineage-tag read: issues 7 & 8 are stamped
		// v1 (this build); issue 99 belongs to an older tag and must be excluded.
		tasks := fakeTasks{views: []delivery.TaskView{
			{IssueNumber: 8, Title: "Build widget", Lineage: delivery.Lineage{SpecTag: "v1"}, DerivedStatus: "deployed"},
			{IssueNumber: 7, Title: "Implement api", Lineage: delivery.Lineage{SpecTag: "v1"}, DerivedStatus: "in_progress"},
			{IssueNumber: 99, Title: "Old version task", Lineage: delivery.Lineage{SpecTag: "v0"}, DerivedStatus: "deployed"},
		}}
		svc := newSvc(runner, store, fakeRepos{}, &fakeTagger{}, tasks)

		code, rawBody := getBuild(t, svc, "shop", "v1")
		if code != 200 {
			t.Fatalf("get(%s): got %d body=%s", tc.phase, code, rawBody)
		}
		body := decodeBody[gen.BuildStatus](t, rawBody)
		if body.Status != tc.want {
			t.Errorf("phase %s → status %q, want %q", tc.phase, body.Status, tc.want)
		}
		if body.WorkflowStatus != tc.phase {
			t.Errorf("workflow_status = %q, want the raw phase %q", body.WorkflowStatus, tc.phase)
		}
		if len(body.Tasks) != 2 {
			t.Fatalf("tasks = %+v, want 2 (issue 99 filtered by lineage)", body.Tasks)
		}
		// Sorted by issue number; each row carries issueNumber + the durable title.
		if got := body.Tasks[0]; got.IssueNumber != 7 || got.Title != "Implement api" || got.Status != "in_progress" {
			t.Errorf("task 7 = %+v, want {7, Implement api, in_progress} (ref refines coding→in_progress)", got)
		}
		// Issue 8 is deployed durably AND done/succeeded in the live refs → completed.
		if got := body.Tasks[1]; got.IssueNumber != 8 || got.Title != "Build widget" || got.Status != "completed" {
			t.Errorf("task 8 = %+v, want {8, Build widget, completed}", got)
		}
	}
}

// The durability payoff: even when the live query is gone (archived run), the
// build still lists its tasks from the lineage read, with each row's derived
// status — no more empty task list on a completed/archived build.
func TestGetBuild_QueryFails_StillListsDurableTasks(t *testing.T) {
	runner := &fakeRunner{statusErr: errors.New("run archived — no live query")}
	store := &fakeStore{row: &delivery.DevflowRun{WorkflowID: "devflow-acme-shop-v1", Status: delivery.WorkflowStatusCompleted}}
	tasks := fakeTasks{views: []delivery.TaskView{
		{IssueNumber: 5, Title: "Ship it", Lineage: delivery.Lineage{SpecTag: "v1"}, DerivedStatus: "deployed"},
		{IssueNumber: 6, Title: "Other build", Lineage: delivery.Lineage{SpecTag: "v2"}, DerivedStatus: "deployed"},
	}}
	svc := newSvc(runner, store, fakeRepos{}, &fakeTagger{}, tasks)

	code, rawBody := getBuild(t, svc, "shop", "v1")
	if code != 200 {
		t.Fatalf("get: got %d body=%s", code, rawBody)
	}
	body := decodeBody[gen.BuildStatus](t, rawBody)
	if body.Status != "completed" {
		t.Errorf("status = %q, want completed (from the archived row)", body.Status)
	}
	if len(body.Tasks) != 1 {
		t.Fatalf("tasks = %+v, want 1 (only v1 lineage)", body.Tasks)
	}
	if got := body.Tasks[0]; got.IssueNumber != 5 || got.Title != "Ship it" || got.Status != "completed" {
		t.Errorf("task = %+v, want {5, Ship it, completed} from the durable derived status", got)
	}
}

// ----- GET /builds --------------------------------------------------------------

func TestListBuilds_NewestFirstOneEntryPerTag(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{rows: []delivery.DevflowRun{
		// Newest first, as the repository returns them. v1 appears twice (a
		// same-tag rebuild writes a second (workflowID, runID) row) — only the
		// newest run represents the tag.
		{Kind: delivery.WorkflowKindDev, Tag: "v2", Status: delivery.WorkflowStatusRunning,
			TasksTotal: 4, TasksDone: 1, TasksFailed: 1, CreatedAt: t0.Add(2 * time.Hour), UpdatedAt: t0.Add(3 * time.Hour)},
		{Kind: delivery.WorkflowKindDev, Tag: "v1", Status: delivery.WorkflowStatusCompleted,
			TasksTotal: 3, TasksDone: 3, CreatedAt: t0.Add(time.Hour), UpdatedAt: t0.Add(90 * time.Minute)},
		{Kind: delivery.WorkflowKindDev, Tag: "v1", Status: delivery.WorkflowStatusFailed,
			TasksTotal: 3, TasksDone: 1, TasksFailed: 2, CreatedAt: t0, UpdatedAt: t0.Add(time.Minute)},
	}}
	svc := newSvc(&fakeRunner{}, store, fakeRepos{}, &fakeTagger{}, fakeTasks{})

	code, rawBody := listBuilds(t, svc, "shop")
	if code != 200 {
		t.Fatalf("list: got %d body=%s", code, rawBody)
	}
	builds := decodeBody[gen.BuildList](t, rawBody).Builds
	if len(builds) != 2 {
		t.Fatalf("builds = %+v, want 2 (v1's older run folded into its newest)", builds)
	}
	v2 := builds[0]
	if v2.Tag != "v2" || v2.Status != "in_progress" {
		t.Errorf("builds[0] = %+v, want the running v2 first", v2)
	}
	if v2.Tasks.Total != 4 || v2.Tasks.Done != 1 || v2.Tasks.Failed != 1 || v2.Tasks.Active != 2 {
		t.Errorf("v2 tasks = %+v, want {4,1,1,2}", v2.Tasks)
	}
	if !v2.StartedAt.Equal(t0.Add(2*time.Hour)) || v2.CompletedAt != nil {
		t.Errorf("v2 times = %v/%v, want startedAt=+2h and no completedAt while running", v2.StartedAt, v2.CompletedAt)
	}
	v1 := builds[1]
	if v1.Tag != "v1" || v1.Status != "completed" {
		t.Errorf("builds[1] = %+v, want the completed v1 (newest run wins the tag)", v1)
	}
	if v1.CompletedAt == nil || !v1.CompletedAt.Equal(t0.Add(90*time.Minute)) {
		t.Errorf("v1 completedAt = %v, want the terminal row's updatedAt", v1.CompletedAt)
	}
}

func TestListBuilds_ActiveClampedAndEmptyList(t *testing.T) {
	// A lost total write (done > total) must not render a negative active.
	store := &fakeStore{rows: []delivery.DevflowRun{
		{Kind: delivery.WorkflowKindDev, Tag: "v1", Status: delivery.WorkflowStatusRunning, TasksDone: 2},
	}}
	svc := newSvc(&fakeRunner{}, store, fakeRepos{}, &fakeTagger{}, fakeTasks{})
	code, rawBody := listBuilds(t, svc, "shop")
	if code != 200 {
		t.Fatalf("list: got %d body=%s", code, rawBody)
	}
	if got := decodeBody[gen.BuildList](t, rawBody).Builds[0].Tasks.Active; got != 0 {
		t.Errorf("active = %d, want 0 (clamped)", got)
	}

	// No runs → an empty list serialized as [] (not null).
	svc = newSvc(&fakeRunner{}, &fakeStore{}, fakeRepos{}, &fakeTagger{}, fakeTasks{})
	code, rawBody = listBuilds(t, svc, "shop")
	if code != 200 {
		t.Fatalf("list (empty): got %d body=%s", code, rawBody)
	}
	if !strings.Contains(rawBody, `"builds":[]`) {
		t.Errorf("body = %s, want an empty non-null builds array", rawBody)
	}
}

func TestListBuilds_StoreError_500(t *testing.T) {
	store := &fakeStore{listErr: errors.New("db down")}
	svc := newSvc(&fakeRunner{}, store, fakeRepos{}, &fakeTagger{}, fakeTasks{})
	code, body := listBuilds(t, svc, "shop")
	if code != 500 {
		t.Fatalf("status = %d, want 500 (body=%s)", code, body)
	}
	e := componenttest.DecodeEnvelope(t, body)
	if e.Code != "internal_error" || e.Message != "list builds" {
		t.Fatalf("500 envelope = %+v (must not leak the store error)", e)
	}
}

func TestGetBuild_UnknownTag_404(t *testing.T) {
	svc := newSvc(&fakeRunner{}, &fakeStore{row: nil}, fakeRepos{}, &fakeTagger{}, fakeTasks{})
	code, body := getBuild(t, svc, "shop", "v9")
	if code != 404 {
		t.Fatalf("status = %d, want 404 (org fence / unknown build), body=%s", code, body)
	}
	if e := componenttest.DecodeEnvelope(t, body); e.Code != "not_found" || e.Message != "build not found" {
		t.Fatalf("404 envelope = %+v", e)
	}
}

func TestGetBuild_QueryFails_FallsBackToRowStatus(t *testing.T) {
	runner := &fakeRunner{statusErr: errors.New("temporal query failed")}
	store := &fakeStore{row: &delivery.DevflowRun{WorkflowID: "devflow-acme-shop-v1", Status: delivery.WorkflowStatusFailed}}
	svc := newSvc(runner, store, fakeRepos{}, &fakeTagger{}, fakeTasks{})

	code, rawBody := getBuild(t, svc, "shop", "v1")
	if code != 200 {
		t.Fatalf("get: got %d body=%s", code, rawBody)
	}
	body := decodeBody[gen.BuildStatus](t, rawBody)
	if body.Status != "failed" || body.WorkflowStatus != delivery.WorkflowStatusFailed {
		t.Errorf("fallback body = %+v, want failed/failed", body)
	}
}

func TestGetBuild_TitleFetchFailure_Degrades(t *testing.T) {
	runner := &fakeRunner{status: delivery.DevFlowStatus{
		Phase: delivery.DevPhaseExecuting,
		Tasks: []delivery.DevTaskRef{{Issue: 3, Phase: delivery.TaskPhaseBuilding}},
	}}
	store := &fakeStore{row: &delivery.DevflowRun{Status: delivery.WorkflowStatusRunning}}
	svc := newSvc(runner, store, fakeRepos{}, &fakeTagger{}, fakeTasks{err: errors.New("github down")})

	code, rawBody := getBuild(t, svc, "shop", "v1")
	if code != 200 {
		t.Fatalf("get must not fail on a title-read hiccup: %d body=%s", code, rawBody)
	}
	body := decodeBody[gen.BuildStatus](t, rawBody)
	if body.Tasks[0].Title != "Task #3" {
		t.Errorf("title = %q, want the numbered placeholder", body.Tasks[0].Title)
	}
}

// ----- GET /build/preflight -----------------------------------------------------

// pfDesign / pfStatus are the preflight ports' fakes for the HTTP-surface
// wiring test (the filtering rules themselves are unit-proven in
// preflight_test.go).
type pfDesign struct{ comps []spec.DesignComponent }

func (f pfDesign) ReadDesignComponents(context.Context, string, string) ([]spec.DesignComponent, error) {
	return f.comps, nil
}

type pfStatus struct{}

func (pfStatus) Ready(context.Context, string, string, string) (bool, error) { return false, nil }

func TestGetPreflight_WiredThroughRealService(t *testing.T) {
	comps := []spec.DesignComponent{{Name: "orders", ComponentType: spec.ComponentTypeService,
		Dependencies: []spec.Dependency{
			{Kind: spec.DependencyKindPlatformResource, Name: "orders-db", ResourceType: "postgres-cnpg", Parameters: map[string]any{"instances": 1}},
		}}}
	pfSvc := build.NewPreflightService(build.PreflightDeps{Design: pfDesign{comps: comps}, Status: pfStatus{}})
	h := componenttest.New(t, componenttest.Options{Deps: edge.Deps{
		Delivery: mustDelivery(deliveryhttpapi.New(deliveryhttpapi.Deps{PreflightSvc: pfSvc})),
	}})

	resp := h.AsOrg("acme").Get("/api/v1/projects/shop/build/preflight")
	if resp.Code != 200 {
		t.Fatalf("preflight: got %d body=%s", resp.Code, resp.Body.String())
	}
	pf := decodeBody[gen.BuildPreflight](t, resp.Body.String())
	if !pf.NeedsInput || len(pf.Items) != 1 {
		t.Fatalf("preflight = %+v, want one platform-resource item", pf)
	}
	item := pf.Items[0]
	if item.Dependency != "orders-db" || item.Kind != "platform-resource" || item.ResourceType != "postgres-cnpg" {
		t.Errorf("item = %+v", item)
	}
}

func TestGetPreflight_Unconfigured503(t *testing.T) {
	// The domain is wired but its preflight service is not (an empty Deps): the
	// build handler is non-nil and answers 503 from its own nil guard, exactly as
	// the pre-migration edge did on an unset PreflightSvc.
	h := componenttest.New(t, componenttest.Options{Deps: edge.Deps{
		Delivery: mustDelivery(deliveryhttpapi.New(deliveryhttpapi.Deps{})),
	}})
	resp := h.AsOrg("acme").Get("/api/v1/projects/shop/build/preflight")
	if resp.Code != 503 {
		t.Fatalf("unwired preflight: want 503, got %d body=%s", resp.Code, resp.Body.String())
	}
	e := componenttest.DecodeEnvelope(t, resp.Body.String())
	if e.Code != "service_unavailable" || e.Message != "build preflight is not configured" {
		t.Fatalf("503 envelope = %+v", e)
	}
}
