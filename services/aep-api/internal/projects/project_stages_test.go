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

// The #184 stage-aggregate derivation table, pinned row by row against fake
// poll sources. The fixture lives in project_service_test.go.
package projects

import (
	"context"
	"fmt"
	"testing"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/spec"
)

func devBinding(name, readyStatus, readyReason string) openchoreo.ReleaseBindingSummary {
	return openchoreo.ReleaseBindingSummary{
		ComponentName: name,
		Environment:   "development",
		ReadyStatus:   readyStatus,
		ReadyReason:   readyReason,
	}
}

func mustStatus(t *testing.T, fx statusFixture) *gen.ProjectStatus {
	t.Helper()
	st, err := fx.service().GetProjectStatus(context.Background(), "acme", "web")
	if err != nil {
		t.Fatalf("GetProjectStatus: %v", err)
	}
	return st
}

// specRun builds a spec-build milestone run for the version `tag` in `state`.
// A version's delivery IS its run, so these rows are the whole build stage.
func specRun(tag, state string) delivery.MilestoneRun {
	return delivery.MilestoneRun{
		MilestoneNumber: len(tag),
		MilestoneTitle:  tag,
		Origin:          delivery.RunOriginSpecBuild,
		State:           state,
	}
}

// TestStageDerivation_FullPipeline drives all three aggregates at once: a v2
// run in flight over a deployed v1 — the mid-flight overview.
func TestStageDerivation_FullPipeline(t *testing.T) {
	t.Parallel()
	fx := statusFixture{
		snap: spec.StatusSnapshot{
			HasSpec:     true,
			HasDesign:   true,
			SpecVersion: "v2",
			SpecDirty:   true,
		},
		runs: []delivery.MilestoneRun{
			specRun("v2", delivery.RunStateRunning),
			specRun("v1", delivery.RunStateSucceeded),
		},
		bindings: []openchoreo.ReleaseBindingSummary{
			devBinding("api", "True", "Ready"),
			devBinding("web", "False", "ResourcesProgressing"),
			{ComponentName: "api", Environment: "production", ReadyStatus: "True", ReadyReason: "Ready"}, // ignored: not dev
		},
		counts: map[string]int{"v1": 3},
	}
	st := mustStatus(t, fx)

	if want := (gen.SpecStage{Exists: true, Version: "v2", Dirty: true, Design: true}); st.Spec != want {
		t.Errorf("spec = %+v, want %+v", st.Spec, want)
	}

	if st.Build.Version != "v2" || st.Build.Status != "running" {
		t.Errorf("build = %s/%s, want v2/running", st.Build.Version, st.Build.Status)
	}
	// There is no task tally on this aggregate at all: its only honest source is
	// GitHub, and this endpoint is polled at 5s. The console renders counts from
	// the list-tasks response it already holds.

	if st.Deploy.Version != "v1" {
		t.Errorf("deploy version = %q, want v1 (newest SUCCEEDED run, not the running v2)", st.Deploy.Version)
	}
	if st.Deploy.Status != "deploying" {
		t.Errorf("deploy status = %q, want deploying (one dev binding not ready)", st.Deploy.Status)
	}
	if st.Deploy.Components.Total != 3 || st.Deploy.Components.Ready != 1 {
		t.Errorf("deploy components = %+v, want 3 total (design at v1) / 1 ready", st.Deploy.Components)
	}
}

// TestBuildStage_RunStateMapping pins the run-state → BuildStage enum table. A
// version is "running" for as long as its run is live — waiting between cycles
// included, because the version is still being delivered.
func TestBuildStage_RunStateMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		runs       []delivery.MilestoneRun
		wantVer    string
		wantStatus string
	}{
		{name: "no rows → idle", wantStatus: "idle"},
		{name: "succeeded", runs: []delivery.MilestoneRun{specRun("v3", delivery.RunStateSucceeded)}, wantVer: "v3", wantStatus: "succeeded"},
		{name: "failed", runs: []delivery.MilestoneRun{specRun("v3", delivery.RunStateFailed)}, wantVer: "v3", wantStatus: "failed"},
		{name: "cancelled → failed", runs: []delivery.MilestoneRun{specRun("v3", delivery.RunStateCancelled)}, wantVer: "v3", wantStatus: "failed"},
		{name: "running", runs: []delivery.MilestoneRun{specRun("v3", delivery.RunStateRunning)}, wantVer: "v3", wantStatus: "running"},
		{name: "waiting between cycles is still running", runs: []delivery.MilestoneRun{specRun("v3", delivery.RunStateWaiting)}, wantVer: "v3", wantStatus: "running"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A succeeded run becomes deploy.version → the denominator read
			// runs; give it a count so this test stays about the build row.
			st := mustStatus(t, statusFixture{runs: tc.runs, counts: map[string]int{"v3": 4}})
			if st.Build.Version != tc.wantVer || st.Build.Status != tc.wantStatus {
				t.Errorf("build = %s/%s, want %s/%s", st.Build.Version, st.Build.Status, tc.wantVer, tc.wantStatus)
			}
		})
	}
}

