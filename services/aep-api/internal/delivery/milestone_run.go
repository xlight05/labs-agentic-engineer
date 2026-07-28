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

// MilestoneRun origins, states, terminal reasons and validation verdicts
// (plain strings, matching the model convention — canonical values here, no
// separate enum package).
const (
	// RunOriginSpecBuild is a run started by the build click: the plan path cut
	// a v<N> tag, minted the milestone, and started the supervisor. At most one
	// non-terminal spec-build run exists per project (the mutex below).
	RunOriginSpecBuild = "spec-build"
	// RunOriginIncidentAdoption is a run started by adopting an issue into an
	// already-deployed version's milestone. Incident runs on other milestones
	// execute concurrently with each other and with a live spec run.
	RunOriginIncidentAdoption = "incident-adoption"

	// RunStatePlanning is the FILL WINDOW: the row has been admitted (so the
	// spec mutex is armed) but the milestone it works is still being written —
	// gates minted, then the planning turn streaming issues into GitHub. It is
	// bounded work the platform is actively doing, which is exactly what
	// separates it from RunStateWaiting: nothing is held, nobody is needed.
	//
	// Only the plan path writes it, and only at admission. The supervisor's
	// first pass leaves it — for running if it dispatches, for waiting if a gate
	// holds it — so no transition ever moves INTO planning.
	RunStatePlanning = "planning"
	// RunStateWaiting is the unbounded wait between cycles; cancel is its only
	// expiry. RunStateRunning covers a dispatched cycle. The loop oscillates
	// waiting ⇄ running until it settles.
	RunStateWaiting   = "waiting"
	RunStateRunning   = "running"
	RunStateSucceeded = "succeeded"
	RunStateFailed    = "failed"
	RunStateCancelled = "cancelled"

	// Terminal reasons. Each names exactly ONE failure class so the reason a run
	// stopped is never ambiguous. Empty while the run is non-terminal, and empty
	// on a succeeded run.
	RunReasonRedispatchBudget     = "redispatch-budget"
	RunReasonBuildRetriggerBudget = "build-retrigger-budget"
	RunReasonFixChainBudget       = "fix-chain-budget"
	RunReasonConflictBudget       = "conflict-budget"
	RunReasonNoProgress           = "no-progress"
	RunReasonCycleCeiling         = "cycle-ceiling"
	RunReasonValidationFailed     = "validation-failed"
	// RunReasonPlanFailed is the plan path's own failure class: the run row is
	// admitted BEFORE the planning turn (so the spec-run mutex is armed for the
	// whole of it), which means a planning turn that cannot finish must settle
	// the row it armed. Without it a failed plan would wedge the project behind
	// its own mutex until a human cancelled.
	RunReasonPlanFailed = "plan-failed"

	// Validation verdicts. Empty until the validation cycle settles; skipped
	// when the project has no acceptance criteria and on incident runs (which
	// get no validation cycle at all).
	ValidationVerdictPassed  = "passed"
	ValidationVerdictFailed  = "failed"
	ValidationVerdictSkipped = "skipped"
)

// Budget limits. Each budget names exactly one failure class, which is what
// keeps the terminal reasons honest.
const (
	// RunMaxRedispatchPerCycle bounds agent death (including
	// Job-succeeded-no-PR) within ONE cycle. It is counted on the cycle record's
	// Attempts, not on the run row, because it resets at every cycle boundary.
	RunMaxRedispatchPerCycle = 2
	// RunMaxBuildRetriggersPerComponentSHA is the automatic build re-trigger
	// allowance: one per component per merge SHA. The authoritative guard is
	// keyed by (component, SHA) at the trigger site; MilestoneRun.BuildRetriggers
	// is only the run-wide tally behind RunReasonBuildRetriggerBudget.
	RunMaxBuildRetriggersPerComponentSHA = 1
	// RunMaxFixCycles / RunMaxConflictCycles bound the two recovery chains.
	RunMaxFixCycles      = 2
	RunMaxConflictCycles = 2
	// RunDefaultCycleCeiling is the total-cycle ceiling a run starts with when
	// the caller does not pin one. The legitimate worst case uses 4–5 cycles.
	RunDefaultCycleCeiling = 8
)

