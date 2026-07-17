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
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/models"
)

// validationTaskIssue builds a project-scoped aep:validation Task issue
// (operation "validate", no component) that dependsOn the given components.
func validationTaskIssue(number int, deps []string, extraLabels []string, state string) sourcecontrol.IssueInfo {
	block := taskmeta.Block{
		Operation: "validate",
		DependsOn: deps,
		Origin:    taskmeta.OriginSpecPlan,
		DesignTag: "design-v1",
	}
	body := taskmeta.ComposeBody(block, taskmeta.Human{Rationale: "validate the deployed system", Body: "## Report"})
	labels := append(taskmeta.NewTaskLabels(taskmeta.ClassValidation, taskmeta.OriginSpecPlan), extraLabels...)
	return sourcecontrol.IssueInfo{
		Number: number,
		Title:  "Validate the deployed system against its acceptance criteria",
		Body:   body,
		State:  state,
		URL:    fmt.Sprintf("https://github.com/o/r/issues/%d", number),
		Labels: labels,
	}
}

func newValidationFunnel(store *fakeStore, issues *fakeIssues, design map[string]bool, exec Executor) *Funnel {
	reg := NewRegistry()
	reg.Register(taskmeta.ClassValidation, exec)
	return NewFunnel(store, issues, fakeRepos{orgID: "org1", projectID: "proj1"}, fakeDesign{names: design}, reg)
}

// A validation Task (operation-based, dependsOn every component) must be gated
// by depsGate exactly like a coding task — held while a component is not
// deployed, dispatched once it deploys. This pins the depsGate fix that stopped
// short-circuiting Component=="" tasks that declare dependsOn.
func TestFunnel_Validation_HeldUntilDepsDeploy(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	issues := newFakeIssues([]sourcecontrol.IssueInfo{
		taskIssue(1, "hello-web", nil, nil, "open"), // component task, not yet deployed
		validationTaskIssue(9, []string{"hello-web"}, []string{taskmeta.LabelExecute}, "open"),
	})
	exec := &fakeExecutor{store: store, startOK: true}
	f := newValidationFunnel(store, issues, map[string]bool{"hello-web": true}, exec)

	// Dep not deployed → held (queued, no dispatch).
	if err := f.OnExecuteIntent(ctx, "o/r", 9); err != nil {
		t.Fatalf("OnExecuteIntent: %v", err)
	}
	if len(exec.got) != 0 {
		t.Fatalf("validation must hold until its component deps deploy, got %d dispatches", len(exec.got))
	}
	execs, _ := store.LatestPerKind(ctx, "o/r", 9)
	if c := execs[string(taskmeta.KindCoding)]; c == nil || c.Status != string(taskmeta.ExecQueued) {
		t.Fatalf("expected a queued execution while held, got %+v", c)
	}

	// Deploy hello-web (a succeeded build derives the component deployed), then
	// Reevaluate re-gates the queued row and dispatches the validation run.
	_, dep, _ := store.TryAdmit(ctx, &models.Execution{Repo: "o/r", IssueNumber: 1, Kind: string(taskmeta.KindBuild)})
	_, _ = store.Finish(ctx, dep.ID, string(taskmeta.ExecSucceeded), "")

	if err := f.Reevaluate(ctx); err != nil {
		t.Fatalf("Reevaluate: %v", err)
	}
	if len(exec.got) != 1 {
		t.Fatalf("expected 1 dispatch after dep deployed, got %d", len(exec.got))
	}
	if exec.got[0].Task.Class != taskmeta.ClassValidation {
		t.Errorf("dispatched task class = %q; want validation", exec.got[0].Task.Class)
	}
}

// A merged validation PR must NOT spawn a build: a validation Task is project-
// scoped (no component to build) and rests at merged.
func TestEvents_Validation_PRMerged_NoBuild(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	issues := newFakeIssues([]sourcecontrol.IssueInfo{validationTaskIssue(9, []string{"hello-web"}, nil, "open")})
	exec := &fakeExecutor{store: store, startOK: true}
	e := newEvents(store, issues, exec)

	if err := e.PullRequestClosed(ctx, "pull_request", "closed",
		prPayload("closed", 9, true, "abc123", "Closes #9", "dev")); err != nil {
		t.Fatalf("PullRequestClosed(merged validation): %v", err)
	}
	if len(exec.got) != 0 {
		t.Fatalf("merged validation PR must not dispatch a build, got %d", len(exec.got))
	}
	execs, _ := store.LatestPerKind(ctx, "o/r", 9)
	if b := execs[string(taskmeta.KindBuild)]; b != nil {
		t.Errorf("no build execution should be admitted for a validation merge, got %+v", b)
	}
}
