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
	"fmt"
	"testing"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

func taskIssue(number int, comp string, deps []string, extraLabels []string, state string) sourcecontrol.IssueInfo {
	block := taskmeta.Block{
		Component: comp,
		DependsOn: deps,
		Origin:    taskmeta.OriginSpecPlan,
		SpecTag:   "req-v1",
		DesignTag: "design-v1",
	}
	body := taskmeta.ComposeBody(block, taskmeta.Human{Rationale: "because", Body: "## Scope\nwork"})
	labels := append(taskmeta.NewTaskLabels(taskmeta.ClassCoding, taskmeta.OriginSpecPlan), extraLabels...)
	return sourcecontrol.IssueInfo{
		Number: number,
		Title:  "Implement " + comp,
		Body:   body,
		State:  state,
		URL:    fmt.Sprintf("https://github.com/o/r/issues/%d", number),
		Labels: labels,
	}
}

func newTestFunnel(store *fakeStore, issues *fakeIssues, design map[string]bool, exec delivery.Executor) *Funnel {
	reg := NewRegistry()
	if exec != nil {
		reg.Register(taskmeta.ClassCoding, exec)
	}
	return NewFunnel(store, issues, fakeRepos{orgID: "org1", projectID: "proj1"}, fakeDesign{names: design}, reg)
}

func TestFunnel_DepsSatisfied_Dispatches(t *testing.T) {
	store := newFakeStore()
	// Dependency #1 (user-service) is deployed: seed a succeeded build row.
	_, dep, _ := store.TryAdmit(context.Background(), &delivery.Execution{Repo: "o/r", IssueNumber: 1, Kind: string(taskmeta.KindBuild)})
	_, _ = store.Finish(context.Background(), dep.ID, string(taskmeta.ExecSucceeded), "")

	issues := newFakeIssues([]sourcecontrol.IssueInfo{
		taskIssue(1, "user-service", nil, nil, "open"),
		taskIssue(2, "order-service", []string{"user-service"}, []string{taskmeta.LabelExecute}, "open"),
	})
	exec := &fakeExecutor{store: store, startOK: true}
	f := newTestFunnel(store, issues, map[string]bool{"user-service": true, "order-service": true}, exec)

	if err := f.OnExecuteIntent(context.Background(), "o/r", 2); err != nil {
		t.Fatalf("OnExecuteIntent: %v", err)
	}
	if len(exec.got) != 1 {
		t.Fatalf("expected 1 dispatch, got %d", len(exec.got))
	}
	if exec.got[0].Execution.Kind != string(taskmeta.KindCoding) {
		t.Errorf("expected coding dispatch, got %q", exec.got[0].Execution.Kind)
	}
	// aep:execute must be consumed.
	if got := issues.removed[2]; len(got) == 0 || got[0] != taskmeta.LabelExecute {
		t.Errorf("expected aep:execute consumed on issue 2, got %v", got)
	}
	execs, _ := store.LatestPerKind(context.Background(), "o/r", 2)
	if c := execs[string(taskmeta.KindCoding)]; c == nil || c.Status != string(taskmeta.ExecRunning) {
		t.Errorf("expected running coding execution, got %+v", c)
	}
}

func TestFunnel_DepsUnsatisfied_QueuesNoDispatch(t *testing.T) {
	store := newFakeStore()
	issues := newFakeIssues([]sourcecontrol.IssueInfo{
		taskIssue(1, "user-service", nil, nil, "open"), // not deployed
		taskIssue(2, "order-service", []string{"user-service"}, []string{taskmeta.LabelExecute}, "open"),
	})
	exec := &fakeExecutor{store: store, startOK: true}
	f := newTestFunnel(store, issues, map[string]bool{"user-service": true, "order-service": true}, exec)

	if err := f.OnExecuteIntent(context.Background(), "o/r", 2); err != nil {
		t.Fatalf("OnExecuteIntent: %v", err)
	}
	if len(exec.got) != 0 {
		t.Fatalf("expected no dispatch (dep not deployed), got %d", len(exec.got))
	}
	execs, _ := store.LatestPerKind(context.Background(), "o/r", 2)
	if c := execs[string(taskmeta.KindCoding)]; c == nil || c.Status != string(taskmeta.ExecQueued) {
		t.Errorf("expected queued coding execution, got %+v", c)
	}
}

