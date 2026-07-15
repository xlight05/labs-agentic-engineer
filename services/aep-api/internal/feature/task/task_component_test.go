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

// Component tier for the task read surface: the REAL contract-first strict
// handler (via componenttest, tenant gate in ENFORCE) over list-tasks /
// get-task, with only the out-of-process edges faked (GitHub issues,
// executions rows). Proves the HTTP contract end to end — derived-status
// shapes, the 404 miss, and the no-claims 401 the API-surface guard exists
// for. The command/plan HTTP operations the retired Huma surface carried
// (plan-tasks, execute-task, hold-task, unhold-task, promote-task-from-issue)
// are not in the committed contract, so their route tests are gone; the
// one-active-plan-turn invariant keeps a service-level test below
// (PlanService still backs the devflow validator).
package task_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/api"
	"github.com/wso2/aep/aep-api/internal/clients/agentsvc"
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/credentials"
	"github.com/wso2/aep/aep-api/internal/feature/artifacts"
	"github.com/wso2/aep/aep-api/internal/feature/execution"
	"github.com/wso2/aep/aep-api/internal/feature/gitrepo"
	"github.com/wso2/aep/aep-api/internal/feature/task"
	"github.com/wso2/aep/aep-api/internal/platform/componenttest"
	"github.com/wso2/aep/aep-api/internal/platform/gitfs/workspacetest"
	"github.com/wso2/aep/aep-api/internal/platform/gittest"
	"github.com/wso2/aep/aep-api/models"
)

const (
	org   = "acme"
	proj  = "widgets"
	tasks = "/api/v1/projects/" + proj + "/tasks"
)

// ---- faked edges -----------------------------------------------------------

type fakeIssues struct {
	mu    sync.Mutex
	byNum map[int]*gitrepo.IssueInfo
}

func newIssues(seed ...gitrepo.IssueInfo) *fakeIssues {
	f := &fakeIssues{byNum: map[int]*gitrepo.IssueInfo{}}
	for i := range seed {
		cp := seed[i]
		f.byNum[cp.Number] = &cp
	}
	return f
}
func (f *fakeIssues) CreateIssue(context.Context, string, string, gitrepo.CreateIssueRequest) (*gitrepo.IssueResult, error) {
	return &gitrepo.IssueResult{Number: 999, URL: "u"}, nil
}
func (f *fakeIssues) ListIssues(_ context.Context, _, _ string, want []string) ([]gitrepo.IssueInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []gitrepo.IssueInfo
	for _, i := range f.byNum {
		if hasAll(i.Labels, want) {
			out = append(out, *i)
		}
	}
	return out, nil
}
func (f *fakeIssues) GetIssue(_ context.Context, _, _ string, n int) (*gitrepo.IssueInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.byNum[n]; i != nil {
		cp := *i
		return &cp, nil
	}
	return nil, gitrepo.ErrIssueNotFound
}
func (f *fakeIssues) CommentIssue(context.Context, string, string, int, string) error { return nil }
func (f *fakeIssues) EditIssueBody(context.Context, string, string, int, string) error {
	return nil
}
func (f *fakeIssues) EditIssueTitle(context.Context, string, string, int, string) error { return nil }
func (f *fakeIssues) AddLabels(_ context.Context, _, _ string, n int, labels []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.byNum[n]; i != nil {
		i.Labels = append(i.Labels, labels...)
	}
	return nil
}
func (f *fakeIssues) RemoveLabel(context.Context, string, string, int, string) error { return nil }

type fakeRepos struct{}

func (fakeRepos) GetRepo(context.Context, string, string) (*models.GitRepository, error) {
	return &models.GitRepository{OrgID: org, ProjectID: proj, RepoURL: "https://github.com/acme/widgets"}, nil
}

type fakeExecs struct {
	latest  map[int]map[string]*models.Execution
	history map[int][]models.Execution
}

func (f fakeExecs) LatestPerKindScoped(_ context.Context, _, _ string, n int) (map[string]*models.Execution, error) {
	if m := f.latest[n]; m != nil {
		return m, nil
	}
	return map[string]*models.Execution{}, nil
}
func (f fakeExecs) LatestPerKindForRepoScoped(_ context.Context, _, _ string) (map[int]map[string]*models.Execution, error) {
	if f.latest != nil {
		return f.latest, nil
	}
	return map[int]map[string]*models.Execution{}, nil
}
func (f fakeExecs) ListByIssueScoped(_ context.Context, _, _ string, n int) ([]models.Execution, error) {
	return f.history[n], nil
}

// fakeVersions drives the plan versioned-spec gate.
type fakeVersions struct {
	spec []artifacts.RequirementsVersionInfo
}

