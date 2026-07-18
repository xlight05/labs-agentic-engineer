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

package provisioning

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/dependencies"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/internal/spec"
)

// ---- fakes -----------------------------------------------------------------

type fakeIssues struct {
	list     []sourcecontrol.IssueInfo
	created  []sourcecontrol.CreateIssueRequest
	closed   map[int]string
	comments map[int][]string
	nextNum  int
	// project records the project each CREATED issue belongs to so ListIssues can
	// filter by project (production lists issues per project repo). Seeded issues
	// carry no project and match every project — backward compatible with the
	// single-project tests.
	project map[int]string
	// raceNewIssues simulates GitHub's eventually-consistent label-filtered issue
	// LIST: when true, an issue just returned by CreateIssue is hidden from
	// ListIssues (as if the list index has not caught up yet). CreateIssue still
	// returns the real number — this is exactly the read-after-write race #164 hits.
	raceNewIssues  bool
	hiddenFromList map[int]bool
}

func newFakeIssues(seed []sourcecontrol.IssueInfo) *fakeIssues {
	max := 0
	for _, i := range seed {
		if i.Number > max {
			max = i.Number
		}
	}
	return &fakeIssues{list: seed, closed: map[int]string{}, comments: map[int][]string{}, nextNum: max + 1, project: map[int]string{}, hiddenFromList: map[int]bool{}}
}

func (f *fakeIssues) ListIssues(_ context.Context, _, projectID string, _ []string) ([]sourcecontrol.IssueInfo, error) {
	var out []sourcecontrol.IssueInfo
	for _, i := range f.list {
		if f.hiddenFromList[i.Number] {
			continue // eventually-consistent list has not caught up to this create yet
		}
		if p := f.project[i.Number]; p == "" || p == projectID {
			out = append(out, i)
		}
	}
	return out, nil
}
func (f *fakeIssues) CreateIssue(_ context.Context, _, projectID string, req sourcecontrol.CreateIssueRequest) (*sourcecontrol.IssueResult, error) {
	f.created = append(f.created, req)
	n := f.nextNum
	f.nextNum++
	f.list = append(f.list, sourcecontrol.IssueInfo{Number: n, Title: req.Title, Body: req.Body, State: "open", Labels: req.Labels})
	f.project[n] = projectID
	if f.raceNewIssues {
		f.hiddenFromList[n] = true
	}
	return &sourcecontrol.IssueResult{Number: n}, nil
}
func (f *fakeIssues) CloseIssue(_ context.Context, _, _ string, number int, comment string) error {
	f.closed[number] = comment
	for i := range f.list {
		if f.list[i].Number == number {
			f.list[i].State = "closed"
		}
	}
	return nil
}
func (f *fakeIssues) CommentIssue(_ context.Context, _, _ string, number int, body string) error {
	f.comments[number] = append(f.comments[number], body)
	return nil
}

type fakeExecStore struct {
	rows   []*delivery.Execution
	nextID int
}

func (f *fakeExecStore) TryAdmit(_ context.Context, e *delivery.Execution) (bool, *delivery.Execution, error) {
	for _, r := range f.rows {
		if r.Repo == e.Repo && r.IssueNumber == e.IssueNumber && r.Kind == e.Kind &&
			taskmeta.ExecutionStatus(r.Status).IsActive() {
			return false, r, nil
		}
	}
	f.nextID++
	e.ID = fmt.Sprintf("exec-%d", f.nextID)
	f.rows = append(f.rows, e)
	return true, e, nil
}
func (f *fakeExecStore) StartWithRun(_ context.Context, id, runName string) (*delivery.Execution, error) {
	for _, r := range f.rows {
		if r.ID == id {
			r.Status = string(taskmeta.ExecRunning)
			r.RunName = runName
			now := time.Unix(1000, 0)
			r.StartedAt = &now
			return r, nil
		}
	}
	return nil, fmt.Errorf("not found")
}
func (f *fakeExecStore) Finish(_ context.Context, id, status, reason string) (*delivery.Execution, error) {
	for _, r := range f.rows {
		if r.ID == id {
			r.Status = status
			r.Reason = reason
			return r, nil
		}
	}
	return nil, nil
}
func (f *fakeExecStore) ListActive(_ context.Context) ([]delivery.Execution, error) {
	var out []delivery.Execution
	for _, r := range f.rows {
		if taskmeta.ExecutionStatus(r.Status).IsActive() {
			out = append(out, *r)
		}
	}
	return out, nil
}