// MilestoneRun is one run of the milestone loop: "work the open issues in
// milestone M until it settles". There is exactly ONE run species — a spec
// build and an incident adoption differ only in Origin (and in whether the run
// gets a validation cycle) — so this single row carries every run's small
// state, its budgets and its validation verdict.
//
// Identity is (OrgID, ProjectID, MilestoneNumber). **The milestone NUMBER is
// the platform key, never the title**: GitHub milestone titles are freely
// renamable and its title filters are case-insensitive while create-uniqueness
// is case-sensitive. MilestoneTitle is the title AT CREATION (== the `v<N>` tag
// the run builds) and is kept for display and for resolving a `?tag=` query to
// a milestone number through these rows — which is why the read model never
// title-matches against GitHub.
//
// Loop POSITION is deliberately absent: it renders from the latest RunCycle
// joined live, because fix and conflict cycles re-enter earlier phases and a
// flat phase enum would lie mid-loop. Per-component build/deploy status is
// likewise absent — it is derived from OpenChoreo on read, never stored.
//
// The spec-run mutex (§5's 409, in DB form) is a partial unique index on
// (org_id, project_id) WHERE origin = 'spec-build' AND state IN
// ('planning','waiting','running'), created by the milestone_runs migration —
// AutoMigrate cannot express a partial index. Incident-adoption runs are
// deliberately outside the index, so they run concurrently on their own
// milestones.
type MilestoneRun struct {
	ID        string `gorm:"primaryKey;type:uuid;default:gen_random_uuid()" json:"id"`
	OrgID     string `gorm:"index;not null" json:"-"`
	ProjectID string `gorm:"index;not null" json:"projectId"`

	// MilestoneNumber is the GitHub milestone this run works — the platform key.
	MilestoneNumber int `gorm:"index;not null" json:"milestoneNumber"`
	// MilestoneTitle is the milestone's title at creation, equal to the spec tag
	// (`v<N>`). Display + `?tag=` resolution only; never a lookup key against
	// GitHub.
	MilestoneTitle string `gorm:"index;not null" json:"milestoneTitle"`

	Origin string `gorm:"not null;index" json:"origin"`                // spec-build | incident-adoption
	State  string `gorm:"not null;index;default:waiting" json:"state"` // planning | waiting | running | succeeded | failed | cancelled
	// TerminalReason is set exactly once, when the run settles into a non-success
	// terminal state. Empty while non-terminal and on a succeeded run.
	TerminalReason string `gorm:"type:text" json:"terminalReason,omitempty"`

	// Budget counters. CyclesTotal is checked against CycleCeiling; FixCycles and
	// ConflictCycles bound the two recovery chains; BuildRetriggers is the
	// run-wide tally of automatic build re-triggers (the authoritative
	// one-per-component-per-SHA guard lives at the trigger site, which is keyed
	// by facts this row does not hold). The per-cycle re-dispatch budget is NOT
	// here — it is RunCycle.Attempts, because it resets each cycle.
	CyclesTotal     int `gorm:"not null;default:0" json:"cyclesTotal"`
	FixCycles       int `gorm:"not null;default:0" json:"fixCycles"`
	ConflictCycles  int `gorm:"not null;default:0" json:"conflictCycles"`
	BuildRetriggers int `gorm:"not null;default:0" json:"buildRetriggers"`
	// CycleCeiling is the run's total-cycle ceiling, snapshotted at start so a
	// config change cannot retroactively fail (or rescue) a live run.
	CycleCeiling int `gorm:"not null" json:"cycleCeiling"`

	// ValidationVerdict is a run property (the validation cycle's outcome), not
	// a per-issue one. Empty until the validation cycle settles.
	ValidationVerdict string `gorm:"type:text" json:"validationVerdict,omitempty"`

	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

// TableName pins the table name so a struct rename cannot silently move the
// table.
func (MilestoneRun) TableName() string { return "milestone_runs" }

// IsTerminalRunState reports whether a run state is settled. Terminal rows are
// never resurrected: every guarded transition in MilestoneRunRepository is
// fenced on the state NOT being terminal, and the spec-run mutex only counts
// non-terminal rows.
func IsTerminalRunState(state string) bool {
	switch state {
	case RunStateSucceeded, RunStateFailed, RunStateCancelled:
		return true
	default:
		return false
	}
}

// nonTerminalRunStates is the WHERE-clause form of !IsTerminalRunState, shared
// by the guarded transitions and the mutex lookup so the two can never disagree.
// It must stay in step with the migration's partial index predicate — a state
// missing from one and present in the other would let a second spec run in.
var nonTerminalRunStates = []string{RunStatePlanning, RunStateWaiting, RunStateRunning}
