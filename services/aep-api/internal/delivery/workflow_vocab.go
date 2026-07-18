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

import "fmt"

// The dev-workflow I/O vocabulary (§10.3.1): the input/status DTOs, the gate
// config, the phase/outcome protocol constants, the workflow-registration name
// and the deterministic id builder. It lives in the delivery ROOT because it
// crosses a feature boundary — the `devflowwork` sub-package (producer) runs
// the Temporal workflow that consumes DevFlowInput and answers DevFlowStatus,
// while the `buildpipe` sub-package (starter/reader) cuts the tag, starts the
// workflow by DevFlowWorkflowName and reads its status. A build must not import
// devflowwork (slice ⊥ sibling), so the contract both sides speak is homed in
// the kernel and each imports only the root. It carries no Temporal dependency
// (the workflow implementation stays in the sub-package) — only the wire types,
// the constants, and the pure id function.

// GateConfig selects auto vs human-in-the-loop per gate for one run.
// A gate absent from Auto runs in auto mode (the default): awaitGate returns
// immediately. Auto[name] = false makes the gate manual: the workflow pauses,
// surfaces the gate in its status query, and waits for a SigGateDecision.
type GateConfig struct {
	Auto map[string]bool `json:"auto,omitempty"`
	// ApprovalTimeoutSeconds bounds a manual gate's wait; 0 means wait
	// indefinitely (the workflow run timeout still applies). A timeout is
	// treated as a rejection.
	ApprovalTimeoutSeconds int `json:"approvalTimeoutSeconds,omitempty"`
}

// IsAuto reports whether the named gate runs without a human pause.
func (c GateConfig) IsAuto(gate string) bool {
	if c.Auto == nil {
		return true
	}
	auto, ok := c.Auto[gate]
	return !ok || auto
}

// DevFlowInput starts a per-version development workflow: re-validate the
// spec at the tag, plan tasks, fan out task workflows, validate. Tag is the
// spec version this run builds — always cut by the build endpoint (after the
// whole-spec hard gate) BEFORE the workflow starts.
type DevFlowInput struct {
	OrgID     string `json:"orgId"`
	ProjectID string `json:"projectId"`
	// Repo is the project's "owner/name" — resolved by the API before start so
	// task children can dispatch + be signaled by the webhook handlers.
	Repo  string     `json:"repo"`
	Tag   string     `json:"tag"`
	Gates GateConfig `json:"gates"`
	// Provision carries the user's build-drawer inputs (issue #164): the
	// non-secret config, staged secret references, platform-resource params, and
	// approvals the workflow's provisioning step (Task 3) authors OC bindings +
	// gate issues from. Empty when the build needs no provisioning. Secret VALUES
	// are never carried here — only SM-API references (SecretRefByEnv).
	Provision []ProvisionInput `json:"provision,omitempty"`
}

// ProvisionInput is one dependency's resolved provisioning payload, produced by
// the build endpoint from the drawer inputs and carried into the dev workflow.
// It is the shared wire contract between POST /build (which stages secrets to
// SM-API and derives references) and the workflow's provisioning step (which
// authors the OC Resource model + aep:provision gates). A raw secret value is
// NEVER placed here — SecretRefByEnv holds the SM-API reference per env instead.
type ProvisionInput struct {
	Component  string `json:"component"`
	Dependency string `json:"dependency"`
	Kind       string `json:"kind"`
	// external non-secret config by key.
	Config map[string]string `json:"config,omitempty"`
	// external: the SM-API secret reference per env (NOT the secret value).
	SecretRefByEnv map[string]string `json:"secretRefByEnv,omitempty"`
	// platform-resource: provisioning params (mixed scalar types).
	Parameters map[string]any `json:"parameters,omitempty"`
	// platform-resource / org-service: the user's approval.
	Approved bool `json:"approved,omitempty"`
}

// DevFlowStatus is the QueryStatus result for a dev workflow.
type DevFlowStatus struct {
	Phase       string       `json:"phase"`
	Tag         string       `json:"tag,omitempty"`
	PendingGate string       `json:"pendingGate,omitempty"`
	Tasks       []DevTaskRef `json:"tasks,omitempty"`
	// Validation is the validating phase's outcome (the ValidationFlowWorkflow
	// child). Nil until the validating phase runs.
	Validation *ValidationRef `json:"validation,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// DevTaskRef is a child task's summary in the dev workflow status.
type DevTaskRef struct {
	Issue      int    `json:"issue"`
	WorkflowID string `json:"workflowId,omitempty"`
	Phase      string `json:"phase,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
}

// ValidationRef is the validating phase's summary in the dev workflow status:
// the orchestrator child plus its per-lane results. Outcome carries
// "skipped: no acceptance criteria" when there was nothing to validate.
type ValidationRef struct {
	WorkflowID string       `json:"workflowId,omitempty"`
	Phase      string       `json:"phase,omitempty"`
	Outcome    string       `json:"outcome,omitempty"`
	Lanes      []DevTaskRef `json:"lanes,omitempty"`
}

// DevFlow phase values.
const (
	DevPhaseValidatingSpec = "validating-spec"
	DevPhasePlanning       = "planning"
	DevPhaseProvisioning   = "provisioning"
	DevPhaseExecuting      = "executing"
	DevPhaseValidating     = "validating"
	DevPhaseDone           = "done"
	DevPhaseFailed         = "failed"
)

// TaskFlow phase values.
const (
	TaskPhaseStarting  = "starting"
	TaskPhaseCoding    = "coding"
	TaskPhaseMerging   = "merging"
	TaskPhaseBuilding  = "building"
	TaskPhaseDeploying = "deploying"
	TaskPhaseDone      = "done"
	TaskPhaseFailed    = "failed"
)

// Outcome values for TaskFlowResult.
const (
	OutcomeSucceeded     = "succeeded"
	OutcomeFailed        = "failed"
	OutcomeSkippedDepFai = "skipped-dep-failed"
)

// QueryStatus is the query name every devflow workflow exposes so the
// API/console can read live workflow state.
const QueryStatus = "status"

// DevFlowWorkflowName is the registered type name of the dev workflow — the
// handle the build endpoint starts a run by (the devflowwork sub-package
// registers the workflow under it; the other workflow-type names stay private
// to that sub-package since nothing outside starts them by name).
const DevFlowWorkflowName = "DevFlowWorkflow"

// DevWorkflowID builds the deterministic dev workflow id
// (devflow-<org>-<project>-<tag>) — shared by the build endpoint (start +
// status lookup) and the workflow_runs index.
func DevWorkflowID(orgID, projectID, tag string) string {
	return fmt.Sprintf("devflow-%s-%s-%s", orgID, projectID, tag)
}