type fakeReeval struct{ calls int }

func (f *fakeReeval) Reevaluate(context.Context) error { f.calls++; return nil }

type fakeDesign struct{ comps []spec.DesignComponent }

func (f fakeDesign) ReadDesignComponents(context.Context, string, string) ([]spec.DesignComponent, error) {
	return f.comps, nil
}

type fakeRepos struct{}

func (fakeRepos) RepoFullName(context.Context, string, string) (string, error) { return "o/r", nil }
func (fakeRepos) ByFullName(context.Context, string) (string, string, error) {
	return "acme", "warehouse", nil
}

type fakeCatalog struct {
	entries map[string]*dependencies.ExternalResource
	deleted []string
}

func (f *fakeCatalog) Get(_ context.Context, _, name string) (*dependencies.ExternalResource, error) {
	return f.entries[name], nil
}
func (f *fakeCatalog) List(_ context.Context, _ string) ([]dependencies.ExternalResource, error) {
	out := make([]dependencies.ExternalResource, 0, len(f.entries))
	for _, e := range f.entries {
		out = append(out, *e)
	}
	return out, nil
}
func (f *fakeCatalog) Delete(_ context.Context, _, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}

type fakeExtProv struct {
	calls         int
	byEnv         map[string]dependencies.EnvValues
	result        *dependencies.ProvisionResult
	err           error
	deprovisioned []string

	// AuthorWithSecretRef spies (the build path's no-SM-write author half).
	authorRefCalls int
	authorByEnv    map[string]dependencies.PreparedEnvValues
	authorResult   *dependencies.ProvisionResult
	authorErr      error
}

func (f *fakeExtProv) Provision(_ context.Context, _, _, _ string, _ *dependencies.ExternalResource, byEnv map[string]dependencies.EnvValues) (*dependencies.ProvisionResult, error) {
	f.calls++
	f.byEnv = byEnv
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &dependencies.ProvisionResult{ResourceName: "o-ext", BindingByEnv: map[string]string{"development": "o-ext-development"}}, nil
}
func (f *fakeExtProv) AuthorWithSecretRef(_ context.Context, _, _ string, _ *dependencies.ExternalResource, byEnv map[string]dependencies.PreparedEnvValues) (*dependencies.ProvisionResult, error) {
	f.authorRefCalls++
	f.authorByEnv = byEnv
	if f.authorErr != nil {
		return nil, f.authorErr
	}
	if f.authorResult != nil {
		return f.authorResult, nil
	}
	return &dependencies.ProvisionResult{ResourceName: "o-ext", BindingByEnv: map[string]string{"development": "o-ext-development"}}, nil
}
func (f *fakeExtProv) Deprovision(_ context.Context, _, _, name string, _ []string) error {
	f.deprovisioned = append(f.deprovisioned, name)
	return nil
}
func (f *fakeExtProv) ResolveRunnerSecrets(_ context.Context, _, _, _ string, names []string) ([]dependencies.ExternalResourceRunnerSecret, error) {
	out := make([]dependencies.ExternalResourceRunnerSecret, 0, len(names))
	for _, n := range names {
		out = append(out, dependencies.ExternalResourceRunnerSecret{KVPath: "vault/" + n, Keys: []string{"API_KEY"}})
	}
	return out, nil
}

type fakePlatProv struct {
	calls         int
	params        map[string]any
	result        *dependencies.PlatformProvisionResult
	err           error
	deprovisioned []string
}

func (f *fakePlatProv) Provision(_ context.Context, _, _, depName, _ string, params map[string]any, _ []string) (*dependencies.PlatformProvisionResult, error) {
	f.calls++
	f.params = params
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &dependencies.PlatformProvisionResult{ResourceName: "o-" + depName, BindingByEnv: map[string]string{"development": "o-" + depName + "-development"}}, nil
}
func (f *fakePlatProv) Deprovision(_ context.Context, _, _, depName string, _ []string) error {
	f.deprovisioned = append(f.deprovisioned, depName)
	return nil
}

type fakeBindings struct {
	byName map[string]*openchoreo.ResourceReleaseBinding
}

func (f *fakeBindings) GetBinding(_ context.Context, _, name string) (*openchoreo.ResourceReleaseBinding, error) {
	return f.byName[name], nil
}

