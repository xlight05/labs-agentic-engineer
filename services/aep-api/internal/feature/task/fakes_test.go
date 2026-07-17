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

package task

import (
	"context"
	"fmt"
	"sync"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/models"
)

// fakeIssues is an in-memory IssueClient: issues keyed by number, recording
// label/comment/body/title mutations.
type fakeIssues struct {
	mu           sync.Mutex
	byNumber     map[int]*sourcecontrol.IssueInfo
	nextNum      int
	created      []sourcecontrol.CreateIssueRequest
	comments     map[int][]string
	failCreate   bool
	failEditBody bool
}

func newFakeIssues() *fakeIssues {
	return &fakeIssues{byNumber: map[int]*sourcecontrol.IssueInfo{}, nextNum: 100, comments: map[int][]string{}}
}

func (f *fakeIssues) seed(issue sourcecontrol.IssueInfo) *fakeIssues {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := issue
	f.byNumber[issue.Number] = &cp
	if issue.Number >= f.nextNum {
		f.nextNum = issue.Number + 1
	}
	return f
}

func (f *fakeIssues) CreateIssue(_ context.Context, _, _ string, req sourcecontrol.CreateIssueRequest) (*sourcecontrol.IssueResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failCreate {
		return nil, fmt.Errorf("boom: create failed")
	}
	n := f.nextNum
	f.nextNum++
	f.created = append(f.created, req)
	f.byNumber[n] = &sourcecontrol.IssueInfo{Number: n, Title: req.Title, Body: req.Body, State: "open", Labels: req.Labels, URL: fmt.Sprintf("https://github.com/o/r/issues/%d", n)}
	return &sourcecontrol.IssueResult{Number: n, URL: f.byNumber[n].URL}, nil
}

func (f *fakeIssues) ListIssues(_ context.Context, _, _ string, labels []string) ([]sourcecontrol.IssueInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sourcecontrol.IssueInfo
	for _, issue := range f.byNumber {
		if issueHasAll(issue.Labels, labels) {
			out = append(out, *issue)
		}
	}
	return out, nil
}

func (f *fakeIssues) GetIssue(_ context.Context, _, _ string, number int) (*sourcecontrol.IssueInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.byNumber[number]; i != nil {
		cp := *i
		return &cp, nil
	}
	return nil, sourcecontrol.ErrIssueNotFound
}

func (f *fakeIssues) CommentIssue(_ context.Context, _, _ string, number int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments[number] = append(f.comments[number], body)
	return nil
}

func (f *fakeIssues) EditIssueBody(_ context.Context, _, _ string, number int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failEditBody {
		return fmt.Errorf("boom: edit body failed")
	}
	if i := f.byNumber[number]; i != nil {
		i.Body = body
	}
	return nil
}

func (f *fakeIssues) EditIssueTitle(_ context.Context, _, _ string, number int, title string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.byNumber[number]; i != nil {
		i.Title = title
	}
	return nil
}

func (f *fakeIssues) AddLabels(_ context.Context, _, _ string, number int, labels []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.byNumber[number]; i != nil {
		i.Labels = append(i.Labels, labels...)
	}
	return nil
}

func (f *fakeIssues) RemoveLabel(_ context.Context, _, _ string, number int, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.byNumber[number]
	if i == nil {
		return nil
	}
	var kept []string
	for _, l := range i.Labels {
		if l != label {
			kept = append(kept, l)
		}
	}
	i.Labels = kept
	return nil
}

func (f *fakeIssues) labelsOf(number int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.byNumber[number]; i != nil {
		return append([]string{}, i.Labels...)
	}
	return nil
}

func (f *fakeIssues) bodyOf(number int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.byNumber[number]; i != nil {
		return i.Body
	}
	return ""
}

