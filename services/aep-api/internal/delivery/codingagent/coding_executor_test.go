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
	"strings"
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

// --- §9 runner contract: the milestone-keyed dispatch prompt ----------------

// TestBuildPrompt_IsAMilestoneReferenceOnly pins the §9 contract: the dispatch
// prompt names the milestone and defers EVERY step to the versioned `aep`
// skill. It must not name an issue, a branch, or a PR-body token — those would
// version with the BFF binary instead of with the skill.
func TestBuildPrompt_IsAMilestoneReferenceOnly(t *testing.T) {
	got := buildPrompt(12, "v3")

	if !strings.Contains(got, "milestone 12") {
		t.Errorf("prompt must name the milestone number, got %q", got)
	}
	if !strings.Contains(got, `"v3"`) {
		t.Errorf("prompt must name the milestone title (quoted, as gh --milestone matches it), got %q", got)
	}
	if !strings.Contains(got, "`aep` skill") {
		t.Errorf("prompt must defer the procedure to the aep skill, got %q", got)
	}
	for _, banned := range []string{"issue:", "issues/", "Closes #", "Resolves #", "git checkout", "gh pr create"} {
		if strings.Contains(got, banned) {
			t.Errorf("prompt must carry no procedure/issue anchor, but contains %q: %q", banned, got)
		}
	}
}

// TestRunCoding_WithoutMilestone_RefusesBeforeAnySideEffect pins the fail-fast:
// a coding dispatch with no milestone reference is refused before the
// component-ensure pre-flight runs, so a mis-wired caller cannot provision
// anything or launch a runner whose prompt names milestone 0.
func TestRunCoding_WithoutMilestone_RefusesBeforeAnySideEffect(t *testing.T) {
	ensurer := &fakeEnsurer{}
	row := codingRow("c1")
	e := codingExecutorFor(t, ensurer, &ocmocks.ComponentClientMock{}, row)

	req := codingDispatch(row)
	req.MilestoneNumber = 0
	req.MilestoneTitle = ""

	err := e.Run(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "milestone reference") {
		t.Fatalf("a coding dispatch without a milestone must be refused, got %v", err)
	}
	if len(ensurer.calls()) != 0 {
		t.Errorf("refusal must precede the component-ensure pre-flight, got %v", ensurer.calls())
	}
}

// TestBuildValidationPrompt_StaysIssueAnchored pins the other half of §9: the
// validation dispatch is NOT re-keyed — one validation issue, one run.
func TestBuildValidationPrompt_StaysIssueAnchored(t *testing.T) {
	got := buildValidationPrompt("https://github.com/acme/widgets/issues/9", 9)

	if !strings.Contains(got, "https://github.com/acme/widgets/issues/9") {
		t.Errorf("validation prompt must name its issue URL, got %q", got)
	}
	if !strings.Contains(got, "Closes #9") {
		t.Errorf("validation prompt must keep its Closes #N link contract, got %q", got)
	}
	if strings.Contains(got, "milestone") {
		t.Errorf("validation dispatch must stay issue-anchored, got %q", got)
	}
}

// TestRunCoding_ValidationWithoutProxy_RefusesWithTheRealReason pins the
// deliberate keep: with one runner image the refusal is no longer about the
// image, it is about K8sJobInput carrying no AEP_TASK_KIND or deadline.
func TestRunCoding_ValidationWithoutProxy_RefusesWithTheRealReason(t *testing.T) {
	row := codingRow("v1")
	row.Component = ""
	e := codingExecutorFor(t, &fakeEnsurer{}, &ocmocks.ComponentClientMock{}, row)

	req := codingDispatch(row)
	req.Task.Class = taskmeta.ClassValidation
	req.MilestoneNumber, req.MilestoneTitle = 0, "" // validation is issue-anchored

	err := e.Run(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "cluster-gateway-proxy path") {
		t.Fatalf("validation without the proxy path must be refused, got %v", err)
	}
	if strings.Contains(err.Error(), "VALIDATION_RUNNER_IMAGE") {
		t.Errorf("the refusal must no longer blame a second image, got %v", err)
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