type fakeProjects struct{ refs []ProjectRef }

func (f fakeProjects) ListProjects(_ context.Context, orgID string) ([]ProjectRef, error) {
	var out []ProjectRef
	for _, r := range f.refs {
		if r.OrgID == orgID {
			out = append(out, r)
		}
	}
	return out, nil
}

type fakeProviders struct {
	byName map[string]openchoreo.WorkloadEndpointInfo
}

func (f fakeProviders) FindByComponent(_ context.Context, _, name string) (openchoreo.WorkloadEndpointInfo, bool, error) {
	ep, ok := f.byName[name]
	return ep, ok, nil
}

type fakeAccess struct {
	rows   []*dependencies.AccessRequest
	nextID int
}

func (f *fakeAccess) Create(_ context.Context, ar *dependencies.AccessRequest) error {
	f.nextID++
	ar.ID = fmt.Sprintf("ar-%d", f.nextID)
	f.rows = append(f.rows, ar)
	return nil
}
func (f *fakeAccess) ListByConsumerProject(_ context.Context, orgID, projectID string) ([]dependencies.AccessRequest, error) {
	var out []dependencies.AccessRequest
	for _, r := range f.rows {
		if r.OrgID == orgID && r.ConsumerProjectID == projectID {
			out = append(out, *r)
		}
	}
	return out, nil
}
func (f *fakeAccess) FindOpenForTarget(_ context.Context, orgID, providerProjectID, providerComponent string) (*dependencies.AccessRequest, error) {
	for _, r := range f.rows {
		if r.OrgID == orgID && r.ProviderProjectID == providerProjectID && r.ProviderComponentName == providerComponent &&
			r.Status != dependencies.AccessRequestStatusGranted && r.Status != dependencies.AccessRequestStatusRejected {
			return r, nil
		}
	}
	return nil, nil
}
func (f *fakeAccess) UpdateStatus(_ context.Context, id, status string) error {
	for _, r := range f.rows {
		if r.ID == id {
			r.Status = status
		}
	}
	return nil
}
func (f *fakeAccess) ListByProviderTask(_ context.Context, providerTaskID string) ([]dependencies.AccessRequest, error) {
	var out []dependencies.AccessRequest
	for _, r := range f.rows {
		if r.ProviderTaskID == providerTaskID {
			out = append(out, *r)
		}
	}
	return out, nil
}

func TestOnIssueClosed_RejectsUngrantedRidersOnDecline(t *testing.T) {
	access := &fakeAccess{}
	ctx := context.Background()
	// Two consumers rode the same warehouse#12 org-publish gate issue: one still
	// pending, one already granted (an earlier partial grant).
	_ = access.Create(ctx, &dependencies.AccessRequest{OrgID: "acme", ProviderProjectID: "warehouse",
		ProviderTaskID: providerTaskKey("warehouse", 12), Status: dependencies.AccessRequestStatusRequested})
	_ = access.Create(ctx, &dependencies.AccessRequest{OrgID: "acme", ProviderProjectID: "warehouse",
		ProviderTaskID: providerTaskKey("warehouse", 12), Status: dependencies.AccessRequestStatusGranted})
	svc := NewService(Deps{Access: access, Repos: fakeRepos{}})

	// Provider manually closes the gate issue → decline.
	payload := []byte(`{"issue":{"number":12},"repository":{"full_name":"asdlc-repos/warehouse"}}`)
	if err := svc.OnIssueClosed(ctx, "issues", "closed", payload); err != nil {
		t.Fatalf("OnIssueClosed: %v", err)
	}
	if access.rows[0].Status != dependencies.AccessRequestStatusRejected {
		t.Fatalf("pending rider must flip to rejected, got %q", access.rows[0].Status)
	}
	if access.rows[1].Status != dependencies.AccessRequestStatusGranted {
		t.Fatalf("already-granted rider must stay granted, got %q", access.rows[1].Status)
	}
}

func TestOnIssueClosed_NonOrgPublishIssueIsNoop(t *testing.T) {
	access := &fakeAccess{}
	svc := NewService(Deps{Access: access, Repos: fakeRepos{}})
	// No access request keys to issue #99 (a routine coding-issue close) → no-op.
	payload := []byte(`{"issue":{"number":99},"repository":{"full_name":"asdlc-repos/warehouse"}}`)
	if err := svc.OnIssueClosed(context.Background(), "issues", "closed", payload); err != nil {
		t.Fatalf("OnIssueClosed on a non-org-publish issue must be a silent no-op, got %v", err)
	}
}

