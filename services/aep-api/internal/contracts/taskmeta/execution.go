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

package taskmeta

import (
	"strconv"
	"strings"
	"time"
)

// ExecutionKind is the kind of work one Execution attempts for a Task (§7). No
// Execution spans a human gate: a merged PR ends nothing — it spawns a build.
type ExecutionKind string

const (
	// KindCoding: dispatch → coding agent run → pull request.
	KindCoding ExecutionKind = "coding"
	// KindBuild: PR merged → build → deploy.
	KindBuild ExecutionKind = "build"
	// KindOps: a platform operation (create a DB, provision an IDP). No
	// PR/build; executor TBD (§11).
	KindOps ExecutionKind = "ops"
	// KindProvision: a dependency-provisioning run (external value collection or
	// platform-resource provisioning). No PR/build; admitted+started by the
	// ProvisioningService from the drawer action and Finished by the
	// resource-readiness watcher (dependency-management §3.6). A succeeded
	// provision run derives StatusDeployed, satisfying dependent coding tasks.
	KindProvision ExecutionKind = "provision"
)

// Valid reports whether k is a known execution kind.
func (k ExecutionKind) Valid() bool {
	switch k {
	case KindCoding, KindBuild, KindOps, KindProvision:
		return true
	}
	return false
}

// ExecutionStatus is the lifecycle of a single Execution row (§7).
type ExecutionStatus string

const (
	ExecQueued    ExecutionStatus = "queued"    // admitted, gated, not yet running
	ExecRunning   ExecutionStatus = "running"   // dispatched and in flight
	ExecSucceeded ExecutionStatus = "succeeded" // terminal
	ExecFailed    ExecutionStatus = "failed"    // terminal
	ExecCanceled  ExecutionStatus = "canceled"  // terminal
)

// Valid reports whether s is a known execution status.
func (s ExecutionStatus) Valid() bool {
	switch s {
	case ExecQueued, ExecRunning, ExecSucceeded, ExecFailed, ExecCanceled:
		return true
	}
	return false
}

// IsTerminal reports whether the status is final (succeeded/failed/canceled) —
// no further transitions occur and the admission mutex (§5) no longer holds.
func (s ExecutionStatus) IsTerminal() bool {
	switch s {
	case ExecSucceeded, ExecFailed, ExecCanceled:
		return true
	}
	return false
}

// IsActive reports whether the status still holds the admission mutex
// (queued/running) — i.e. it blocks a second Execution of the same kind (§5).
func (s ExecutionStatus) IsActive() bool {
	return s == ExecQueued || s == ExecRunning
}

// ExecutionFact is the minimal Execution projection the derived-status algebra
// consumes (derive.go): kind, status, reason, and creation time for recency.
// The full persisted row lives in delivery.Execution; derive stays pure by
// taking only these facts (delivery.ExecutionFacts is the row projector).
type ExecutionFact struct {
	Kind      ExecutionKind
	Status    ExecutionStatus
	Reason    string
	CreatedAt time.Time
}

// ReasonPRClosedUnmerged is the reason sentinel feature/execution stamps on a
// synthetic terminal coding row appended when a linked PR is closed without
// merging (§4 rejected). No existing execution row is mutated — a new terminal
// row is appended, so history stays monotonic and the derived status flips to
// rejected. It is the shared convention that lets both halves of the
// Task/Execution split reconstruct GitHub PR state from the executions rows
// without a live PR query (§8), so the sentinel and the reconstruction
// (PRStateFromFacts) live here in the shared encoding.
const ReasonPRClosedUnmerged = "pr_closed_unmerged"

// ReasonPROpenPrefix + the PR number is stamped on a succeeded coding row when
// its linked PR opens ("pr#123"), so the read path can recover the PR number
// from the executions rows without a live PR query (§8). Shared here so both
// feature/execution (which stamps it) and the project status builder (which
// links to the validation PR) use one encoding.
const ReasonPROpenPrefix = "pr#"

// OpenPRNumber parses the PR number from a coding row's PR-open reason
// ("pr#123" → 123), or 0 when the reason carries no pr# marker. Callers confirm
// the row is a succeeded coding Execution before trusting the number.
func OpenPRNumber(reason string) int {
	rest, ok := strings.CutPrefix(reason, ReasonPROpenPrefix)
	if !ok {
		return 0
	}
	n, _ := strconv.Atoi(rest)
	return n
}

// PRStateFromFacts reconstructs the latest linked PR's state from the
// latest-per-kind execution facts (documented §13 default — no live PR query,
// §8). The mapping follows the Execution lifecycle (§7): a coding Execution
// ends (succeeds) only at PR-open, a merged PR spawns a build Execution, and a
// PR closed unmerged appends a terminal coding row tagged
// ReasonPRClosedUnmerged.
//
//   - a build Execution exists              → PR was merged (PRMerged);
//   - latest coding failed, closed-unmerged → PRClosedUnmerged;
//   - latest coding succeeded               → PR is open (PROpen);
//   - otherwise                             → no linked PR yet (PRNone).
func PRStateFromFacts(execs []ExecutionFact) PRState {
	if latestOfKind(execs, KindBuild) != nil {
		return PRMerged
	}
	coding := latestOfKind(execs, KindCoding)
	if coding == nil {
		return PRNone
	}
	switch coding.Status {
	case ExecFailed:
		if coding.Reason == ReasonPRClosedUnmerged {
			return PRClosedUnmerged
		}
		return PRNone
	case ExecSucceeded:
		return PROpen
	}
	return PRNone
}
