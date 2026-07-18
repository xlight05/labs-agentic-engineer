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

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	ocmocks "github.com/wso2/aep/aep-api/internal/clients/openchoreo/mocks"
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/models"
)

// noDispatchPathErr is the error runCoding returns once it clears the pre-flight
// and reaches the dispatch stage with no path wired (proxy + k8sJob both unset).
// The pre-flight tests below assert this to prove control reached dispatch: they
// exercise the component-ensure / runtime-config pre-flight, not any particular
// dispatch mechanism (the K8s Job / proxy paths need cluster infra to unit-test).
const noDispatchPathErr = "no coding-agent dispatch path configured"

// codingExecutorFor builds a coding executor with NO dispatch path wired (proxy
// and k8sJob both unset), plus the coding-dispatch fakes + a scripted ensurer, so
// runCoding runs the pre-flight and then fails at the dispatch stage with
// noDispatchPathErr — enough to assert the pre-flight behavior in isolation.
func codingExecutorFor(t *testing.T, ensurer ComponentEnsurer, oc openchoreo.ComponentClient, row *models.Execution) *CodingExecutor {
	t.Helper()
	e := NewCodingExecutor(oc, fakeRepos{repo: &models.GitRepository{RepoURL: "https://github.com/acme/widgets"}},
		fakeIdentities{}, nil, fakeTokens{}, newFakeExecRepo(row), "http://git", "http://platform", nil, nil, nil, nil)
	return e.WithComponentEnsurer(ensurer)
}

func codingRow(id string) *models.Execution {
	return &models.Execution{
		ID: id, OrgID: "acme", ProjectID: "widgets", Repo: "acme/widgets", IssueNumber: 7,
		Kind: string(taskmeta.KindCoding), Status: string(taskmeta.ExecQueued), Component: "order-service",
	}
}

func codingDispatch(row *models.Execution) delivery.DispatchRequest {
	return delivery.DispatchRequest{
		Execution: row,
		Task: delivery.TaskFacts{
			OrgID: "acme", ProjectID: "widgets", Component: "order-service",
			IssueNumber: 7, IssueURL: "https://github.com/acme/widgets/issues/7",
		},
	}
}

func TestRunCoding_PreflightEnsuresComponent_BeforeDispatch(t *testing.T) {
	ensurer := &fakeEnsurer{}
	row := codingRow("c1")
	e := codingExecutorFor(t, ensurer, &ocmocks.ComponentClientMock{}, row)

	// A successful pre-flight hands control to the dispatch stage, which — with no
	// path wired — returns noDispatchPathErr. That error is the proof the
	// pre-flight ran and passed control on to dispatch.
	err := e.Run(context.Background(), codingDispatch(row))
	if err == nil || !strings.Contains(err.Error(), noDispatchPathErr) {
		t.Fatalf("expected to reach dispatch (no path configured), got %v", err)
	}
	if got := ensurer.calls(); len(got) != 1 || got[0] != [3]string{"acme", "widgets", "order-service"} {
		t.Fatalf("EnsureComponent must be called once with (org,project,component), got %v", got)
	}
}

func TestRunCoding_PreflightFailure_BlocksDispatch(t *testing.T) {
	ensurer := &fakeEnsurer{err: errors.New("design missing")}
	e := codingExecutorFor(t, ensurer, &ocmocks.ComponentClientMock{}, codingRow("c1"))

	// A pre-flight failure must block BEFORE the dispatch stage: the error names
	// the pre-flight and dispatch is never reached (no noDispatchPathErr).
	err := e.Run(context.Background(), codingDispatch(codingRow("c1")))
	if err == nil || !strings.Contains(err.Error(), "ensure component pre-flight") {
		t.Fatalf("pre-flight failure must block dispatch with a clear error, got %v", err)
	}
	if strings.Contains(err.Error(), noDispatchPathErr) {
		t.Error("dispatch must NOT be reached when the pre-flight fails")
	}
}

