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

package codingagent

import (
	"context"
	"errors"
	"testing"

	"github.com/wso2/aep/aep-api/internal/gen"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	ocmocks "github.com/wso2/aep/aep-api/internal/clients/openchoreo/mocks"
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/delivery"
)

// runningBuild seeds a running build row with a given run name + reason.
func runningBuild(id, runName, reason string) *delivery.Execution {
	return &delivery.Execution{
		ID: id, OrgID: "acme", ProjectID: "widgets", Repo: "acme/widgets", IssueNumber: 7,
		Kind: string(taskmeta.KindBuild), Status: string(taskmeta.ExecRunning),
		Component: "order-service", CommitSHA: "deadbeef", RunName: runName, Reason: reason,
	}
}

// ocRuns serves a fixed WorkflowRun for any GetWorkflowRun by run name.
func ocRuns(byRun map[string]*gen.WorkflowRun) *ocmocks.ComponentClientMock {
	return &ocmocks.ComponentClientMock{
		GetWorkflowRunFunc: func(_ context.Context, _, runName string) (*gen.WorkflowRun, error) {
			return byRun[runName], nil
		},
	}
}

func authFailedRun(name string) *gen.WorkflowRun {
	return &gen.WorkflowRun{
		Name: name, Completed: true, Status: openchoreo.ReasonWorkflowFailed,
		Tasks: []gen.WorkflowRunTask{{Name: "checkout-source", Phase: "Failed", Message: "fatal: Authentication failed for 'https://github.com/acme/widgets'"}},
	}
}

func plainFailedRun(name string) *gen.WorkflowRun {
	return &gen.WorkflowRun{
		Name: name, Completed: true, Status: openchoreo.ReasonWorkflowFailed,
		Tasks: []gen.WorkflowRunTask{{Name: "build", Phase: "Failed", Message: "compilation error"}},
	}
}

func TestExecWatcher_BuildGitAuthFailure_WithinBudget_ReMintsAndRetries(t *testing.T) {
	row := runningBuild("b1", "run-1", "") // first failure — no retry marker yet
	repo := newFakeExecRepo(row)
	retrier := &fakeRetrier{newRun: "run-2"}
	w := NewExecWatcher(ocRuns(map[string]*gen.WorkflowRun{"run-1": authFailedRun("run-1")}), repo, nil, nil, 0).
		WithBuildRetrier(retrier, 3)

	if err := w.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if retrier.count() != 1 {
		t.Fatalf("expected exactly one re-mint+retry, got %d", retrier.count())
	}
	got := repo.get("b1")
	if got.Status != string(taskmeta.ExecRunning) {
		t.Errorf("row must stay running across a retry, got %q", got.Status)
	}
	if got.RunName != "run-2" {
		t.Errorf("row must track the re-triggered run, got %q", got.RunName)
	}
	if got.Reason != "build_auth_retry:1" {
		t.Errorf("retry attempt must be recorded on reason, got %q", got.Reason)
	}
}

func TestExecWatcher_BuildGitAuthFailure_BudgetExhausted_FinishesFailed(t *testing.T) {
	row := runningBuild("b1", "run-3", "build_auth_retry:3") // already at budget
	repo := newFakeExecRepo(row)
	retrier := &fakeRetrier{newRun: "run-4"}
	w := NewExecWatcher(ocRuns(map[string]*gen.WorkflowRun{"run-3": authFailedRun("run-3")}), repo, nil, nil, 0).
		WithBuildRetrier(retrier, 3)

	if err := w.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if retrier.count() != 0 {
		t.Errorf("budget exhausted → no further retry, got %d", retrier.count())
	}
	got := repo.get("b1")
	if got.Status != string(taskmeta.ExecFailed) || got.Reason != buildAuthRetryExceededReason {
		t.Errorf("exhausted budget must Finish failed with %q, got status=%q reason=%q", buildAuthRetryExceededReason, got.Status, got.Reason)
	}
}

func TestExecWatcher_BuildGitAuthFailure_ReMintErrorStillMarchesToBudget(t *testing.T) {
	row := runningBuild("b1", "run-1", "")
	repo := newFakeExecRepo(row)
	retrier := &fakeRetrier{err: errors.New("org disconnected")}
	w := NewExecWatcher(ocRuns(map[string]*gen.WorkflowRun{"run-1": authFailedRun("run-1")}), repo, nil, nil, 0).
		WithBuildRetrier(retrier, 3)

	if err := w.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	got := repo.get("b1")
	// The attempt counter still advances (so a stuck re-mint eventually exhausts),
	// but the run name is unchanged (the old failed run).
	if got.Reason != "build_auth_retry:1" || got.RunName != "run-1" {
		t.Errorf("failed re-mint must advance the counter on the same run, got reason=%q run=%q", got.Reason, got.RunName)
	}
	if got.Status != string(taskmeta.ExecRunning) {
		t.Errorf("row must stay running, got %q", got.Status)
	}
}