func TestFunnel_Held_QueuesNoDispatch(t *testing.T) {
	store := newFakeStore()
	issues := newFakeIssues([]sourcecontrol.IssueInfo{
		taskIssue(2, "order-service", nil, []string{taskmeta.LabelExecute, taskmeta.LabelHold}, "open"),
	})
	exec := &fakeExecutor{store: store, startOK: true}
	f := newTestFunnel(store, issues, map[string]bool{"order-service": true}, exec)

	if err := f.OnExecuteIntent(context.Background(), "o/r", 2); err != nil {
		t.Fatalf("OnExecuteIntent: %v", err)
	}
	if len(exec.got) != 0 {
		t.Fatalf("held task must not dispatch, got %d", len(exec.got))
	}
	execs, _ := store.LatestPerKind(context.Background(), "o/r", 2)
	if c := execs[string(taskmeta.KindCoding)]; c == nil || c.Status != string(taskmeta.ExecQueued) {
		t.Errorf("expected queued coding execution while held, got %+v", c)
	}
}

func TestFunnel_HoldLifted_Reevaluate_Dispatches(t *testing.T) {
	store := newFakeStore()
	held := taskIssue(2, "order-service", nil, []string{taskmeta.LabelExecute, taskmeta.LabelHold}, "open")
	issues := newFakeIssues([]sourcecontrol.IssueInfo{held})
	exec := &fakeExecutor{store: store, startOK: true}
	f := newTestFunnel(store, issues, map[string]bool{"order-service": true}, exec)

	_ = f.OnExecuteIntent(context.Background(), "o/r", 2) // queues behind hold
	// Lift the hold, then re-evaluate (sweep / unlabel path).
	issues.list[0].Labels = taskmeta.NewTaskLabels(taskmeta.ClassCoding, taskmeta.OriginSpecPlan)
	if err := f.Reevaluate(context.Background()); err != nil {
		t.Fatalf("Reevaluate: %v", err)
	}
	if len(exec.got) != 1 {
		t.Fatalf("expected dispatch after hold lifted, got %d", len(exec.got))
	}
}

func TestFunnel_OpsClass_NoExecutor_FlagsAttention(t *testing.T) {
	store := newFakeStore()
	issue := sourcecontrol.IssueInfo{
		Number: 3,
		Title:  "Provision DB",
		State:  "open",
		URL:    "https://github.com/o/r/issues/3",
		Body: taskmeta.ComposeBody(taskmeta.Block{
			Operation: "create-db", Origin: taskmeta.OriginManual, DesignTag: "design-v1",
		}, taskmeta.Human{Rationale: "r"}),
		Labels: append(taskmeta.NewTaskLabels(taskmeta.ClassOps, taskmeta.OriginManual), taskmeta.LabelExecute),
	}
	issues := newFakeIssues([]sourcecontrol.IssueInfo{issue})
	// Registry has only the coding executor.
	exec := &fakeExecutor{store: store, startOK: true}
	f := newTestFunnel(store, issues, map[string]bool{}, exec)

	if err := f.OnExecuteIntent(context.Background(), "o/r", 3); err != nil {
		t.Fatalf("OnExecuteIntent: %v", err)
	}
	if len(exec.got) != 0 {
		t.Fatalf("ops task must not reach the coding executor")
	}
	if len(issues.added[3]) == 0 || issues.added[3][0] != taskmeta.LabelAttention {
		t.Errorf("expected aep:attention on ops task, got %v", issues.added[3])
	}
	execs, _ := store.LatestPerKind(context.Background(), "o/r", 3)
	if c := execs[string(taskmeta.KindCoding)]; c == nil || c.Status != string(taskmeta.ExecCanceled) {
		t.Errorf("expected canceled row for no-executor class, got %+v", c)
	}
}

func TestFunnel_ComponentRemoved_FlagsAttention(t *testing.T) {
	store := newFakeStore()
	issues := newFakeIssues([]sourcecontrol.IssueInfo{
		taskIssue(2, "order-service", nil, []string{taskmeta.LabelExecute}, "open"),
	})
	exec := &fakeExecutor{store: store, startOK: true}
	// Design at HEAD no longer has order-service.
	f := newTestFunnel(store, issues, map[string]bool{"user-service": true}, exec)

	if err := f.OnExecuteIntent(context.Background(), "o/r", 2); err != nil {
		t.Fatalf("OnExecuteIntent: %v", err)
	}
	if len(exec.got) != 0 {
		t.Fatalf("stale-component task must not dispatch")
	}
	if len(issues.added[2]) == 0 || issues.added[2][0] != taskmeta.LabelAttention {
		t.Errorf("expected aep:attention for removed component, got %v", issues.added[2])
	}
}

