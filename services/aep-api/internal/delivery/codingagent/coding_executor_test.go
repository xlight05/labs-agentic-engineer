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
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	ocmocks "github.com/wso2/aep/aep-api/internal/clients/openchoreo/mocks"
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/delivery"
)

// stagedRef is the secretRef our fake stager returns (mirrors the real per-org
// build GitSecret name, orgcreds.BuildGitSecretName — a literal here so the
// codingagent package holds no orgcreds import).
const stagedRef = "aep-component-build-git-secret"

// buildTrigger captures the args of the last TriggerBuildAtCommit call.
type buildTrigger struct {
	called    bool
	sha       string
	secretRef string
	component string
	runName   string
}

func ocWithBuildCapture(cap *buildTrigger) *ocmocks.ComponentClientMock {
	return &ocmocks.ComponentClientMock{
		TriggerBuildAtCommitFunc: func(_ context.Context, _, _, componentName, commitSHA, secretRef, runName string) (*gen.WorkflowRun, error) {
			cap.called = true
			cap.sha, cap.secretRef, cap.component, cap.runName = commitSHA, secretRef, componentName, runName
			return &gen.WorkflowRun{Name: runName}, nil
		},
	}
}

func buildRow(id string) *delivery.Execution {
	return &delivery.Execution{
		ID: id, OrgID: "acme", ProjectID: "widgets", Repo: "acme/widgets", IssueNumber: 7,
		Kind: string(taskmeta.KindBuild), Status: string(taskmeta.ExecQueued),
		Component: "order-service", CommitSHA: "deadbeef",
	}
}

func buildDispatch(row *delivery.Execution) delivery.DispatchRequest {
	return delivery.DispatchRequest{
		Execution: row,
		Task:      delivery.TaskFacts{OrgID: "acme", ProjectID: "widgets", Component: "order-service"},
		MergeSHA:  "deadbeef",
	}
}

func newBuildExecutor(oc openchoreo.ComponentClient, repo *sourcecontrol.GitRepository, execRows *fakeExecRepo) *CodingExecutor {
	return NewCodingExecutor(oc, fakeRepos{repo: repo}, nil, nil, nil, execRows, "http://git", "http://platform", nil, nil, nil, nil)
}

func TestRunBuild_StagesSecret_PassesRefToBuild(t *testing.T) {
	cap := &buildTrigger{}
	row := buildRow("e1")
	repoRows := newFakeExecRepo(row)
	stager := &fakeStager{ref: stagedRef}
	e := newBuildExecutor(ocWithBuildCapture(cap), &sourcecontrol.GitRepository{RepoSlug: "acme-widgets"}, repoRows).
		WithBuildSecrets(stager, 0)

	if err := e.Run(context.Background(), buildDispatch(row)); err != nil {
		t.Fatalf("Run(build): %v", err)
	}
	if !cap.called {
		t.Fatal("TriggerBuildAtCommit was not called")
	}
	if cap.secretRef != stagedRef {
		t.Errorf("build secretRef = %q, want the staged ref (private-repo clone)", cap.secretRef)
	}
	if cap.sha != "deadbeef" || cap.component != "order-service" {
		t.Errorf("build args wrong: sha=%q component=%q", cap.sha, cap.component)
	}
	if stager.calls() != 1 {
		t.Errorf("StageBuildSecret calls = %d, want 1", stager.calls())
	}
	// The row was Started with the returned run name (one discipline).
	if got := repoRows.get("e1"); got.Status != string(taskmeta.ExecRunning) || got.RunName != cap.runName {
		t.Errorf("row not started with run name: status=%q run=%q", got.Status, got.RunName)
	}
}