func readyBinding(outputs ...string) *openchoreo.ResourceReleaseBinding {
	st := &openchoreo.ResourceReleaseBindingStatus{
		Conditions: []openchoreo.OCCondition{{Type: "Ready", Status: "True"}},
	}
	for _, o := range outputs {
		st.Outputs = append(st.Outputs, openchoreo.ResolvedOutput{Name: o})
	}
	return &openchoreo.ResourceReleaseBinding{Status: st}
}

// ---- fixtures --------------------------------------------------------------

func designWithDeps() []spec.DesignComponent {
	return []spec.DesignComponent{{
		Name: "orders",
		Dependencies: []spec.Dependency{
			{Kind: spec.DependencyKindExternal, Name: "stripe", Config: []spec.ConfigKey{
				{Key: "api_key", Secret: true}, {Key: "region"},
			}},
			{Kind: spec.DependencyKindPlatformResource, Name: "orders-db", ResourceType: "postgres-cnpg", Parameters: map[string]any{"size": "small"}},
		},
	}}
}

func newTestService(issues *fakeIssues, execs *fakeExecStore, reeval Reevaluator, design DesignReader, catalog *fakeCatalog, ext *fakeExtProv, plat *fakePlatProv, bindings *fakeBindings) *Service {
	return NewService(Deps{
		Issues:   issues,
		Execs:    execs,
		Reeval:   reeval,
		Design:   design,
		Repos:    fakeRepos{},
		Catalog:  catalog,
		ExtProv:  ext,
		PlatProv: plat,
		Bindings: bindings,
	})
}

// ---- tests -----------------------------------------------------------------

func TestEnsureProvisionIssues_MintsPerDepDeduped(t *testing.T) {
	issues := newFakeIssues(nil)
	svc := newTestService(issues, &fakeExecStore{}, &fakeReeval{}, fakeDesign{comps: designWithDeps()}, &fakeCatalog{}, &fakeExtProv{}, &fakePlatProv{}, &fakeBindings{})

	gateByDep, err := svc.EnsureProvisionIssues(context.Background(), "org", "proj", "v1-1")
	if err != nil {
		t.Fatalf("EnsureProvisionIssues: %v", err)
	}
	if len(issues.created) != 2 {
		t.Fatalf("want 2 gate issues (stripe + orders-db), got %d", len(issues.created))
	}
	// The returned map carries the minted gate number for each distinct dep — the
	// read-your-write the build path threads past the racy list.
	if gateByDep["stripe"] == 0 || gateByDep["orders-db"] == 0 {
		t.Fatalf("EnsureProvisionIssues must return the minted gate number per dep, got %+v", gateByDep)
	}
	// Every gate issue carries the provision class label + a GateKind block field.
	var haveConfig, haveResource bool
	for _, req := range issues.created {
		if !contains(req.Labels, taskmeta.LabelProvision) {
			t.Errorf("gate issue missing aep:provision label: %v", req.Labels)
		}
		block, err := taskmeta.ParseBlock(req.Body)
		if err != nil {
			t.Fatalf("gate issue body has no block: %v", err)
		}
		switch block.GateKind {
		case taskmeta.GateConfigCollection:
			haveConfig = true
		case taskmeta.GateResourceProvisioning:
			haveResource = true
		default:
			t.Errorf("unexpected gateKind %q", block.GateKind)
		}
	}
	if !haveConfig || !haveResource {
		t.Fatalf("want both config-collection + resource-provisioning gate kinds")
	}

	// Idempotent: a second call mints nothing new (the deps already have open issues).
	issues.created = nil
	gateByDep2, err := svc.EnsureProvisionIssues(context.Background(), "org", "proj", "v1-1")
	if err != nil {
		t.Fatalf("EnsureProvisionIssues #2: %v", err)
	}
	if len(issues.created) != 0 {
		t.Fatalf("second mint must be a no-op, created %d", len(issues.created))
	}
	// The map still resolves the pre-existing open gates (from openProvisionDeps).
	if gateByDep2["stripe"] == 0 || gateByDep2["orders-db"] == 0 {
		t.Fatalf("second call must still return the existing gate numbers, got %+v", gateByDep2)
	}
}