func TestExecWatcher_BuildPlainFailure_FinishesFailed(t *testing.T) {
	row := runningBuild("b1", "run-1", "")
	repo := newFakeExecRepo(row)
	retrier := &fakeRetrier{}
	w := NewExecWatcher(ocRuns(map[string]*gen.WorkflowRun{"run-1": plainFailedRun("run-1")}), repo, nil, nil, 0).
		WithBuildRetrier(retrier, 3)

	if err := w.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if retrier.count() != 0 {
		t.Errorf("a non-auth failure must not retry, got %d", retrier.count())
	}
	if got := repo.get("b1"); got.Status != string(taskmeta.ExecFailed) {
		t.Errorf("plain failure must Finish failed, got %q", got.Status)
	}
}

func TestExecWatcher_BuildSuccess_FinishesAndReevaluates(t *testing.T) {
	row := runningBuild("b1", "run-1", "")
	repo := newFakeExecRepo(row)
	reeval := &fakeReeval{}
	ok := &gen.WorkflowRun{Name: "run-1", Completed: true, Status: openchoreo.ReasonWorkflowSucceeded}
	w := NewExecWatcher(ocRuns(map[string]*gen.WorkflowRun{"run-1": ok}), repo, reeval, nil, 0)

	if err := w.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := repo.get("b1"); got.Status != string(taskmeta.ExecSucceeded) {
		t.Errorf("build success must Finish succeeded, got %q", got.Status)
	}
	if reeval.count() != 1 {
		t.Errorf("build success must reevaluate the funnel once, got %d", reeval.count())
	}
}

// TestExecWatcher_SkipsProxyJobRuns proves the ExecWatcher ignores
// proxy-dispatched coding-agent Jobs (`ca-…`, owned by the JobWatcher) and polls
// only OpenChoreo WorkflowRuns (`wf-…`) — otherwise it spams "WorkflowRun not
// found" every tick for the Job rows.
func TestExecWatcher_SkipsProxyJobRuns(t *testing.T) {
	caRow := &delivery.Execution{ID: "j1", OrgID: "acme", Repo: "acme/widgets", IssueNumber: 7,
		Kind: string(taskmeta.KindCoding), Status: string(taskmeta.ExecRunning), RunName: "ca-abc12345-2601011200"}
	wfRow := &delivery.Execution{ID: "w1", OrgID: "acme", Repo: "acme/widgets", IssueNumber: 8,
		Kind: string(taskmeta.KindCoding), Status: string(taskmeta.ExecRunning), RunName: "wf-xyz"}
	repo := newFakeExecRepo(caRow, wfRow)

	var polled []string
	oc := &ocmocks.ComponentClientMock{
		GetWorkflowRunFunc: func(_ context.Context, _, runName string) (*gen.WorkflowRun, error) {
			polled = append(polled, runName)
			return &gen.WorkflowRun{Name: runName, Completed: false}, nil // still running
		},
	}
	w := NewExecWatcher(oc, repo, nil, nil, 0)

	if err := w.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(polled) != 1 || polled[0] != "wf-xyz" {
		t.Fatalf("ExecWatcher must skip ca- rows and poll only wf-, polled=%v", polled)
	}
}

func TestExecWatcher_CodingFailure_FinishesFailed_SuccessRidesPRWebhook(t *testing.T) {
	// A ClusterWorkflow (`wf-…`) coding run — the ExecWatcher's domain; proxy
	// `ca-…` runs are the JobWatcher's and are skipped here.
	coding := &delivery.Execution{ID: "c1", OrgID: "acme", Repo: "acme/widgets", IssueNumber: 7,
		Kind: string(taskmeta.KindCoding), Status: string(taskmeta.ExecRunning), RunName: "wf-1"}
	repo := newFakeExecRepo(coding)
	failed := &gen.WorkflowRun{Name: "wf-1", Completed: true, Status: openchoreo.ReasonWorkflowFailed}
	w := NewExecWatcher(ocRuns(map[string]*gen.WorkflowRun{"wf-1": failed}), repo, nil, nil, 0)

	if err := w.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if got := repo.get("c1"); got.Status != string(taskmeta.ExecFailed) {
		t.Errorf("failed coding run must Finish failed (no PR will open), got %q", got.Status)
	}
}
