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
// mirror snapshot (spec.StatusSnapshot), the build source is one
// workflow_runs row read, the deploy source is one org-scoped OpenChoreo
// call. See docs/design/project-status-stage-aggregates.md.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wso2/aep/aep-api/internal/gen"

	"golang.org/x/sync/errgroup"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
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

	// Coarse validation-run state (the deploy.validation contract enum): none
	// (no validation child — not reached, or no acceptance criteria), running,
	// completed (ran to completion), failed (mechanical failure).
	validationNone      = "none"
	validationRunning   = "running"
	validationCompleted = "completed"
	validationFailed    = "failed"
)

// devRunRows is the narrow port over the workflow_runs lookup index: the
// status read plus the project-delete purge.
// delivery.WorkflowRunRepository satisfies it.
type devRunRows interface {
	ListByProject(ctx context.Context, orgID, projectID, kind string) ([]delivery.DevflowRun, error)
	ValidationRunByParent(ctx context.Context, orgID, projectID, parentWorkflowID string) (*delivery.DevflowRun, error)
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
func (s *Service) SetStageSources(runs devRunRows, bindings bindingsReader) {
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
		snap          *spec.StatusSnapshot
		runs          []delivery.DevflowRun
		bindings      []openchoreo.ReleaseBindingSummary
		deployVer     string
		deployTotal   int64
		validationRun *delivery.DevflowRun
		validationPR  int
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
		if runs, err = s.runReader.ListByProject(gctx, orgName, projectName, delivery.WorkflowKindDev); err != nil {
			return fmt.Errorf("list dev runs: %w", err)
		}
		// Validation-run state for the newest dev run — cheap DB reads (no
		// GitHub), so it stays within the poll budget. Read here, before the
		// deploy-version early return below, so a first build that has reached
		// its validating phase (no completed run yet) still reports validation.
		if len(runs) > 0 {
			vr, verr := s.runReader.ValidationRunByParent(gctx, orgName, projectName, runs[0].WorkflowID)
			if verr != nil {
				return fmt.Errorf("validation run: %w", verr)
			}
			validationRun = vr
			// The validation PR number is recovered from the executions rows (the
			// succeeded coding row's "pr#" reason) — no live PR query.
			if vr != nil && s.execs != nil {
				byKind, eerr := s.execs.LatestPerKindScoped(gctx, orgName, vr.Repo, vr.IssueNumber)
				if eerr != nil {
					return fmt.Errorf("validation pr lookup: %w", eerr)
				}
				if coding := byKind[string(taskmeta.KindCoding)]; coding != nil &&
					coding.Status == string(taskmeta.ExecSucceeded) {
					validationPR = taskmeta.OpenPRNumber(coding.Reason)
				}
			}
		}
		// The deploy denominator depends only on the rows, so read it here —
		// overlapped with the OC call instead of serial after the join.
		// deploy.version = the newest COMPLETED dev run's tag: what the
		// platform last finished building (a running v2 does not unseat a
		// live v1).
		for _, r := range runs {
			if r.Status == delivery.WorkflowStatusCompleted {
				deployVer = r.Tag
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

	// Build stage: the newest dev run row (ListByProject is newest-first).
	// Counts are the run's own tally, written by the workflow and frozen at
	// terminal; `active` is computed, clamped so a lost total write can
	// never render negative.
	if len(runs) > 0 {
		row := runs[0]
		status.Build.Version = row.Tag
		status.Build.Status = buildStageStatus(row.Status)
		status.Build.Tasks.Total = int64(row.TasksTotal)
		status.Build.Tasks.Done = int64(row.TasksDone)
		status.Build.Tasks.Failed = int64(row.TasksFailed)
		if active := row.TasksTotal - row.TasksDone - row.TasksFailed; active > 0 {
			status.Build.Tasks.Active = int64(active)
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

	// Validation: the coarse run state of the newest build's validation child,
	// plus a link to its PR (the validation issue as a fallback before a PR
	// exists). Both derived from cheap DB reads above — no GitHub in the poll.
	status.Deploy.Validation = gen.DeployStageValidation(validationStageStatus(validationRun))
	status.Deploy.ValidationURL = validationURL(status.RepoURL, validationRun, validationPR)
	return nil
}

// validationStageStatus maps the validation child run's row status onto the
// deploy.validation enum. No child row → none (not reached, or no acceptance
// criteria). An unknown non-terminal status reads as running (in flight).
func validationStageStatus(run *delivery.DevflowRun) string {
	if run == nil {
		return validationNone
	}
	switch run.Status {
	case delivery.WorkflowStatusCompleted:
		return validationCompleted
	case delivery.WorkflowStatusFailed, delivery.WorkflowStatusCanceled:
		return validationFailed
	default: // running
		return validationRunning
	}
}

// validationURL builds the validation PR link from the repo's clone URL, or the
// validation issue as a fallback before the PR opens. Empty when there is no
// validation run or no repo URL to build from.
func validationURL(repoURL string, run *delivery.DevflowRun, prNumber int) string {
	if run == nil || repoURL == "" {
		return ""
	}
	base := strings.TrimSuffix(repoURL, ".git")
	if prNumber > 0 {
		return fmt.Sprintf("%s/pull/%d", base, prNumber)
	}
	return fmt.Sprintf("%s/issues/%d", base, run.IssueNumber)
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

// buildStageStatus maps a workflow_runs row status onto the BuildStage enum.
func buildStageStatus(rowStatus string) string {
	switch rowStatus {
	case delivery.WorkflowStatusCompleted:
		return buildSucceeded
	case delivery.WorkflowStatusFailed, delivery.WorkflowStatusCanceled:
		return buildFailed
	default: // running
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