func TestSaveValues_ProvisionsAndClosesGate(t *testing.T) {
	gate := provisionGateIssue(10, "stripe", taskmeta.GateConfigCollection)
	issues := newFakeIssues([]sourcecontrol.IssueInfo{gate})
	execs := &fakeExecStore{}
	reeval := &fakeReeval{}
	ext := &fakeExtProv{}
	catalog := &fakeCatalog{entries: map[string]*dependencies.ExternalResource{
		"stripe": {Name: "stripe", ConfigKeys: []spec.ConfigKey{{Key: "api_key", Secret: true}, {Key: "region"}}},
	}}
	svc := newTestService(issues, execs, reeval, fakeDesign{comps: designWithDeps()}, catalog, ext, &fakePlatProv{}, &fakeBindings{})

	err := svc.SaveValues(context.Background(), "org", "org", "proj", "stripe", map[string]map[string]string{
		"development": {"api_key": "sk_live_x", "region": "us"},
	})
	if err != nil {
		t.Fatalf("SaveValues: %v", err)
	}
	if ext.calls != 1 {
		t.Fatalf("external provisioner must be called once, got %d", ext.calls)
	}
	// Values split by schema: api_key → secret, region → plain. No secret leaks
	// into the plain map.
	ev := ext.byEnv["development"]
	if ev.Secret["api_key"] != "sk_live_x" || ev.Plain["region"] != "us" {
		t.Fatalf("split-by-schema wrong: %+v", ev)
	}
	if _, leaked := ev.Plain["api_key"]; leaked {
		t.Fatalf("secret api_key leaked into the plain map")
	}
	// The gate issue is closed and the funnel re-evaluated (consumers release).
	if _, closed := issues.closed[10]; !closed {
		t.Fatalf("gate issue must be closed after external provisioning")
	}
	if reeval.calls != 1 {
		t.Fatalf("Reevaluate must be called once, got %d", reeval.calls)
	}
	// The provision Execution derives deployed (succeeded provision run).
	if r := latestProvisionRow(execs); r == nil || r.Status != string(taskmeta.ExecSucceeded) {
		t.Fatalf("want a succeeded provision run, got %+v", r)
	}
	// No secret value appears in the close comment.
	if strings.Contains(issues.closed[10], "sk_live_x") {
		t.Fatalf("secret leaked into the close comment: %q", issues.closed[10])
	}
}

func TestProvision_PlatformIsAsync_LeftRunning(t *testing.T) {
	gate := provisionGateIssue(11, "orders-db", taskmeta.GateResourceProvisioning)
	issues := newFakeIssues([]sourcecontrol.IssueInfo{gate})
	execs := &fakeExecStore{}
	reeval := &fakeReeval{}
	plat := &fakePlatProv{}
	svc := newTestService(issues, execs, reeval, fakeDesign{comps: designWithDeps()}, &fakeCatalog{}, &fakeExtProv{}, plat, &fakeBindings{})

	if err := svc.Provision(context.Background(), "org", "proj", "orders-db", nil, nil); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if plat.calls != 1 {
		t.Fatalf("platform provisioner must be called once, got %d", plat.calls)
	}
	// Design parameters flow through (size=small).
	if plat.params["size"] != "small" {
		t.Fatalf("design params must reach the provisioner, got %+v", plat.params)
	}
	// Async: the run is RUNNING (not finished) and the gate stays OPEN — the
	// watcher completes it.
	r := latestProvisionRow(execs)
	if r == nil || r.Status != string(taskmeta.ExecRunning) {
		t.Fatalf("platform provision run must be left running, got %+v", r)
	}
	if r.RunName != "o-orders-db-development" {
		t.Fatalf("run must pin the development binding name, got %q", r.RunName)
	}
	if _, closed := issues.closed[11]; closed {
		t.Fatalf("gate issue must stay open until the binding is ready")
	}
	if reeval.calls != 0 {
		t.Fatalf("no reevaluate before readiness, got %d", reeval.calls)
	}
}

