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

package devflow

import (
	"context"
	"errors"

	"github.com/wso2/aep/aep-api/models"
)

// errNotConfigured is returned by an activity whose adapter was not wired
// (e.g. Temporal enabled but a dependency missing). Non-retryable in effect —
// the workflow surfaces it rather than looping.
var errNotConfigured = errors.New("devflow: activity dependency not configured")

// Activities is the set of Temporal activities the devflow workflows invoke.
// Every activity is a THIN adapter over an existing aep-api service (the
// funnel, genai turns, the plan service, the issue service) plus the
// workflow_runs lookup index — no task logic is reimplemented here. Fields
// are added as later phases wire each adapter; the workflow-run index
// activities land first because the workflows record themselves on entry
// (the validation orchestrator records after resolving its issue; its lane
// children record no rows).
type Activities struct {
	runs               WorkflowRunStore
	dispatcher         CodingDispatcher
	merger             PRMerger
	spec               SpecValidator
	planner            Planner
	validator          Validator
	validationResolver ValidationResolver
	provisioner        BuildProvisioner
}

// Deps carries the activity adapters. Any field may be nil in narrow contexts
// (the corresponding activity then returns a not-configured error); the app
// root wires all of them.
type Deps struct {
	Runs               WorkflowRunStore
	Dispatcher         CodingDispatcher
	Merger             PRMerger
	Spec               SpecValidator
	Planner            Planner
	Validator          Validator
	ValidationResolver ValidationResolver
	Provisioner        BuildProvisioner
}

// NewActivities wires the activity adapters.
func NewActivities(d Deps) *Activities {
	return &Activities{
		runs:               d.Runs,
		dispatcher:         d.Dispatcher,
		merger:             d.Merger,
		spec:               d.Spec,
		planner:            d.Planner,
		validator:          d.Validator,
		validationResolver: d.ValidationResolver,
		provisioner:        d.Provisioner,
	}
}

// RecordWorkflowRunInput is the first-activity payload every workflow emits so
// the lookup index (workflow_runs) knows the run exists and how to signal it.
type RecordWorkflowRunInput struct {
	WorkflowID       string `json:"workflowId"`
	RunID            string `json:"runId"`
	Kind             string `json:"kind"` // dev | task | validation
	OrgID            string `json:"orgId"`
	ProjectID        string `json:"projectId"`
	Tag              string `json:"tag,omitempty"`
	Repo             string `json:"repo,omitempty"`
	IssueNumber      int    `json:"issueNumber,omitempty"`
	ParentWorkflowID string `json:"parentWorkflowId,omitempty"`
}

// RecordWorkflowRun upserts the workflow_runs row (idempotent on workflow
// retry). Called as the first activity of every workflow.
func (a *Activities) RecordWorkflowRun(ctx context.Context, in RecordWorkflowRunInput) error {
	return a.runs.Record(ctx, &models.DevflowRun{
		WorkflowID:       in.WorkflowID,
		RunID:            in.RunID,
		Kind:             in.Kind,
		OrgID:            in.OrgID,
		ProjectID:        in.ProjectID,
		Tag:              in.Tag,
		Repo:             in.Repo,
		IssueNumber:      in.IssueNumber,
		ParentWorkflowID: in.ParentWorkflowID,
		Status:           models.WorkflowStatusRunning,
	})
}

// SetWorkflowRunStatusInput marks a run terminal in the lookup index. Reason
// carries the failure detail for a `failed` status (empty otherwise) so the
// build summary can show WHY the run failed.
type SetWorkflowRunStatusInput struct {
	WorkflowID string `json:"workflowId"`
	Status     string `json:"status"` // completed | failed | canceled
	Reason     string `json:"reason,omitempty"`
}

// SetWorkflowRunStatus records a run's terminal status (+ failure reason) in the
// lookup index. Called as the final activity of the run-recording workflows.
func (a *Activities) SetWorkflowRunStatus(ctx context.Context, in SetWorkflowRunStatusInput) error {
	return a.runs.SetStatus(ctx, in.WorkflowID, in.Status, in.Reason)
}

// SetWorkflowRunTaskCountsInput carries a dev run's task tally — absolute
// values derived from the workflow's deterministic task state, so a retried
// activity re-writes the same numbers instead of double-counting. RunID
// scopes the write to this execution (a same-tag rebuild reuses the
// workflow id).
type SetWorkflowRunTaskCountsInput struct {
	WorkflowID string `json:"workflowId"`
	RunID      string `json:"runId"`
	Total      int    `json:"total"`
	Done       int    `json:"done"`
	Failed     int    `json:"failed"`
}

// SetWorkflowRunTaskCounts records the dev run's task tally in the lookup
// index — the overview build stage's counts source. Written once with the
// plan size, then per task transition, frozen when the run ends.
func (a *Activities) SetWorkflowRunTaskCounts(ctx context.Context, in SetWorkflowRunTaskCountsInput) error {
	return a.runs.SetTaskCounts(ctx, in.WorkflowID, in.RunID, in.Total, in.Done, in.Failed)
}

