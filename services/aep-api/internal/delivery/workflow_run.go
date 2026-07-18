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

package delivery

import "time"

// WorkflowRun kinds and statuses (plain strings, matching the model
// convention — canonical values here, no separate enum package).
const (
	WorkflowKindDev  = "dev"
	WorkflowKindTask = "task"
	// WorkflowKindValidation is the dev run's validation-phase orchestrator
	// (ValidationFlowWorkflow): one row per validating phase, carrying the
	// project's validation issue and parented to the dev run. It owns the
	// issue's webhook signals (see RunningTaskByIssue) and its status is the
	// phase status the deployments board surfaces.
	WorkflowKindValidation = "validation"

	WorkflowStatusRunning   = "running"
	WorkflowStatusCompleted = "completed"
	WorkflowStatusFailed    = "failed"
	WorkflowStatusCanceled  = "canceled"
)

// WorkflowRun is the lookup index for Temporal devflow workflows — NOT the
// source of truth (Temporal is). Webhook handlers and watchers use it to
// resolve "which running workflow wants this event?" (a point lookup by
// (repo, issue_number) for task workflows, by (org, project) for dev
// workflows), and the list endpoint uses it to enumerate a project's runs
// before refreshing live state from Temporal. Rows are written by workflow
// activities (replay-safe), so the index self-heals on workflow retry.
//
// The one-running-task-per-issue invariant is a partial unique index on
// (repo, issue_number) WHERE kind='task' AND status='running', created by the
// workflow_runs migration (AutoMigrate cannot express partial indexes).
type DevflowRun struct {
	ID string `gorm:"primaryKey;type:uuid;default:gen_random_uuid()" json:"id"`

	// WorkflowID + RunID identify the Temporal execution. The composite
	// unique index is the Record upsert's conflict target.
	WorkflowID string `gorm:"index;uniqueIndex:ux_workflow_runs_wf_run;not null" json:"workflowId"`
	RunID      string `gorm:"uniqueIndex:ux_workflow_runs_wf_run;not null" json:"runId"`

	Kind string `gorm:"not null;index" json:"kind"` // dev | task | validation

	OrgID     string `gorm:"index;not null" json:"-"`
	ProjectID string `gorm:"index;not null" json:"projectId"`
	// Tag is the spec version this run builds (the devflow lineage).
	Tag string `gorm:"type:text" json:"tag,omitempty"`

	// Repo + IssueNumber identify the Task for task-kind rows ("" / 0 for dev).
	Repo        string `gorm:"index" json:"repo,omitempty"`
	IssueNumber int    `gorm:"index" json:"issueNumber,omitempty"`

	// ParentWorkflowID links a task run to its dev run.
	ParentWorkflowID string `gorm:"index" json:"parentWorkflowId,omitempty"`

	Status string `gorm:"not null;index;default:running" json:"status"` // running | completed | failed | canceled

	// TasksTotal/Done/Failed are the dev run's own task tally (the overview
	// build stage), written by the dev workflow as absolute values — total
	// once after plan, done/failed per task transition — and frozen when the
	// run ends. Zero for task-kind rows and for runs predating the columns.
	TasksTotal  int `gorm:"not null;default:0" json:"tasksTotal"`
	TasksDone   int `gorm:"not null;default:0" json:"tasksDone"`
	TasksFailed int `gorm:"not null;default:0" json:"tasksFailed"`

	// Reason is the human-readable failure detail for a terminal `failed` run —
	// the devflow's DevFlowStatus.Error (e.g. "provisioning failed: org-service
	// provider not found …"). Empty for running/completed runs. Surfaced on the
	// build summary so the console shows WHY a build failed, not a bare "Failed".
	Reason string `gorm:"type:text" json:"reason,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TableName pins the table name.
func (DevflowRun) TableName() string { return "workflow_runs" }