func TestResourceWatcher_ReadyClosesGateAndReleases(t *testing.T) {
	gate := provisionGateIssue(11, "orders-db", taskmeta.GateResourceProvisioning)
	issues := newFakeIssues([]sourcecontrol.IssueInfo{gate})
	execs := &fakeExecStore{}
	reeval := &fakeReeval{}
	bindings := &fakeBindings{byName: map[string]*openchoreo.ResourceReleaseBinding{}}
	plat := &fakePlatProv{}
	svc := newTestService(issues, execs, reeval, fakeDesign{comps: designWithDeps()}, &fakeCatalog{}, &fakeExtProv{}, plat, bindings)

	// Provision (async) → running row pinned to the binding.
	if err := svc.Provision(context.Background(), "org", "proj", "orders-db", nil, nil); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	w := NewResourceWatcher(svc, nil, time.Second)
	// Pin the clock near StartedAt (Unix 1000) so the stale bound does not fire.
	w.now = func() time.Time { return time.Unix(1000, 0).Add(time.Minute) }

	// Binding not ready yet → watcher waits (run stays running, gate open).
	bindings.byName["o-orders-db-development"] = &openchoreo.ResourceReleaseBinding{}
	if err := w.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep (not ready): %v", err)
	}
	if r := latestProvisionRow(execs); r.Status != string(taskmeta.ExecRunning) {
		t.Fatalf("run must stay running while binding not ready, got %s", r.Status)
	}

	// Binding goes Ready → watcher finishes the run, closes the gate, reevaluates.
	bindings.byName["o-orders-db-development"] = readyBinding("host", "port")
	if err := w.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep (ready): %v", err)
	}
	if r := latestProvisionRow(execs); r == nil || r.Status != string(taskmeta.ExecSucceeded) {
		t.Fatalf("ready binding must finish the run succeeded, got %+v", r)
	}
	if _, closed := issues.closed[11]; !closed {
		t.Fatalf("gate issue must be closed once the binding is ready")
	}
	if reeval.calls != 1 {
		t.Fatalf("Reevaluate must fire once on readiness, got %d", reeval.calls)
	}
}

func TestResourceWatcher_StaleFails(t *testing.T) {
	gate := provisionGateIssue(11, "orders-db", taskmeta.GateResourceProvisioning)
	issues := newFakeIssues([]sourcecontrol.IssueInfo{gate})
	execs := &fakeExecStore{}
	bindings := &fakeBindings{byName: map[string]*openchoreo.ResourceReleaseBinding{"o-orders-db-development": {}}}
	svc := newTestService(issues, execs, &fakeReeval{}, fakeDesign{comps: designWithDeps()}, &fakeCatalog{}, &fakeExtProv{}, &fakePlatProv{}, bindings)
	if err := svc.Provision(context.Background(), "org", "proj", "orders-db", nil, nil); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	w := NewResourceWatcher(svc, nil, time.Second)
	// Jump the clock past the stale window (StartedAt was stamped at Unix 1000).
	w.now = func() time.Time { return time.Unix(1000, 0).Add(31 * time.Minute) }
	if err := w.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if r := latestProvisionRow(execs); r == nil || r.Status != string(taskmeta.ExecFailed) {
		t.Fatalf("a stale provision run must be failed, got %+v", r)
	}
}

func TestStatus_MasksOutputsToNames(t *testing.T) {
	bindings := &fakeBindings{byName: map[string]*openchoreo.ResourceReleaseBinding{
		"proj-orders-db-development": readyBinding("host", "port"),
	}}
	svc := newTestService(newFakeIssues(nil), &fakeExecStore{}, &fakeReeval{}, fakeDesign{}, &fakeCatalog{}, &fakeExtProv{}, &fakePlatProv{}, bindings)
	st, err := svc.Status(context.Background(), "org", "proj", "orders-db", "")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Ready || st.Status != "ready" {
		t.Fatalf("want ready, got %+v", st)
	}
	if len(st.Outputs) != 2 || st.Outputs[0] != "host" {
		t.Fatalf("outputs must be the masked names, got %+v", st.Outputs)
	}
}

func TestDeleteExternalResource_InUse409(t *testing.T) {
	// Consumers are scanned from committed designs (component_tasks is gone):
	// project "proj" component "orders" declares external dep "stripe".
	catalog := &fakeCatalog{}
	svc := NewService(Deps{
		Issues:   newFakeIssues(nil),
		Execs:    &fakeExecStore{},
		Design:   fakeDesign{comps: designWithDeps()},
		Repos:    fakeRepos{},
		Catalog:  catalog,
		Projects: fakeProjects{refs: []ProjectRef{{OrgID: "org", ProjectID: "proj"}}},
	})
	if err := svc.DeleteExternalResource(context.Background(), "org", "stripe"); err != ErrExternalResourceInUse {
		t.Fatalf("in-use delete must return ErrExternalResourceInUse, got %v", err)
	}
	if len(catalog.deleted) != 0 {
		t.Fatalf("an in-use resource must not be deleted")
	}
	// No consumers → delete proceeds.
	if err := svc.DeleteExternalResource(context.Background(), "org", "unused"); err != nil {
		t.Fatalf("delete of unused resource: %v", err)
	}
	if len(catalog.deleted) != 1 || catalog.deleted[0] != "unused" {
		t.Fatalf("unused resource must be deleted, got %v", catalog.deleted)
	}
}