func issueHasAll(have, want []string) bool {
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

// fakeRepos returns a fixed repo row.
type fakeRepos struct {
	repo *models.GitRepository
}

func (f fakeRepos) GetRepo(context.Context, string, string) (*models.GitRepository, error) {
	if f.repo == nil {
		return nil, sourcecontrol.ErrRepoNotFound
	}
	return f.repo, nil
}

func defaultRepo() *models.GitRepository {
	return &models.GitRepository{OrgID: "org1", ProjectID: "proj1", RepoURL: "https://github.com/o/r"}
}

// fakeExecReader serves seeded execution rows.
type fakeExecReader struct {
	latest  map[int]map[string]*models.Execution // issueNumber → kind → row
	history map[int][]models.Execution
}

func newFakeExecReader() *fakeExecReader {
	return &fakeExecReader{latest: map[int]map[string]*models.Execution{}, history: map[int][]models.Execution{}}
}

func (f *fakeExecReader) put(number int, e models.Execution) *fakeExecReader {
	if f.latest[number] == nil {
		f.latest[number] = map[string]*models.Execution{}
	}
	cp := e
	f.latest[number][e.Kind] = &cp
	f.history[number] = append(f.history[number], e)
	return f
}

func (f *fakeExecReader) LatestPerKindScoped(_ context.Context, _, _ string, number int) (map[string]*models.Execution, error) {
	if m := f.latest[number]; m != nil {
		return m, nil
	}
	return map[string]*models.Execution{}, nil
}

func (f *fakeExecReader) LatestPerKindForRepoScoped(_ context.Context, _, _ string) (map[int]map[string]*models.Execution, error) {
	return f.latest, nil
}

func (f *fakeExecReader) ListByIssueScoped(_ context.Context, _, _ string, number int) ([]models.Execution, error) {
	return f.history[number], nil
}

// fakeDispatcher records funnel calls (thread-safe — Execute/Unhold dispatch on
// a detached goroutine) and signals each on a channel so tests can await them.
type fakeDispatcher struct {
	mu           sync.Mutex
	executeCalls []int // issue numbers
	reevalCalls  int
	signal       chan struct{}
}

func newFakeDispatcher() *fakeDispatcher {
	return &fakeDispatcher{signal: make(chan struct{}, 8)}
}

func (f *fakeDispatcher) OnExecuteIntent(_ context.Context, _ string, number int) error {
	f.mu.Lock()
	f.executeCalls = append(f.executeCalls, number)
	f.mu.Unlock()
	f.signal <- struct{}{}
	return nil
}

func (f *fakeDispatcher) Reevaluate(context.Context) error {
	f.mu.Lock()
	f.reevalCalls++
	f.mu.Unlock()
	f.signal <- struct{}{}
	return nil
}

func (f *fakeDispatcher) executed() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int{}, f.executeCalls...)
}

func (f *fakeDispatcher) reevaluated() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reevalCalls
}

// fakeRepoLocator resolves any full name to one org/project.
type fakeRepoLocator struct{}

func (fakeRepoLocator) ByFullName(context.Context, string) (string, string, error) {
	return "org1", "proj1", nil
}

// fakeDesign is an in-memory DesignReader: per-component provision + org-service
// dependency names (keys lowercased, as the design adapter emits them).
type fakeDesign struct {
	provision  map[string][]string
	orgService map[string][]string
}

func (f fakeDesign) ProvisionDepNames(context.Context, string, string) (map[string][]string, error) {
	return f.provision, nil
}

func (f fakeDesign) OrgServiceDepNames(context.Context, string, string) (map[string][]string, error) {
	return f.orgService, nil
}

// provisionGateIssue builds a seeded aep:provision gate issue whose machine-block
// component IS the dependency name it gates (dependency-management §3.6) — the
// exact shape the read path indexes into provisionByDep.
func provisionGateIssue(number int, depName string) sourcecontrol.IssueInfo {
	block := taskmeta.Block{
		Component: depName,
		GateKind:  taskmeta.GateConfigCollection,
		Origin:    taskmeta.OriginSpecPlan,
		DesignTag: "design-v1",
	}
	body := taskmeta.ComposeBody(block, taskmeta.Human{Rationale: "gate"})
	return sourcecontrol.IssueInfo{
		Number: number,
		Title:  "Provision " + depName,
		Body:   body,
		State:  "open",
		URL:    fmt.Sprintf("https://github.com/o/r/issues/%d", number),
		Labels: taskmeta.NewTaskLabels(taskmeta.ClassProvision, taskmeta.OriginSpecPlan),
	}
}
