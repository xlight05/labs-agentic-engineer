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

package projects

// The #184 stage aggregates: one GET /projects/{name}/status cheap enough to
// poll at 5s serves the whole overview pipeline. Poll-path budget: no GitHub
// API, no Temporal query, no origin git fetch — the git source is the local
// mirror snapshot (spec.StatusSnapshot), the build source is one milestone_runs
// row read, the deploy source is one org-scoped OpenChoreo call. See
// internal/projects/README.md.

import (
	"context"
	"errors"
	"fmt"

	"github.com/wso2/aep/aep-api/internal/gen"

	"golang.org/x/sync/errgroup"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/spec"
)

// Stage status vocabularies (the contract enums).
const (
	buildIdle      = "idle"
	buildRunning   = "running"
	buildFailed    = "failed"
	buildSucceeded = "succeeded"

	deployNone      = "none"
	deployDeploying = "deploying"
	deployDeployed  = "deployed"
	deployFailed    = "failed"

	// Coarse validation state (the deploy.validation contract enum): none (the
	// run has not reached validation, or the project has no acceptance
	// criteria), running, completed, failed.
	validationNone      = "none"
	validationRunning   = "running"
	validationCompleted = "completed"
	validationFailed    = "failed"
)

// milestoneRunRows is the narrow port over the milestone_runs index: the status
// read plus the project-delete purge. An app-root adapter over
// delivery.MilestoneRunRepository (which also purges the cycle records) is what
// satisfies it.
type milestoneRunRows interface {
	// ListByProject returns the project's runs newest-first.
	ListByProject(ctx context.Context, orgID, projectID string) ([]delivery.MilestoneRun, error)
	DeleteByProject(ctx context.Context, orgID, projectID string) error
}

// bindingsReader is the narrow status-read port over OpenChoreo release
// bindings. openchoreo.ComponentClient satisfies it.
type bindingsReader interface {
	ListProjectReleaseBindings(ctx context.Context, orgName, projectName string) ([]openchoreo.ReleaseBindingSummary, error)
}

// SetStageSources wires the build/deploy stage inputs at the composition
// root. On a ready repo GetProjectStatus fails when either is missing — the
// stages are contract-required and never silently fabricated (D7).
func (s *Service) SetStageSources(runs milestoneRunRows, bindings bindingsReader) {
	s.runReader = runs
	s.bindingsReader = bindings
}