func TestRunCoding_ReDispatch_PreflightRunsEachTime(t *testing.T) {
	// The pre-flight is idempotent (EnsureComponent is 409-safe at the OC client),
	// so a re-dispatch simply calls it again and reaches dispatch again — no
	// duplicate, no pre-flight error.
	ensurer := &fakeEnsurer{}
	e := codingExecutorFor(t, ensurer, &ocmocks.ComponentClientMock{}, codingRow("c1"))
	for i := 0; i < 2; i++ {
		err := e.Run(context.Background(), codingDispatch(codingRow("c1")))
		if err == nil || !strings.Contains(err.Error(), noDispatchPathErr) {
			t.Fatalf("re-dispatch %d: expected to reach dispatch, got %v", i, err)
		}
	}
	if got := ensurer.calls(); len(got) != 2 {
		t.Fatalf("pre-flight must run on each dispatch (idempotent), got %d calls", len(got))
	}
}

func TestRunCoding_EmitsRuntimeConfig_AtEnsurePreflight(t *testing.T) {
	// The ensure pre-flight fires env-config.js emission best-effort. The web-app
	// gate lives inside the emitter (which self-no-ops for non-web-apps), so the
	// executor calls it unconditionally with the dispatched component's name.
	ensurer := &fakeEnsurer{}
	rc := &fakeRuntimeConfig{}
	row := codingRow("c1")
	e := codingExecutorFor(t, ensurer, &ocmocks.ComponentClientMock{}, row).WithComponentRuntimeConfig(rc)

	err := e.Run(context.Background(), codingDispatch(row))
	if err == nil || !strings.Contains(err.Error(), noDispatchPathErr) {
		t.Fatalf("expected to reach dispatch, got %v", err)
	}
	if got := rc.calls(); len(got) != 1 || got[0] != [3]string{"acme", "widgets", "order-service"} {
		t.Fatalf("EmitForComponent must be called once with (org,project,component), got %v", got)
	}
}

func TestRunCoding_RuntimeConfigEmitError_DoesNotFailDispatch(t *testing.T) {
	// Emission is best-effort: an emit failure warns but never aborts the run. The
	// flow still proceeds to the dispatch stage — here failing only for the unwired
	// path (noDispatchPathErr), NOT surfacing the emit error.
	ensurer := &fakeEnsurer{}
	rc := &fakeRuntimeConfig{err: errors.New("OC transient")}
	row := codingRow("c1")
	e := codingExecutorFor(t, ensurer, &ocmocks.ComponentClientMock{}, row).WithComponentRuntimeConfig(rc)

	err := e.Run(context.Background(), codingDispatch(row))
	if err == nil || !strings.Contains(err.Error(), noDispatchPathErr) {
		t.Fatalf("emit failure must not abort the run; expected to reach dispatch, got %v", err)
	}
	if strings.Contains(err.Error(), "OC transient") {
		t.Fatalf("emit error must be swallowed (best-effort), got %v", err)
	}
	// The emission must have been ATTEMPTED — otherwise this test also passes
	// when EmitForComponent is skipped entirely, proving nothing about the
	// swallow-after-attempt behavior.
	if got := rc.calls(); len(got) != 1 || got[0] != [3]string{"acme", "widgets", "order-service"} {
		t.Fatalf("EmitForComponent must be attempted once with (org,project,component), got %v", got)
	}
}

func TestRunBuild_ComponentMissing_ActionableError(t *testing.T) {
	// The build path does NOT upsert; a missing Component surfaces a clear,
	// actionable error wrapping openchoreo.ErrNotFound.
	oc := &ocmocks.ComponentClientMock{
		TriggerBuildAtCommitFunc: func(_ context.Context, _, _, _, _, _, _ string) (*gen.WorkflowRun, error) {
			return nil, openchoreo.ErrNotFound
		},
	}
	row := buildRow("b1")
	e := NewCodingExecutor(oc, fakeRepos{repo: &models.GitRepository{RepoURL: "https://github.com/acme/widgets"}},
		fakeIdentities{}, nil, fakeTokens{}, newFakeExecRepo(row), "http://git", "http://platform", nil, nil, nil, nil)

	err := e.Run(context.Background(), buildDispatch(row))
	if err == nil {
		t.Fatal("a missing Component must fail the build")
	}
	if !errors.Is(err, openchoreo.ErrNotFound) {
		t.Errorf("build error must wrap openchoreo.ErrNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "coding execution must run first") {
		t.Errorf("build error must be actionable (re-run coding), got %v", err)
	}
}
