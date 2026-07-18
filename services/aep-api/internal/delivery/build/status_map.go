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

package build

import (
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/models"
)

// Build status enum values (the contract's BuildStatus.status /
// BuildStatusTask.status vocabulary).
const (
	statusStarted    = "started"
	statusInProgress = "in_progress"
	statusCompleted  = "completed"
	statusFailed     = "failed"
)

// statusFromPhase maps a live DevFlowStatus phase onto the contract enum.
func statusFromPhase(phase string) string {
	switch phase {
	case delivery.DevPhaseValidatingSpec:
		return statusStarted
	case delivery.DevPhaseDone:
		return statusCompleted
	case delivery.DevPhaseFailed:
		return statusFailed
	default: // planning / executing / validating
		return statusInProgress
	}
}

// statusFromRow maps a workflow_runs row status onto the contract enum — the
// fallback when the live query is unavailable (worker gone, run archived).
func statusFromRow(rowStatus string) string {
	switch rowStatus {
	case models.WorkflowStatusCompleted:
		return statusCompleted
	case models.WorkflowStatusFailed, models.WorkflowStatusCanceled:
		return statusFailed
	default: // running
		return statusInProgress
	}
}

// statusFromDerived maps a Task's live derived status (taskmeta.Derive: GitHub
// facts ⋈ executions) onto the contract's build enum. This is the DURABLE task
// status — the source for a build's task list when the Temporal run is archived
// (no live refs) or the query is unavailable. The workflow's DevTaskRef refines
// it for tasks still in flight (taskStatus above).
func statusFromDerived(derived string) string {
	switch taskmeta.DerivedStatus(derived) {
	case taskmeta.StatusDeployed:
		return statusCompleted
	case taskmeta.StatusFailed, taskmeta.StatusRejected, taskmeta.StatusAbandoned:
		return statusFailed
	case taskmeta.StatusPending, taskmeta.StatusOnHold:
		return statusStarted
	default: // in_progress / ready_for_review / merged / building
		return statusInProgress
	}
}

// taskStatus maps a DevTaskRef (phase + outcome) onto the contract enum.
func taskStatus(ref delivery.DevTaskRef) string {
	if ref.Outcome == delivery.OutcomeFailed || ref.Outcome == delivery.OutcomeSkippedDepFai {
		return statusFailed
	}
	if ref.Outcome == delivery.OutcomeSucceeded {
		return statusCompleted
	}
	switch ref.Phase {
	case "pending", delivery.TaskPhaseStarting:
		return statusStarted
	case delivery.TaskPhaseDone:
		return statusCompleted
	case delivery.TaskPhaseFailed:
		return statusFailed
	default: // coding / merging / building / deploying
		return statusInProgress
	}
}