func (f fakeVersions) ListRequirementsVersions(context.Context, string, string) ([]artifacts.RequirementsVersionInfo, error) {
	return f.spec, nil
}
func (f fakeVersions) LatestSpecTag(context.Context, string, string) string {
	if len(f.spec) == 0 {
		return ""
	}
	return f.spec[0].Tag
}
func (f fakeVersions) GetRequirementsAtTag(context.Context, string, string, string) (map[string]string, error) {
	return nil, nil
}

func hasAll(have, want []string) bool {
	set := map[string]bool{}
	for _, l := range have {
		set[l] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func taskIssue(number int, component, state string, extra ...string) gitrepo.IssueInfo {
	block := taskmeta.Block{Component: component, Origin: taskmeta.OriginSpecPlan, SpecTag: "req-v1", DesignTag: "design-v1"}
	body := taskmeta.ComposeBody(block, taskmeta.Human{Rationale: "r", Body: "## Scope"})
	labels := append(taskmeta.NewTaskLabels(taskmeta.ClassCoding, taskmeta.OriginSpecPlan), extra...)
	return gitrepo.IssueInfo{Number: number, Title: "Implement " + component, Body: body, State: state, URL: "https://github.com/acme/widgets/issues/1", Labels: labels}
}

// ---- harness ---------------------------------------------------------------

func newRig(t *testing.T, iss *fakeIssues, execs fakeExecs) *componenttest.Harness {
	t.Helper()
	reads := task.NewReads(iss, fakeRepos{}, execs, nil, nil)
	return componenttest.New(t, componenttest.Options{Deps: api.Deps{TaskReads: reads}})
}

// ---- tests -----------------------------------------------------------------

func TestList_DerivesStatusShapes(t *testing.T) {
	iss := newIssues(
		taskIssue(1, "user-service", "open"),
		taskIssue(2, "order-service", "open"),
	)
	execs := fakeExecs{latest: map[int]map[string]*models.Execution{
		1: {string(taskmeta.KindCoding): row("c1", taskmeta.KindCoding, taskmeta.ExecSucceeded, "", -2), string(taskmeta.KindBuild): row("b1", taskmeta.KindBuild, taskmeta.ExecSucceeded, "", -1)},
		2: {string(taskmeta.KindCoding): row("c2", taskmeta.KindCoding, taskmeta.ExecRunning, "", 0)},
	}}
	h := newRig(t, iss, execs)

	rec := h.AsOrg(org).Get(tasks + "?state=open")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: code %d (%s)", rec.Code, rec.Body.String())
	}
	var views []task.TaskView
	_ = json.Unmarshal(rec.Body.Bytes(), &views)
	byNum := map[int]task.TaskView{}
	for _, v := range views {
		byNum[v.IssueNumber] = v
	}
	if byNum[1].DerivedStatus != string(taskmeta.StatusDeployed) {
		t.Errorf("task 1 = %q, want deployed", byNum[1].DerivedStatus)
	}
	if byNum[2].DerivedStatus != string(taskmeta.StatusInProgress) {
		t.Errorf("task 2 = %q, want in_progress", byNum[2].DerivedStatus)
	}
	if byNum[1].Component != "user-service" || byNum[1].ExecutorClass != "coding" {
		t.Errorf("task 1 shape wrong: %+v", byNum[1])
	}
}

func TestGet_IncludesHistory(t *testing.T) {
	iss := newIssues(taskIssue(5, "order-service", "open"))
	execs := fakeExecs{
		latest:  map[int]map[string]*models.Execution{5: {string(taskmeta.KindCoding): row("b", taskmeta.KindCoding, taskmeta.ExecSucceeded, "", 0)}},
		history: map[int][]models.Execution{5: {*row("a", taskmeta.KindCoding, taskmeta.ExecFailed, "", -1), *row("b", taskmeta.KindCoding, taskmeta.ExecSucceeded, "", 0)}},
	}
	h := newRig(t, iss, execs)

	rec := h.AsOrg(org).Get(tasks + "/5")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: code %d (%s)", rec.Code, rec.Body.String())
	}
	var d task.TaskDetail
	_ = json.Unmarshal(rec.Body.Bytes(), &d)
	if len(d.ExecutionHistory) != 2 || d.DerivedStatus != string(taskmeta.StatusReadyForReview) {
		t.Fatalf("get shape wrong: status=%q history=%d", d.DerivedStatus, len(d.ExecutionHistory))
	}

	miss := h.AsOrg(org).Get(tasks + "/404")
	if miss.Code != http.StatusNotFound {
		t.Errorf("missing task: code %d, want 404", miss.Code)
	}
	if e := componenttest.DecodeEnvelope(t, miss.Body.String()); e.Code != "not_found" {
		t.Errorf("missing task envelope code = %q, want not_found", e.Code)
	}
}