// A run that failed BECAUSE its validation cycle failed reports build=succeeded:
// every coding cycle landed, and the failure already rides
// deploy.validation=failed. The carve-out keys on the run's own terminal
// reason, which names exactly one failure class — so no tally guard or recency
// heuristic is needed any more.
func TestBuildStage_ValidationFailureAttribution(t *testing.T) {
	t.Parallel()
	failedFor := func(reason string) []delivery.MilestoneRun {
		run := specRun("v1", delivery.RunStateFailed)
		run.TerminalReason = reason
		run.ValidationVerdict = delivery.ValidationVerdictFailed
		return []delivery.MilestoneRun{run}
	}
	cases := []struct {
		name           string
		runs           []delivery.MilestoneRun
		wantBuild      string
		wantValidation string
	}{
		{
			name:      "validation-attributed failure → build succeeded",
			runs:      failedFor(delivery.RunReasonValidationFailed),
			wantBuild: "succeeded", wantValidation: "failed",
		},
		{
			name:      "a budget failure is the build's own",
			runs:      failedFor(delivery.RunReasonRedispatchBudget),
			wantBuild: "failed", wantValidation: "failed",
		},
		{
			name:      "a plan failure is the build's own",
			runs:      failedFor(delivery.RunReasonPlanFailed),
			wantBuild: "failed", wantValidation: "failed",
		},
		{
			name:      "a cancelled run is never carved out",
			runs:      []delivery.MilestoneRun{specRun("v1", delivery.RunStateCancelled)},
			wantBuild: "failed", wantValidation: "none",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := mustStatus(t, statusFixture{runs: tc.runs, counts: map[string]int{"v1": 1}})
			if st.Build.Status != tc.wantBuild {
				t.Errorf("build status = %q, want %q", st.Build.Status, tc.wantBuild)
			}
			if string(st.Deploy.Validation) != tc.wantValidation {
				t.Errorf("deploy.validation = %q, want %q", st.Deploy.Validation, tc.wantValidation)
			}
		})
	}
}

// TestDeployStage_ConditionMatrix pins the condition-driven status: failed >
// deploying > deployed, none without bindings; undeploy-state and non-dev
// bindings excluded; unknown reasons read as deploying, never failed.
func TestDeployStage_ConditionMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		bindings   []openchoreo.ReleaseBindingSummary
		wantStatus string
		wantReady  int64
	}{
		{name: "no bindings → none", wantStatus: "none"},
		{
			name: "all ready → deployed",
			bindings: []openchoreo.ReleaseBindingSummary{
				devBinding("api", "True", "Ready"),
				devBinding("web", "True", "Ready"),
			},
			wantStatus: "deployed",
			wantReady:  2,
		},
		{
			name: "any failure reason wins over progress",
			bindings: []openchoreo.ReleaseBindingSummary{
				devBinding("api", "True", "Ready"),
				devBinding("web", "False", "ResourcesProgressing"),
				devBinding("db", "False", "ResourceApplyFailed"),
			},
			wantStatus: "failed",
			wantReady:  1,
		},
		{
			name: "unknown not-ready reason → deploying (forgiving default)",
			bindings: []openchoreo.ReleaseBindingSummary{
				devBinding("api", "False", "SomeNewReason"),
			},
			wantStatus: "deploying",
		},
		{
			name: "absent Ready condition → deploying",
			bindings: []openchoreo.ReleaseBindingSummary{
				devBinding("api", "", ""),
			},
			wantStatus: "deploying",
		},
		{
			name: "undeploy-state binding excluded from status and counts",
			bindings: []openchoreo.ReleaseBindingSummary{
				devBinding("api", "True", "Ready"),
				{ComponentName: "web", Environment: "development", Undeploy: true, ReadyStatus: "False", ReadyReason: "ResourcesUndeployed"},
			},
			wantStatus: "deployed",
			wantReady:  1,
		},
		{
			name: "only non-dev bindings → none",
			bindings: []openchoreo.ReleaseBindingSummary{
				{ComponentName: "api", Environment: "production", ReadyStatus: "True", ReadyReason: "Ready"},
			},
			wantStatus: "none",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := mustStatus(t, statusFixture{bindings: tc.bindings})
			if st.Deploy.Status != tc.wantStatus {
				t.Errorf("deploy status = %q, want %q", st.Deploy.Status, tc.wantStatus)
			}
			if st.Deploy.Components.Ready != tc.wantReady {
				t.Errorf("ready = %d, want %d", st.Deploy.Components.Ready, tc.wantReady)
			}
		})
	}
}

