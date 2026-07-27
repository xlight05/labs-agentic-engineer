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

// Package taskmeta is the pure, IO-free encoding of an EXECUTION — one
// platform attempt at one kind of work, owned entirely by Postgres.
//
// It is what remains of the encoding the Task/Execution split once shared. The
// milestone model retired the GitHub-facing half of that encoding: issue bodies
// are prose, issue structure is labels plus milestone membership, nothing
// platform-side parses an issue any more, and agent work is accounted for by
// the run's cycle records rather than execution rows. What survives is the
// vocabulary the PROVISIONING gates and the execution reads still speak: the
// kinds and the lifecycle.
//
// It stays pure: no IO, no domain imports. `internal/arch`'s
// TestTaskmetaIsPure is the executable form of that rule.
package taskmeta

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

// ExecutionStatus is the lifecycle of a single Execution row (§7).
type ExecutionStatus string

const (
	ExecQueued    ExecutionStatus = "queued"    // admitted, gated, not yet running
	ExecRunning   ExecutionStatus = "running"   // dispatched and in flight
	ExecSucceeded ExecutionStatus = "succeeded" // terminal
	ExecFailed    ExecutionStatus = "failed"    // terminal
	ExecCanceled  ExecutionStatus = "canceled"  // terminal
)

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
//
// The AUTHORITY on this in production is the partial unique index the
// executions migration creates (`WHERE status IN ('queued','running')`), so no
// production Go path evaluates it; this is the same rule stated once in Go, for
// the in-memory stores that stand in for that index in tests. Inlining it at
// each of them would duplicate the mutex's definition three ways.
//
//deadcode:keep the Go mirror of the admission mutex's partial unique index — the index is production's authority, so only the fakes that reproduce it call this.
func (s ExecutionStatus) IsActive() bool {
	return s == ExecQueued || s == ExecRunning
}