func TestFunnel_Cycle_FlagsAttention(t *testing.T) {
	store := newFakeStore()
	issues := newFakeIssues([]sourcecontrol.IssueInfo{
		taskIssue(1, "a", []string{"b"}, nil, "open"),
		taskIssue(2, "b", []string{"a"}, []string{taskmeta.LabelExecute}, "open"),
	})
	exec := &fakeExecutor{store: store, startOK: true}
	f := newTestFunnel(store, issues, map[string]bool{"a": true, "b": true}, exec)

	if err := f.OnExecuteIntent(context.Background(), "o/r", 2); err != nil {
		t.Fatalf("OnExecuteIntent: %v", err)
	}
	if len(exec.got) != 0 {
		t.Fatalf("cyclic task must not dispatch")
	}
	if len(issues.added[2]) == 0 || issues.added[2][0] != taskmeta.LabelAttention {
		t.Errorf("expected aep:attention for cycle, got %v", issues.added[2])
	}
}

func TestFunnel_Idempotent_ActiveExecution(t *testing.T) {
	store := newFakeStore()
	issues := newFakeIssues([]sourcecontrol.IssueInfo{
		taskIssue(2, "order-service", nil, []string{taskmeta.LabelExecute}, "open"),
	})
	exec := &fakeExecutor{store: store, startOK: true}
	f := newTestFunnel(store, issues, map[string]bool{"order-service": true}, exec)

	_ = f.OnExecuteIntent(context.Background(), "o/r", 2)
	_ = f.OnExecuteIntent(context.Background(), "o/r", 2) // second intent while running
	if len(exec.got) != 1 {
		t.Fatalf("expected exactly one dispatch under re-intent, got %d", len(exec.got))
	}
}

func TestFunnel_ClosedIssue_NoDispatch(t *testing.T) {
	store := newFakeStore()
	issues := newFakeIssues([]sourcecontrol.IssueInfo{
		taskIssue(2, "order-service", nil, []string{taskmeta.LabelExecute}, "closed"),
	})
	exec := &fakeExecutor{store: store, startOK: true}
	f := newTestFunnel(store, issues, map[string]bool{"order-service": true}, exec)

	if err := f.OnExecuteIntent(context.Background(), "o/r", 2); err != nil {
		t.Fatalf("OnExecuteIntent: %v", err)
	}
	if len(exec.got) != 0 {
		t.Fatalf("closed issue must not dispatch")
	}
	if got := issues.removed[2]; len(got) == 0 {
		t.Errorf("expected aep:execute consumed on closed issue")
	}
}

// --- build retry through the funnel (§7 "retry = a new row of that kind") -----

// seedMergedFailedBuild puts a Task into the "coding succeeded → PR merged →
// build FAILED" state, the state a build retry recovers.
func seedMergedFailedBuild(store *fakeStore, mergeSHA string) {
	ctx := context.Background()
	_, c, _ := store.TryAdmit(ctx, &delivery.Execution{Repo: "o/r", IssueNumber: 2, Kind: string(taskmeta.KindCoding), Component: "order-service"})
	_, _ = store.Finish(ctx, c.ID, string(taskmeta.ExecSucceeded), reasonPROpenPrefix+"7")
	_, b, _ := store.TryAdmit(ctx, &delivery.Execution{Repo: "o/r", IssueNumber: 2, Kind: string(taskmeta.KindBuild), Component: "order-service", CommitSHA: mergeSHA})
	_, _ = store.Finish(ctx, b.ID, string(taskmeta.ExecFailed), "build failed")
}

func TestFunnel_ExecuteOnFailedBuild_RetriesBuildAtSameSHA(t *testing.T) {
	store := newFakeStore()
	seedMergedFailedBuild(store, "sha7")
	issues := newFakeIssues([]sourcecontrol.IssueInfo{taskIssue(2, "order-service", nil, []string{taskmeta.LabelExecute}, "open")})
	exec := &fakeExecutor{store: store, startOK: true}
	f := newTestFunnel(store, issues, map[string]bool{"order-service": true}, exec)

	if err := f.OnExecuteIntent(context.Background(), "o/r", 2); err != nil {
		t.Fatalf("OnExecuteIntent: %v", err)
	}
	if len(exec.got) != 1 || exec.got[0].Execution.Kind != string(taskmeta.KindBuild) || exec.got[0].MergeSHA != "sha7" {
		t.Fatalf("execute on a failed-build Task must retry the BUILD at the stored SHA, got %+v", exec.got)
	}
	// A new build row was admitted (retry = new row); no new coding row.
	coding, build := 0, 0
	rows, _ := store.ListByIssue(context.Background(), "o/r", 2)
	for _, r := range rows {
		switch r.Kind {
		case string(taskmeta.KindCoding):
			coding++
		case string(taskmeta.KindBuild):
			build++
		}
	}
	if coding != 1 {
		t.Errorf("no coding re-run expected, got %d coding rows", coding)
	}
	if build != 2 {
		t.Errorf("expected a new build row (2 total: failed + retry), got %d", build)
	}
}