func TestRunBuild_StagingRefusal_BlocksBuild(t *testing.T) {
	cap := &buildTrigger{}
	row := buildRow("e1")
	stager := &fakeStager{err: errors.New("org disconnected")}
	e := newBuildExecutor(ocWithBuildCapture(cap), &sourcecontrol.GitRepository{RepoSlug: "acme-widgets"}, newFakeExecRepo(row)).
		WithBuildSecrets(stager, 0)

	err := e.Run(context.Background(), buildDispatch(row))
	if err == nil {
		t.Fatal("staging refusal must block the build (returned nil)")
	}
	if cap.called {
		t.Error("TriggerBuildAtCommit must not be called when staging is refused")
	}
}

func TestRunBuild_NoStager_ClonesUnauthenticated(t *testing.T) {
	cap := &buildTrigger{}
	row := buildRow("e1")
	// No WithBuildSecrets → public-repo path, empty secretRef.
	e := newBuildExecutor(ocWithBuildCapture(cap), &sourcecontrol.GitRepository{RepoSlug: "acme-widgets"}, newFakeExecRepo(row))

	if err := e.Run(context.Background(), buildDispatch(row)); err != nil {
		t.Fatalf("Run(build): %v", err)
	}
	if !cap.called || cap.secretRef != "" {
		t.Errorf("no stager → empty secretRef; got called=%v ref=%q", cap.called, cap.secretRef)
	}
}

func TestRunBuild_NoRepoSlug_Degrades(t *testing.T) {
	cap := &buildTrigger{}
	row := buildRow("e1")
	stager := &fakeStager{ref: "should-not-be-used"}
	// Repo row present but no slug → cannot stage; degrade to unauthenticated.
	e := newBuildExecutor(ocWithBuildCapture(cap), &sourcecontrol.GitRepository{RepoSlug: ""}, newFakeExecRepo(row)).
		WithBuildSecrets(stager, 0)

	if err := e.Run(context.Background(), buildDispatch(row)); err != nil {
		t.Fatalf("Run(build): %v", err)
	}
	if cap.secretRef != "" {
		t.Errorf("no repo slug → empty secretRef, got %q", cap.secretRef)
	}
	if stager.calls() != 0 {
		t.Errorf("stager must not be called without a repo slug, got %d calls", stager.calls())
	}
}

func TestRetryAuthFailedBuild_ReMintsAndReTriggersAtCommit(t *testing.T) {
	cap := &buildTrigger{}
	stager := &fakeStager{ref: stagedRef}
	e := newBuildExecutor(ocWithBuildCapture(cap), &sourcecontrol.GitRepository{RepoSlug: "acme-widgets"}, newFakeExecRepo()).
		WithBuildSecrets(stager, 0)

	row := buildRow("e1")
	row.Status = string(taskmeta.ExecRunning)
	newRun, err := e.RetryAuthFailedBuild(context.Background(), row)
	if err != nil {
		t.Fatalf("RetryAuthFailedBuild: %v", err)
	}
	if newRun == "" || newRun != cap.runName {
		t.Errorf("retry must return the fresh run name, got %q (trigger run %q)", newRun, cap.runName)
	}
	if cap.sha != "deadbeef" || cap.component != "order-service" || cap.secretRef != stagedRef {
		t.Errorf("retry re-triggered wrong: sha=%q component=%q ref=%q", cap.sha, cap.component, cap.secretRef)
	}
	if stager.calls() != 1 {
		t.Errorf("retry must re-mint the secret once, got %d", stager.calls())
	}
}

func TestRetryAuthFailedBuild_MissingFacts_Errors(t *testing.T) {
	e := newBuildExecutor(ocWithBuildCapture(&buildTrigger{}), &sourcecontrol.GitRepository{RepoSlug: "acme-widgets"}, newFakeExecRepo()).
		WithBuildSecrets(&fakeStager{}, 0)

	if _, err := e.RetryAuthFailedBuild(context.Background(), &delivery.Execution{ID: "e1", Component: "x"}); err == nil {
		t.Error("retry without CommitSHA must error")
	}
	if _, err := e.RetryAuthFailedBuild(context.Background(), &delivery.Execution{ID: "e1", CommitSHA: "sha"}); err == nil {
		t.Error("retry without Component must error")
	}
}