func TestRequestAccess_CreatesRequestAndProviderIssue(t *testing.T) {
	consumer := spec.DesignComponent{Name: "web", Dependencies: []spec.Dependency{
		{Kind: spec.DependencyKindOrgService, Name: "inventory"},
	}}
	issues := newFakeIssues(nil)
	access := &fakeAccess{}
	providers := fakeProviders{byName: map[string]openchoreo.WorkloadEndpointInfo{
		"inventory": {Project: "warehouse", Component: "warehouse-inventory", Name: "http"},
	}}
	svc := NewService(Deps{
		Issues:    issues,
		Execs:     &fakeExecStore{},
		Design:    fakeDesign{comps: []spec.DesignComponent{consumer}},
		Repos:     fakeRepos{},
		Access:    access,
		Providers: providers,
	})

	ar, err := svc.RequestAccess(context.Background(), "org", "storefront", "web", "inventory")
	if err != nil {
		t.Fatalf("RequestAccess: %v", err)
	}
	if ar.Status != dependencies.AccessRequestStatusRequested {
		t.Fatalf("new access request must be 'requested', got %q", ar.Status)
	}
	if ar.ProviderProjectID != "warehouse" || ar.ProviderComponentName != "inventory" {
		t.Fatalf("provider resolution wrong: %+v", ar)
	}
	// A provider-side org-publish gate issue was created on the provider project.
	if len(issues.created) != 1 {
		t.Fatalf("want one provider org-publish issue, got %d", len(issues.created))
	}
	block, _ := taskmeta.ParseBlock(issues.created[0].Body)
	if block.GateKind != taskmeta.GateOrgPublish || block.Component != "inventory" {
		t.Fatalf("org-publish issue block wrong: %+v", block)
	}
	if ar.ProviderIssueNumber == 0 {
		t.Fatalf("access request must link the provider issue number")
	}

	// A second consumer of the same provider rides the SAME issue (dedup).
	issues.created = nil
	ar2, err := svc.RequestAccess(context.Background(), "org", "another", "api", "inventory")
	if err != nil {
		t.Fatalf("RequestAccess #2: %v", err)
	}
	if len(issues.created) != 0 {
		t.Fatalf("second consumer must ride the existing issue, created %d", len(issues.created))
	}
	if ar2.ProviderIssueNumber != ar.ProviderIssueNumber {
		t.Fatalf("second request must reference the same provider issue")
	}
}

func TestGrant_OnProviderDeploy(t *testing.T) {
	issues := newFakeIssues([]sourcecontrol.IssueInfo{{Number: 20, State: "open", Title: "publish"}})
	access := &fakeAccess{}
	// Two riders on the same provider issue, both pending.
	pk := providerTaskKey("warehouse", 20)
	_ = access.Create(context.Background(), &dependencies.AccessRequest{
		OrgID: "org", ConsumerProjectID: "storefront", ProviderProjectID: "warehouse",
		ProviderComponentName: "inventory", ProviderTaskID: pk, ProviderIssueNumber: 20,
		Status: dependencies.AccessRequestStatusRequested,
	})
	_ = access.Create(context.Background(), &dependencies.AccessRequest{
		OrgID: "org", ConsumerProjectID: "another", ProviderProjectID: "warehouse",
		ProviderComponentName: "inventory", ProviderTaskID: pk, ProviderIssueNumber: 20,
		Status: dependencies.AccessRequestStatusRequested,
	})
	svc := NewService(Deps{Issues: issues, Execs: &fakeExecStore{}, Access: access, Repos: fakeRepos{}})

	// The provider component deploys → grant all riders + close the issue.
	if err := svc.OnComponentDeployed(context.Background(), "org", "warehouse", "inventory"); err != nil {
		t.Fatalf("OnComponentDeployed: %v", err)
	}
	for _, r := range access.rows {
		if r.Status != dependencies.AccessRequestStatusGranted {
			t.Fatalf("all riders must be granted, got %q", r.Status)
		}
	}
	if _, closed := issues.closed[20]; !closed {
		t.Fatalf("provider org-publish issue must be closed on grant")
	}
	// A deploy with no pending access is a no-op (does not error).
	if err := svc.OnComponentDeployed(context.Background(), "org", "warehouse", "other"); err != nil {
		t.Fatalf("no-op deploy grant: %v", err)
	}
}