// DispatchCodingInput identifies the Task to dispatch a coding attempt for.
type DispatchCodingInput struct {
	OrgID     string `json:"orgId"`
	ProjectID string `json:"projectId"`
	Repo      string `json:"repo"`
	Issue     int    `json:"issue"`
}

// DispatchCoding triggers the coding attempt through the funnel and returns
// the admitted execution id. Idempotent: a re-dispatch while a coding
// Execution is already active is a no-op in the funnel (TryAdmit loses the
// race) and returns the active row's id.
func (a *Activities) DispatchCoding(ctx context.Context, in DispatchCodingInput) (string, error) {
	if a.dispatcher == nil {
		return "", errNotConfigured
	}
	return a.dispatcher.DispatchCoding(ctx, in.OrgID, in.ProjectID, in.Repo, in.Issue)
}

// MergePRInput identifies the pull request to squash-merge.
type MergePRInput struct {
	OrgID     string `json:"orgId"`
	ProjectID string `json:"projectId"`
	PRNumber  int    `json:"prNumber"`
}

// MergePR squash-merges the Task's pull request (the auto merge-pr gate).
func (a *Activities) MergePR(ctx context.Context, in MergePRInput) error {
	if a.merger == nil {
		return errNotConfigured
	}
	return a.merger.MergePR(ctx, in.OrgID, in.ProjectID, in.PRNumber)
}

// ProjectRef identifies a project for the dev-workflow activities.
type ProjectRef struct {
	OrgID     string `json:"orgId"`
	ProjectID string `json:"projectId"`
}

// ValidateSpecInput carries the project + tag for the pre-plan spec check.
type ValidateSpecInput struct {
	OrgID     string `json:"orgId"`
	ProjectID string `json:"projectId"`
	Tag       string `json:"tag"`
}

// ValidateSpecAtTag re-runs the whole-spec hard gate at the tag this run
// builds — the defensive check that what the workflow plans from is buildable.
func (a *Activities) ValidateSpecAtTag(ctx context.Context, in ValidateSpecInput) error {
	if a.spec == nil {
		return errNotConfigured
	}
	return a.spec.ValidateSpecAtTag(ctx, in.OrgID, in.ProjectID, in.Tag)
}

// RunPlan runs task planning and returns the planned tasks.
func (a *Activities) RunPlan(ctx context.Context, in ProjectRef) ([]PlannedTask, error) {
	if a.planner == nil {
		return nil, errNotConfigured
	}
	return a.planner.RunPlan(ctx, in.OrgID, in.ProjectID)
}

// ValidateInput carries the project + tag for the validation step.
type ValidateInput struct {
	OrgID     string `json:"orgId"`
	ProjectID string `json:"projectId"`
	Tag       string `json:"tag"`
}

// Validate runs the post-execution validation step.
func (a *Activities) Validate(ctx context.Context, in ValidateInput) error {
	if a.validator == nil {
		return errNotConfigured
	}
	return a.validator.Validate(ctx, in.OrgID, in.ProjectID, in.Tag)
}

// ResolveValidationTask ensures the project's aep:validation Task exists
// (idempotent) and returns its open issue number, or 0 when there are no
// acceptance criteria (the validating phase then skips the validation run).
func (a *Activities) ResolveValidationTask(ctx context.Context, in ProjectRef) (int, error) {
	if a.validationResolver == nil {
		return 0, errNotConfigured
	}
	return a.validationResolver.ResolveValidationTask(ctx, in.OrgID, in.ProjectID)
}

// ProvisionDepsInput carries the project + tag + resolved provisioning payload
// the dev workflow authors the dependencies from (issue #164).
type ProvisionDepsInput struct {
	OrgID     string           `json:"orgId"`
	ProjectID string           `json:"projectId"`
	Tag       string           `json:"tag"`
	Inputs    []ProvisionInput `json:"inputs,omitempty"`
}

// ProvisionDependencies authors the project's dependencies by kind from the
// build drawer inputs (mint gates → external sync, platform-resource async) AND
// reconciles any provision gate whose dependency is already Ready but was left
// un-completed by a prior build (self-heal — issue #164). It always runs when a
// provisioner is wired, even with no drawer inputs, so a project stranded on an
// orphaned gate recovers on its next build. Per-dependency failures come back as
// data; an infra error surfaces as the activity error (retry / fail the run).
func (a *Activities) ProvisionDependencies(ctx context.Context, in ProvisionDepsInput) ([]ProvisionFailure, error) {
	if a.provisioner == nil {
		// No provisioner wired (degraded boot / tests): a build that needs to
		// author inputs cannot proceed; a build with nothing to author (and so
		// nothing to reconcile through the provisioner) is a safe no-op.
		if len(in.Inputs) > 0 {
			return nil, errNotConfigured
		}
		return nil, nil
	}
	return a.provisioner.ProvisionForBuild(ctx, in.OrgID, in.ProjectID, in.Tag, in.Inputs)
}
