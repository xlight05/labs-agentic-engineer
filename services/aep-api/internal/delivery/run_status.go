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

// How a milestone run is ADDRESSED, and what it answers when asked. It lives at
// the root because starting, signalling and querying a run is a contract
// between the supervisor and everything that reaches it, not a private detail
// of the supervisor.

// MilestoneRunWorkflowName is the registered workflow type of the run
// supervisor.
const MilestoneRunWorkflowName = "MilestoneRunWorkflow"

// QueryRunStatus is the query name a live run answers with RunStatus.
const QueryRunStatus = "run-status"

// MilestoneRunWorkflowID is a run's Temporal workflow id.
//
// There is ONE run species and it is keyed by the milestone, not by a tag and
// not by an attempt: "work the open issues in milestone M". A milestone sees
// SEQUENTIAL runs across its life (the spec build that created it, then later
// incident adoptions into the same version), which is why the id is reused
// after a terminal run rather than made unique per run.
func MilestoneRunWorkflowID(orgID, projectID string, milestoneNumber int) string {
	return fmt.Sprintf("run-%s-%s-%d", orgID, projectID, milestoneNumber)
}

// RunStatus is a live run's self-report — the loop position no database column
// holds. The run row carries the durable facts (state, reason, budgets,
// verdict); this carries WHERE IN THE LOOP the run is right now, which is
// deliberately not persisted because fix and conflict cycles re-enter earlier
// phases and a stored phase enum would lie mid-loop.
type RunStatus struct {
	RunID           string `json:"runId"`
	MilestoneNumber int    `json:"milestoneNumber"`
	Origin          string `json:"origin"`

	// State is one of the RunState* values; TerminalReason is a RunReason* and
	// is empty until the run settles into a non-success terminal state.
	State          string `json:"state"`
	TerminalReason string `json:"terminalReason,omitempty"`

	// Phase names what the run is doing inside a cycle (RunPhase*). It is empty
	// while the run is waiting.
	Phase string `json:"phase,omitempty"`

	// CycleKind / CycleAttempt / CyclePR describe the cycle in flight.
	CycleKind    string `json:"cycleKind,omitempty"`
	CycleAttempt int    `json:"cycleAttempt,omitempty"`
	CyclePR      int    `json:"cyclePr,omitempty"`

	// Budget counters as the workflow itself counts them — the authority the
	// run row's columns mirror.
	CyclesTotal    int `json:"cyclesTotal"`
	CycleCeiling   int `json:"cycleCeiling"`
	FixCycles      int `json:"fixCycles"`
	ConflictCycles int `json:"conflictCycles"`

	// ValidationIssue is the validation issue this run minted (0 until
	// deployed-green, and on every incident run); ValidationVerdict is the run
	// property the deployment surface reads.
	ValidationIssue   int    `json:"validationIssue,omitempty"`
	ValidationVerdict string `json:"validationVerdict,omitempty"`
}

// Run phases — where a cycle is. They are a read-model label only: no platform
// decision branches on them, which is what lets the loop re-enter earlier
// phases without inventing a state machine.
const (
	// RunPhaseWaiting is the unbounded wait between cycles, and the state a
	// dispatch-holding gate parks the run in.
	RunPhaseWaiting = "waiting"
	// RunPhaseCoding is dispatched, waiting for the agent's pull request to
	// land.
	RunPhaseCoding = "coding"
	// RunPhaseBuilding is merged, waiting for the fan-out's builds (and, via
	// AutoDeploy, their deployments) to settle.
	RunPhaseBuilding = "building"
	// RunPhaseValidating is the validation cycle.
	RunPhaseValidating = "validating"
	// RunPhaseSettling is the terminal bookkeeping: the milestone close and the
	// run row's final write.
	RunPhaseSettling = "settling"
)
