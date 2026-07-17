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

	"github.com/wso2/aep/aep-api/models"
)

// WorkflowRunStore is the narrow port the activities use to maintain the
// workflow_runs lookup index. Satisfied by
// repositories.WorkflowRunRepository. Kept as a devflow-local interface so
// the workflows/activities depend on a capability, not the concrete repo
// (and tests can fake it).
type WorkflowRunStore interface {
	Record(ctx context.Context, row *models.DevflowRun) error
	SetStatus(ctx context.Context, workflowID, status, reason string) error
	// SetTaskCounts writes the dev run's task tally as absolute values
	// (idempotent under activity retry — never an increment), scoped to one
	// execution so a same-tag rebuild cannot rewrite a prior run's frozen
	// tally.
	SetTaskCounts(ctx context.Context, workflowID, runID string, total, done, failed int) error
}

// CodingDispatcher triggers a coding attempt for a Task through the existing
// execution funnel (admission + gating + coding executor) and returns the
// admitted execution's id. Satisfied by an app-root adapter over
// execution.Funnel — devflow does not import the execution package. A
// nil dispatcher makes DispatchCoding a not-configured error.
type CodingDispatcher interface {
	DispatchCoding(ctx context.Context, orgID, projectID, repo string, issue int) (executionID string, err error)
}

// PRMerger squash-merges a Task's pull request through the existing issue
// service. Satisfied by an app-root adapter over sourcecontrol.IssueService.
type PRMerger interface {
	MergePR(ctx context.Context, orgID, projectID string, prNumber int) error
}

// SpecValidator re-runs the whole-spec hard gate at a `v<N>` tag — the dev
// workflow's defensive pre-plan check (the build endpoint validated the same
// spec before cutting the tag). Satisfied by an app-root adapter over the
// artifacts service.
type SpecValidator interface {
	ValidateSpecAtTag(ctx context.Context, orgID, projectID, tag string) error
}

// Planner runs task planning and returns the planned tasks (issue number +
// component key + dependsOn). Satisfied by an app-root adapter over the plan
// service + task reads.
type Planner interface {
	RunPlan(ctx context.Context, orgID, projectID string) ([]PlannedTask, error)
}

// Validator runs the post-execution consistency check: every design component
// has a Ready deployment (a reachable endpoint). Satisfied by an app-root
// adapter over the artifacts + component services.
type Validator interface {
	Validate(ctx context.Context, orgID, projectID, tag string) error
}

// ValidationResolver ensures the project's aep:validation Task exists
// (idempotent) and returns its open issue number, or 0 when there are no
// acceptance criteria (nothing to validate). Satisfied by an app-root adapter
// over the validation service; devflow does not import the validation package.
type ValidationResolver interface {
	ResolveValidationTask(ctx context.Context, orgID, projectID string) (issue int, err error)
}

// BuildProvisioner authors the project's dependencies from the build drawer
// inputs the dev workflow carries (issue #164): it mints the aep:provision gate
// issues and authors each dependency by kind (external synchronously,
// platform-resource async). Satisfied by an app-root adapter over the design +
// provisioning features — devflow imports neither. A returned error retries the
// activity; per-dependency failures are returned as data (ProvisionFailure).
type BuildProvisioner interface {
	ProvisionForBuild(ctx context.Context, orgID, projectID, tag string, inputs []ProvisionInput) ([]ProvisionFailure, error)
}

// ProvisionFailure is one dependency's provisioning failure surfaced to the
// workflow (data, not an activity error). It carries no secret values. This is
// devflow's own type — the provisioning package defines a field-identical twin;
// the app-root adapter maps between them (a feature must not import the workflow
// orchestrator).
type ProvisionFailure struct {
	Component  string
	Dependency string
	Reason     string
}