func TestPlan_InProgress_409(t *testing.T) {
	// The one-active-plan-turn invariant (§6): while a plan turn holds the
	// per-project in-flight lock (blocked in the upstream Turn), a second
	// StartPlan for the same project must be rejected with ErrPlanInProgress.
	// plan-tasks left the public HTTP contract, but PlanService still backs the
	// devflow validator, so the invariant is asserted at the service seam. The
	// plan dispatch is workspace-shaped, so the rig runs a real engine over
	// real file:// origins.
	iss := newIssues()
	bt := &blockingTurn{started: make(chan struct{}), release: make(chan struct{})}
	fx := workspacetest.New(t, map[string]string{"specs/design/design.md": "# d"})
	skillsOrigin := gittest.NewRemote(t, gittest.WithSeed(map[string]string{
		"skills/task-planning/SKILL.md": "---\nname: task-planning\nmetadata:\n  aep:\n    kind: platform\n---\nbody",
	}, "seed"))
	repoRow := &models.GitRepository{OrgID: org, ProjectID: proj, RepoURL: fx.Origin.URL(),
		DefaultBranch: "main", RepoSlug: workspacetest.DefaultSlug, Status: "ready"}
	skillsRow := &models.GitRepository{OrgID: org, ProjectID: models.SkillsRepoSentinelProjectID,
		RepoURL: skillsOrigin.URL(), DefaultBranch: "main", RepoSlug: "org-skills", Status: "ready"}
	git := gitrepo.NewGitOpsService(nilCredResolver{}, fx.Engine)
	plan := task.NewPlanService(fixedRepos{repo: repoRow},
		fakeVersions{spec: []artifacts.RequirementsVersionInfo{{Tag: "v1"}}}, git,
		func(context.Context, string) (string, error) { return "sk-key", nil }, bt, iss, fx.Engine,
		func(context.Context, string) (*models.GitRepository, error) { return skillsRow, nil })

	firstErr := make(chan error, 1)
	go func() {
		_, err := plan.StartPlan(context.Background(), org, proj)
		firstErr <- err
	}()
	select {
	case <-bt.started: // the first turn now holds the in-flight lock
	case err := <-firstErr:
		t.Fatalf("first plan failed before dispatch: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("first plan never reached the turn dispatch")
	}

	if _, err := plan.StartPlan(context.Background(), org, proj); !errors.Is(err, task.ErrPlanInProgress) {
		t.Fatalf("second concurrent plan: err = %v, want ErrPlanInProgress", err)
	}
	close(bt.release)
}

// fixedRepos serves one fixed row (the workspace-backed plan rig's repo).
type fixedRepos struct{ repo *models.GitRepository }

func (f fixedRepos) GetRepo(context.Context, string, string) (*models.GitRepository, error) {
	return f.repo, nil
}

// nilCredResolver satisfies credentials.Resolver for file:// origins (the
// engine skips askpass injection on a nil credential).
type nilCredResolver struct{}

func (nilCredResolver) Resolve(context.Context, string) (credentials.Credential, error) {
	return nil, nil
}

// blockingTurn holds the in-flight lock by blocking inside Turn until released.
type blockingTurn struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (b *blockingTurn) Turn(_ context.Context, _, _, _ string, _ agentsvc.TurnRequest) (io.ReadCloser, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return io.NopCloser(strings.NewReader("data: [DONE]\n\n")), nil
}

func TestTasks_NoAuth_401(t *testing.T) {
	h := newRig(t, newIssues(), fakeExecs{})
	if rec := h.NoAuth().Get(tasks); rec.Code != http.StatusUnauthorized {
		t.Errorf("no-auth list: code %d, want 401", rec.Code)
	}
}

// row builds a seeded execution with a creation-time offset (hours).
func row(id string, kind taskmeta.ExecutionKind, status taskmeta.ExecutionStatus, reason string, hours int) *models.Execution {
	return &models.Execution{
		ID: id, Kind: string(kind), Status: string(status), Reason: reason,
		CreatedAt: time.Now().Add(time.Duration(hours) * time.Hour),
	}
}

// Ensure the execution package's progress service compiles into this test's
// import set (the progress route is covered by execution's own tests + the
// GetByIDScoped org fence; the internal S2S surface is not mounted by the
// componenttest harness, so the skills route's cross-tenant fence is asserted
// directly in the execution package).
var _ = execution.NewProgressService
