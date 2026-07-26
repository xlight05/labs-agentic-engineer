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

// RunCycle kinds (plain strings, matching the model convention). The kind names
// what the cycle was dispatched to do; recovery cycles are ordinary cycles, so
// a fix or conflict cycle is indistinguishable from normal work apart from this
// label and the budget it spends.
const (
	CycleKindCoding     = "coding"
	CycleKindConflict   = "conflict"
	CycleKindFix        = "fix"
	CycleKindValidation = "validation"
)

// RunCycle is one dispatch within a MilestoneRun: the platform hands the
// coding agent a milestone reference, the agent opens one PR, and the PR
// squash-merges. A run is a sequence of these — one row per dispatch — and the
// run's loop position is always read from the LATEST row rather than from a
// stored phase enum, because fix and conflict cycles re-enter earlier phases.
//
// Attempts is the per-cycle re-dispatch counter (budget
// RunMaxRedispatchPerCycle): it lives here, not on the run row, precisely
// because the budget resets at every cycle boundary. It counts dispatches, so a
// freshly appended row starts at 0 and the first dispatch takes it to 1.
//
// Branch, PRNumber and MergeSHA are LEARNED FROM WEBHOOKS, never from dispatch
// — the agent derives its own branch identity (crash resume reuses an unmerged
// `aep/m<milestone#>-*` branch), so the platform records what actually happened.
// They stay empty on a cycle whose agent died before opening a PR.
//
// OrgID and ProjectID are denormalized from the owning run so a tenant-fenced
// read needs no join; RunID is the real parent key.
type RunCycle struct {
	ID        string `gorm:"primaryKey;type:uuid;default:gen_random_uuid()" json:"id"`
	OrgID     string `gorm:"index;not null" json:"-"`
	ProjectID string `gorm:"index;not null" json:"projectId"`

	// RunID is the owning MilestoneRun.
	RunID string `gorm:"index;not null" json:"runId"`

	Kind string `gorm:"not null;index" json:"kind"` // coding | conflict | fix | validation

	// Attempts counts dispatches of THIS cycle (re-dispatch budget).
	Attempts int `gorm:"not null;default:0" json:"attempts"`

	// JobRef is the dispatched Kubernetes Job for the current attempt (empty
	// until the first dispatch, replaced on re-dispatch).
	JobRef string `gorm:"type:text" json:"jobRef,omitempty"`

	Branch   string `gorm:"type:text" json:"branch,omitempty"`
	PRNumber int    `gorm:"index" json:"prNumber,omitempty"`
	MergeSHA string `gorm:"type:text" json:"mergeSha,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// EndedAt stamps the cycle closed. A nil EndedAt is the "still open" guard
	// every mutator is fenced on.
	EndedAt *time.Time `json:"endedAt,omitempty"`
}

// TableName pins the table name so a struct rename cannot silently move the
// table.
func (RunCycle) TableName() string { return "run_cycles" }
