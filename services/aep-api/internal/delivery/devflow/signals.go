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

// Signal names. The existing webhook handlers and watchers translate their
// events into these signals (via Signaler); the workflows block on them
// instead of polling. Payload structs are versioned by care: only add fields.
const (
	// SigPROpened — pull_request.opened webhook → TaskFlowWorkflow (coding done).
	SigPROpened = "pr-opened"
	// SigPRMerged — pull_request.closed(merged) webhook → TaskFlowWorkflow.
	SigPRMerged = "pr-merged"
	// SigPRRejected — pull_request.closed(unmerged) webhook → TaskFlowWorkflow.
	SigPRRejected = "pr-rejected"
	// SigJobStatus — coding job/run terminal from JobWatcher/ExecWatcher →
	// TaskFlowWorkflow. Only failures are signaled (success rides SigPROpened).
	SigJobStatus = "job-status"
	// SigBuildStatus — build run terminal from ExecWatcher → TaskFlowWorkflow.
	SigBuildStatus = "build-status"
	// SigDeployStatus — deploy observation (build-success deploy fan-out) →
	// TaskFlowWorkflow.
	SigDeployStatus = "deploy-status"
	// SigGateDecision — the approve/reject API endpoint → either workflow.
	SigGateDecision = "gate-decision"
	// SigLaneStatus — ValidationFlowWorkflow → its ValidationTaskWorkflow lane
	// children. The orchestrator owns the validation issue's webhook signals
	// and forwards each lane's terminal state (correlated by ExecutionID).
	SigLaneStatus = "lane-status"
)

// Phase values shared by the status signals.
const (
	PhaseSucceeded = "succeeded"
	PhaseFailed    = "failed"
)

// PRSignal reports a pull-request event for the Task the PR closes.
type PRSignal struct {
	Repo     string `json:"repo"` // "owner/name"
	Issue    int    `json:"issue"`
	PRNumber int    `json:"prNumber,omitempty"`
	// HeadSHA is set on pr-opened; MergeSHA on pr-merged.
	HeadSHA  string `json:"headSha,omitempty"`
	MergeSHA string `json:"mergeSha,omitempty"`
}

// RunStatusSignal reports a coding-job / build / deploy terminal.
type RunStatusSignal struct {
	ExecutionID string `json:"executionId,omitempty"`
	Phase       string `json:"phase"` // succeeded | failed
	Message     string `json:"message,omitempty"`
	ImageRef    string `json:"imageRef,omitempty"` // build
	Endpoint    string `json:"endpoint,omitempty"` // deploy
}

// LaneStatusSignal is the orchestrator's forwarded terminal state for one
// validation lane (see SigLaneStatus).
type LaneStatusSignal struct {
	Lane    string `json:"lane"`
	Phase   string `json:"phase"` // succeeded | failed
	Message string `json:"message,omitempty"`
}

// GateDecisionSignal carries a human approve/reject for a named gate.
type GateDecisionSignal struct {
	Gate    string `json:"gate"`
	Approve bool   `json:"approve"`
	Actor   string `json:"actor,omitempty"`
	Note    string `json:"note,omitempty"`
}