func TestResolveComponentRunnerSecrets(t *testing.T) {
	ext := &fakeExtProv{}
	svc := NewService(Deps{
		Issues:  newFakeIssues(nil),
		Execs:   &fakeExecStore{},
		Design:  fakeDesign{comps: designWithDeps()}, // orders has external "stripe"
		Repos:   fakeRepos{},
		ExtProv: ext,
	})
	srs, err := svc.ResolveComponentRunnerSecrets(context.Background(), "org", "proj", "orders", "")
	if err != nil {
		t.Fatalf("ResolveComponentRunnerSecrets: %v", err)
	}
	// Only the external dep (stripe) yields a runner secret; the platform-resource
	// dep (orders-db) does not.
	if len(srs) != 1 || srs[0].KVPath != "vault/stripe" {
		t.Fatalf("want one runner secret for stripe, got %+v", srs)
	}
	// A component with no external deps yields none.
	none, err := svc.ResolveComponentRunnerSecrets(context.Background(), "org", "proj", "nonexistent", "")
	if err != nil || len(none) != 0 {
		t.Fatalf("unknown component: want no secrets, got %+v (err %v)", none, err)
	}
}

func TestDeprovisionProject_TearsDownResources(t *testing.T) {
	ext := &fakeExtProv{}
	plat := &fakePlatProv{}
	svc := NewService(Deps{
		Issues:   newFakeIssues(nil),
		Execs:    &fakeExecStore{},
		Design:   fakeDesign{comps: designWithDeps()},
		Repos:    fakeRepos{},
		ExtProv:  ext,
		PlatProv: plat,
	})
	if err := svc.DeprovisionProject(context.Background(), "org", "proj"); err != nil {
		t.Fatalf("DeprovisionProject: %v", err)
	}
	if len(ext.deprovisioned) != 1 || ext.deprovisioned[0] != "stripe" {
		t.Fatalf("external dep must be deprovisioned, got %v", ext.deprovisioned)
	}
	if len(plat.deprovisioned) != 1 || plat.deprovisioned[0] != "orders-db" {
		t.Fatalf("platform-resource dep must be deprovisioned, got %v", plat.deprovisioned)
	}
}

func TestSaveValues_WrongKind400(t *testing.T) {
	// stripe is external; asking to provision it as a platform resource is wrong-kind.
	svc := newTestService(newFakeIssues(nil), &fakeExecStore{}, &fakeReeval{}, fakeDesign{comps: designWithDeps()}, &fakeCatalog{}, &fakeExtProv{}, &fakePlatProv{}, &fakeBindings{})
	err := svc.Provision(context.Background(), "org", "proj", "stripe", nil, nil)
	if err == nil || !strings.Contains(err.Error(), dependencies.ErrDepWrongKind.Error()) {
		t.Fatalf("provisioning an external dep as a resource must be wrong-kind, got %v", err)
	}
}

// ---- helpers ---------------------------------------------------------------

func provisionGateIssue(number int, depName, gateKind string) sourcecontrol.IssueInfo {
	block := taskmeta.Block{Component: depName, GateKind: gateKind, Origin: taskmeta.OriginSpecPlan, DesignTag: "v1-1"}
	return sourcecontrol.IssueInfo{
		Number: number,
		Title:  "Provision " + depName,
		Body:   taskmeta.ComposeBody(block, taskmeta.Human{Rationale: "r"}),
		State:  "open",
		Labels: taskmeta.NewTaskLabels(taskmeta.ClassProvision, taskmeta.OriginSpecPlan),
	}
}

func latestProvisionRow(execs *fakeExecStore) *delivery.Execution {
	var last *delivery.Execution
	for _, r := range execs.rows {
		if r.Kind == string(taskmeta.KindProvision) {
			last = r
		}
	}
	return last
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