// TestDeployStage_VanishedTagDegrades: a deploy tag missing from the local
// mirror (deleted on GitHub, or a stale run row) is a data state, not a
// source outage — the poll must stay alive with an unknown denominator, not
// 500 forever.
func TestDeployStage_VanishedTagDegrades(t *testing.T) {
	t.Parallel()
	fx := statusFixture{
		runs:     []delivery.MilestoneRun{specRun("v1", delivery.RunStateSucceeded)},
		countErr: fmt.Errorf("wrapped: %w", spec.ErrSpecTagNotFound),
		bindings: []openchoreo.ReleaseBindingSummary{devBinding("api", "True", "Ready")},
	}
	st := mustStatus(t, fx)
	if st.Deploy.Version != "v1" || st.Deploy.Status != "deployed" {
		t.Errorf("deploy = %s/%s, want v1/deployed (degraded, not failed)", st.Deploy.Version, st.Deploy.Status)
	}
	if st.Deploy.Components.Total != 0 || st.Deploy.Components.Ready != 1 {
		t.Errorf("components = %+v, want 0 total (unknown) / 1 ready", st.Deploy.Components)
	}
}

// TestDeployStage_VersionlessSkipsDenominator: with no completed run there is
// no deployed tag — the denominator read must not happen (the fixture errors
// on any unexpected tag) and counts stay 0/0.
func TestDeployStage_VersionlessSkipsDenominator(t *testing.T) {
	t.Parallel()
	fx := statusFixture{
		runs: []delivery.MilestoneRun{specRun("v1", delivery.RunStateRunning)},
	}
	st := mustStatus(t, fx)
	if st.Deploy.Version != "" {
		t.Errorf("deploy version = %q, want \"\" (no succeeded run)", st.Deploy.Version)
	}
	if st.Deploy.Components.Total != 0 || st.Deploy.Components.Ready != 0 {
		t.Errorf("components = %+v, want 0/0", st.Deploy.Components)
	}
}

// deploy.validation is the newest run's own VERDICT — a column on the row the
// build stage already read, so the poll costs nothing extra. The report and the
// per-cycle detail behind it live on the version's run story, which is why
// there is no validationUrl or validationIssue here any more.
func TestDeployStage_ValidationDerivation(t *testing.T) {
	t.Parallel()

	// Keep the run live (not succeeded) so this test stays about validation
	// rather than the deploy denominator.
	withVerdict := func(state, verdict string) []delivery.MilestoneRun {
		run := specRun("v1", state)
		run.ValidationVerdict = verdict
		return []delivery.MilestoneRun{run}
	}

	cases := []struct {
		name       string
		runs       []delivery.MilestoneRun
		wantStatus string
	}{
		{name: "no run at all → none", wantStatus: "none"},
		{
			name:       "live run, no verdict yet → running",
			runs:       withVerdict(delivery.RunStateRunning, ""),
			wantStatus: "running",
		},
		{
			name:       "settled run that never validated → none",
			runs:       withVerdict(delivery.RunStateFailed, ""),
			wantStatus: "none",
		},
		{name: "passed → completed", runs: withVerdict(delivery.RunStateRunning, delivery.ValidationVerdictPassed), wantStatus: "completed"},
		{name: "failed → failed", runs: withVerdict(delivery.RunStateRunning, delivery.ValidationVerdictFailed), wantStatus: "failed"},
		{
			name:       "skipped (no acceptance criteria) → none, never failed",
			runs:       withVerdict(delivery.RunStateRunning, delivery.ValidationVerdictSkipped),
			wantStatus: "none",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := mustStatus(t, statusFixture{runs: tc.runs})
			if string(st.Deploy.Validation) != tc.wantStatus {
				t.Errorf("validation = %q, want %q", st.Deploy.Validation, tc.wantStatus)
			}
		})
	}
}

// TestRepoNotReady_ZeroValueStages pins the short-circuit: the nested stages
// are contract-required, so a pending repo returns them present but
// zero-valued — idle build, no deploy, empty spec.
func TestRepoNotReady_ZeroValueStages(t *testing.T) {
	t.Parallel()
	repoSvc := &fakeRepoSvc{
		GetRepoFunc: func(context.Context, string, string) (*sourcecontrol.GitRepository, error) {
			return &sourcecontrol.GitRepository{Status: "pending", RepoURL: "https://github.com/o/r.git"}, nil
		},
	}
	// Sources deliberately unwired: the short-circuit must not touch them.
	svc := NewProjectService(nil, repoSvc, nil, nil, nil)
	st, err := svc.GetProjectStatus(context.Background(), "acme", "web")
	if err != nil {
		t.Fatalf("GetProjectStatus: %v", err)
	}
	if st.Phase != "repo-cloning" {
		t.Fatalf("phase = %q, want repo-cloning", st.Phase)
	}
	if st.Spec != (gen.SpecStage{}) {
		t.Errorf("spec = %+v, want zero-valued", st.Spec)
	}
	if st.Build.Status != "idle" || st.Build.Version != "" {
		t.Errorf("build = %+v, want idle zero-valued", st.Build)
	}
	if st.Deploy.Status != "none" || st.Deploy.Version != "" || st.Deploy.Components.Total != 0 {
		t.Errorf("deploy = %+v, want none zero-valued", st.Deploy)
	}
}