// populateStages fills the three nested aggregates plus the flat artifact
// fields from three concurrently-read sources — strict join: any source
// failing fails the whole read (the console's poller keeps last-good data
// and retries; the endpoint never fabricates emptiness).
func (s *Service) populateStages(ctx context.Context, orgName, projectName string, status *gen.ProjectStatus) error {
	if s.artifactSvc == nil || s.runReader == nil || s.bindingsReader == nil {
		return fmt.Errorf("project status: stage sources not wired")
	}

	var (
		snap        *spec.StatusSnapshot
		runs        []delivery.MilestoneRun
		bindings    []openchoreo.ReleaseBindingSummary
		deployVer   string
		deployTotal int64
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		if snap, err = s.artifactSvc.StatusSnapshot(gctx, orgName, projectName); err != nil {
			return fmt.Errorf("git snapshot: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		var err error
		if runs, err = s.runReader.ListByProject(gctx, orgName, projectName); err != nil {
			return fmt.Errorf("list milestone runs: %w", err)
		}
		// The deploy denominator depends only on the rows, so read it here —
		// overlapped with the OC call instead of serial after the join.
		// deploy.version = the newest SUCCEEDED spec run's version: what the
		// platform last finished delivering (a running v2 does not unseat a
		// live v1).
		for i := range runs {
			if runs[i].Origin == delivery.RunOriginSpecBuild && runs[i].State == delivery.RunStateSucceeded {
				deployVer = runs[i].MilestoneTitle
				break
			}
		}
		if deployVer == "" {
			return nil
		}
		total, err := s.artifactSvc.ComponentCountAtTag(gctx, orgName, projectName, deployVer)
		switch {
		case errors.Is(err, spec.ErrSpecTagNotFound):
			// A vanished tag (deleted on GitHub, or a stale run row from a
			// recreated project) is a DATA state, not a source outage — the
			// strict join is for outages. Degrade to an unknown denominator
			// instead of bricking every poll.
			return nil
		case err != nil:
			return fmt.Errorf("component count at %s: %w", deployVer, err)
		}
		deployTotal = int64(total)
		return nil
	})
	g.Go(func() error {
		var err error
		if bindings, err = s.bindingsReader.ListProjectReleaseBindings(gctx, orgName, projectName); err != nil {
			return fmt.Errorf("list release bindings: %w", err)
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		return err
	}

	// Spec stage + flat artifact fields: one snapshot, same semantics as the
	// retired per-call reads — minus their per-poll origin fetches.
	status.Spec = gen.SpecStage{
		Exists:  snap.HasSpec,
		Version: snap.SpecVersion,
		Dirty:   snap.SpecDirty,
		Design:  snap.HasDesign,
	}
	applyFlatArtifactFields(status, snap)

	// Build stage: the newest milestone run (ListByProject is newest-first).
	//
	// The task tally is deliberately ZERO. Its only honest source is the
	// version's milestone on GitHub, and this endpoint is polled at 5s — a
	// per-poll GitHub read is exactly what the #184 budget forbids. The console
	// renders per-version counts from the list-tasks response it already holds.
	var latest *delivery.MilestoneRun
	if len(runs) > 0 {
		latest = &runs[0]
		status.Build.Version = latest.MilestoneTitle
		status.Build.Status = buildStageStatus(latest.State)
		// A failed VALIDATION cycle is not a build failure: every coding cycle
		// landed and the failure already rides deploy.validation=failed below.
		// Without this the overview says "build failed" while the validation chip
		// contradicts it.
		if latest.TerminalReason == delivery.RunReasonValidationFailed {
			status.Build.Status = buildSucceeded
		}
	}

	// Deploy stage: version + denominator computed alongside the row read
	// above (the design at the DEPLOYED tag — what that build actually
	// implemented; HEAD may hold newer spec edits no build has seen); status
	// + numerator from the dev-environment bindings. ready and total come
	// from independent sources, so ready > total is possible transiently
	// (component removed from the design between builds) — informational
	// counts, deliberately not reconciled.
	status.Deploy.Version = deployVer
	status.Deploy.Components.Total = deployTotal
	var dev []openchoreo.ReleaseBindingSummary
	for _, b := range bindings {
		if b.Environment == openchoreo.DevEnvironmentName && !b.Undeploy {
			dev = append(dev, b)
		}
	}
	status.Deploy.Status = deployStageStatus(dev)
	status.Deploy.Components.Ready = int64(countReady(dev))

	// Validation: the newest run'"'"'s own verdict — a column on the row already
	// read above, so the poll costs nothing extra. The report itself, and the
	// per-cycle detail behind it, live on the version'"'"'s run story
	// (list-build-runs), which is where the console'"'"'s validation surface reads
	// them; validationUrl/validationIssue are therefore no longer served here.
	status.Deploy.Validation = gen.DeployStageValidation(validationStageStatus(latest))
	return nil
}

// validationStageStatus maps the newest run'"'"'s validation verdict onto the
// deploy.validation enum.
//
// The verdict is written exactly once, when the validation cycle settles, so an
// empty verdict on a LIVE run means the run has not reached validation yet
// (running), and an empty verdict on a settled run means it never got there —
// which reads as none, the same as a project with no acceptance criteria.
// A skipped verdict (no criteria, or a cycle that merged without a report) is
// also none: nothing was validated, and nothing failed.
func validationStageStatus(run *delivery.MilestoneRun) string {
	if run == nil {
		return validationNone
	}
	switch run.ValidationVerdict {
	case delivery.ValidationVerdictPassed:
		return validationCompleted
	case delivery.ValidationVerdictFailed:
		return validationFailed
	case delivery.ValidationVerdictSkipped:
		return validationNone
	}
	if delivery.IsTerminalRunState(run.State) {
		return validationNone
	}
	return validationRunning
}

// applyFlatArtifactFields recomputes the pre-#184 flat fields from the
// snapshot, outputs identical to the retired per-call reads: hasSpec from
// the requirements listing; specStatus approved on any v<N> tag, draft on an
// unversioned spec; designStatus approved on any legacy v<N>-<M> tag;
// hasDesign only ever true when a spec exists (the old ladder returned at
// "prompt" before reading the design); the phase ladder unchanged. One
// accepted deviation: a design.md with malformed frontmatter counts as
// present here, where the old ReadDesign failed the whole status read — see
// spec.StatusSnapshot.HasDesign. HasTasks stays false — tasks are
// counted live from GitHub, never here.
func applyFlatArtifactFields(status *gen.ProjectStatus, snap *spec.StatusSnapshot) {
	status.HasSpec = snap.HasSpec
	switch {
	case snap.SpecVersion != "":
		status.SpecStatus = "approved"
	case snap.HasSpec:
		status.SpecStatus = "draft"
	}
	if snap.HasDesignTag {
		status.DesignStatus = "approved"
	}
	if !snap.HasSpec {
		status.Phase = "prompt"
		return
	}
	status.HasDesign = snap.HasDesign
	if !snap.HasDesign {
		status.Phase = "spec"
		return
	}
	status.Phase = "tasks"
}

// buildStageStatus maps a milestone run'"'"'s state onto the BuildStage enum. A
// version'"'"'s delivery IS its run: it is running while the run is (waiting between
// cycles included — the version is still being delivered), and it succeeds or
// fails exactly when the run settles.
func buildStageStatus(state string) string {
	switch state {
	case delivery.RunStateSucceeded:
		return buildSucceeded
	case delivery.RunStateFailed, delivery.RunStateCancelled:
		return buildFailed
	default: // waiting | running
		return buildRunning
	}
}

// bindingFailureReasons is the aggregate Ready condition's terminal-failure
// vocabulary (OpenChoreo v1.1.1 releasebinding controller). Anything else
// not-ready reads as still-progressing — the forgiving default: an unknown
// or missing reason renders "deploying", never a false "failed".
var bindingFailureReasons = map[string]bool{
	"RenderingFailed":             true,
	"ResourceApplyFailed":         true,
	"ResourcesDegraded":           true,
	"ResourcesNotReady":           true,
	"JobFailed":                   true,
	"InvalidReleaseConfiguration": true,
	"ReleaseUpdateFailed":         true,
	"ReleaseOwnershipConflict":    true,
	"ComponentReleaseNotFound":    true,
	"EnvironmentNotFound":         true,
	"DataPlaneNotFound":           true,
	"DataPlaneNotConfigured":      true,
	"ComponentNotFound":           true,
	"ProjectNotFound":             true,
}

func bindingReady(b openchoreo.ReleaseBindingSummary) bool { return b.ReadyStatus == "True" }

func bindingFailed(b openchoreo.ReleaseBindingSummary) bool {
	return !bindingReady(b) && bindingFailureReasons[b.ReadyReason]
}

// deployStageStatus derives the stage from binding conditions alone,
// precedence failed > deploying > deployed; no bindings → none. The design
// denominator never gates the status (a designed-but-never-built component
// shows "deployed · 2/3", not forever-"deploying").
func deployStageStatus(dev []openchoreo.ReleaseBindingSummary) string {
	if len(dev) == 0 {
		return deployNone
	}
	progressing := false
	for _, b := range dev {
		if bindingFailed(b) {
			return deployFailed
		}
		if !bindingReady(b) {
			progressing = true
		}
	}
	if progressing {
		return deployDeploying
	}
	return deployDeployed
}

func countReady(dev []openchoreo.ReleaseBindingSummary) int {
	n := 0
	for _, b := range dev {
		if bindingReady(b) {
			n++
		}
	}
	return n
}