func TestFunnel_ExecuteOnFailedCoding_RetriesCoding(t *testing.T) {
	store := newFakeStore()
	// Latest coding FAILED (no merged PR) — retry re-runs coding, unchanged.
	_, c, _ := store.TryAdmit(context.Background(), &delivery.Execution{Repo: "o/r", IssueNumber: 2, Kind: string(taskmeta.KindCoding), Component: "order-service"})
	_, _ = store.Finish(context.Background(), c.ID, string(taskmeta.ExecFailed), "agent crashed")
	issues := newFakeIssues([]sourcecontrol.IssueInfo{taskIssue(2, "order-service", nil, []string{taskmeta.LabelExecute}, "open")})
	exec := &fakeExecutor{store: store, startOK: true}
	f := newTestFunnel(store, issues, map[string]bool{"order-service": true}, exec)

	if err := f.OnExecuteIntent(context.Background(), "o/r", 2); err != nil {
		t.Fatalf("OnExecuteIntent: %v", err)
	}
	if len(exec.got) != 1 || exec.got[0].Execution.Kind != string(taskmeta.KindCoding) {
		t.Fatalf("execute on a failed-coding Task must re-run CODING, got %+v", exec.got)
	}
}

func TestFunnel_ExecuteOnFailedBuild_Held_Queues(t *testing.T) {
	store := newFakeStore()
	seedMergedFailedBuild(store, "sha7")
	issues := newFakeIssues([]sourcecontrol.IssueInfo{taskIssue(2, "order-service", nil, []string{taskmeta.LabelExecute, taskmeta.LabelHold}, "open")})
	exec := &fakeExecutor{store: store, startOK: true}
	f := newTestFunnel(store, issues, map[string]bool{"order-service": true}, exec)

	if err := f.OnExecuteIntent(context.Background(), "o/r", 2); err != nil {
		t.Fatalf("OnExecuteIntent: %v", err)
	}
	if len(exec.got) != 0 {
		t.Fatalf("a held build retry must not dispatch, got %d", len(exec.got))
	}
	// The build retry row is admitted but left queued behind the hold.
	execs, _ := store.LatestPerKind(context.Background(), "o/r", 2)
	if b := execs[string(taskmeta.KindBuild)]; b == nil || b.Status != string(taskmeta.ExecQueued) {
		t.Fatalf("expected a queued build retry row while held, got %+v", b)
	}
}

func TestFunnel_Reevaluate_ReleasesQueuedBuildWhenUnheld(t *testing.T) {
	store := newFakeStore()
	// A queued build retry (as if admitted while held) at sha7; the issue is now
	// NOT held → Reevaluate must dispatch it.
	_, _, _ = store.TryAdmit(context.Background(), &delivery.Execution{Repo: "o/r", IssueNumber: 2, Kind: string(taskmeta.KindBuild), Component: "order-service", CommitSHA: "sha7"})
	issues := newFakeIssues([]sourcecontrol.IssueInfo{taskIssue(2, "order-service", nil, nil, "open")})
	exec := &fakeExecutor{store: store, startOK: true}
	f := newTestFunnel(store, issues, map[string]bool{"order-service": true}, exec)

	if err := f.Reevaluate(context.Background()); err != nil {
		t.Fatalf("Reevaluate: %v", err)
	}
	if len(exec.got) != 1 || exec.got[0].Execution.Kind != string(taskmeta.KindBuild) || exec.got[0].MergeSHA != "sha7" {
		t.Fatalf("Reevaluate must dispatch a queued build row when unheld, got %+v", exec.got)
	}
}

func TestFunnel_ExecuteOnFailedBuild_Closed_NoDispatch(t *testing.T) {
	store := newFakeStore()
	seedMergedFailedBuild(store, "sha7")
	issues := newFakeIssues([]sourcecontrol.IssueInfo{taskIssue(2, "order-service", nil, []string{taskmeta.LabelExecute}, "closed")})
	exec := &fakeExecutor{store: store, startOK: true}
	f := newTestFunnel(store, issues, map[string]bool{"order-service": true}, exec)

	if err := f.OnExecuteIntent(context.Background(), "o/r", 2); err != nil {
		t.Fatalf("OnExecuteIntent: %v", err)
	}
	if len(exec.got) != 0 {
		t.Fatalf("execute on a CLOSED task must not dispatch (closed gate), got %d", len(exec.got))
	}
}
