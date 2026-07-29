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

// Merge verdicts — why a cycle's pull request did not merge. There is
// deliberately no "merged" verdict: a merge is recorded by the merge SHA, so a
// second spelling of it could disagree with the first.
const (
	// CycleMergeDeclined is the auto-merge POLICY's no: the pull request claims
	// nothing that is agent work in this run's milestone, so it is not this
	// run's work and is left for a human.
	CycleMergeDeclined = "declined"
	// CycleMergeRefused is the HOST's no on a pull request that is still open,
	// which in this model means exactly one thing — it does not merge cleanly.
	// A conflict issue is minted and the next cycle rebases.
	CycleMergeRefused = "refused"
)

// IssueNumbers is a jsonb-serialized list of GitHub issue numbers. Named so the
// column's shape is declared once rather than spelled out at each field.
type IssueNumbers []int

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
// Branch, the pull request (PRNumber and PRURL) and MergeSHA are LEARNED FROM
// WEBHOOKS, never from dispatch — the agent derives its own branch identity
// (crash resume reuses an unmerged `aep/m<milestone#>-*` branch), so the
// platform records what actually happened. They stay empty on a cycle whose
// agent died before opening a PR.
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

	// PRURL is the pull request's page on the HOST, exactly as the webhook
	// reported it. It is stored rather than composed from the repo row and
	// PRNumber: composing would spread GitHub's URL grammar and the repo's
	// clone-URL spelling (`.git` suffixes and all) into every reader that wants a
	// link, and be wrong everywhere at once on the day either changes.
	PRURL string `gorm:"type:text" json:"prUrl,omitempty"`

	// PRDraft mirrors the pull request's draft flag. A draft is the agent saying
	// it is not finished, so the merge policy never runs on one — and a cycle
	// parked behind a draft is otherwise indistinguishable from one whose agent
	// never opened a pull request at all.
	PRDraft bool `gorm:"not null;default:false" json:"prDraft,omitempty"`

	// Resolves is the merge policy's MATCHED set: the milestone agent-work
	// issues this cycle's pull request claims, which is what the merge closes.
	//
	// It is recorded because it is the only durable answer to "what did this
	// cycle work". The issues themselves are closed by the merge, and the
	// boundary read the supervisor dispatches on returns COUNTS, so nothing else
	// in the system can attribute a closed issue to the cycle that closed it.
	Resolves IssueNumbers `gorm:"type:jsonb;serializer:json" json:"resolves,omitempty"`

	// MergeVerdict is why the pull request did NOT merge, when something decided
	// so: CycleMergeDeclined (the policy: not this run's work) or
	// CycleMergeRefused (the host: it does not merge cleanly). Empty on a cycle
	// whose merge was never decided against — including every cycle that merged,
	// since a merge is recorded by MergeSHA and each fresh decision overwrites
	// this field.
	MergeVerdict string `gorm:"type:text" json:"mergeVerdict,omitempty"`
	// MergeReason is the verdict's own words, for a reader. Never parsed.
	MergeReason string `gorm:"type:text" json:"mergeReason,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// EndedAt stamps the cycle closed. A nil EndedAt is the "still open" guard
	// every mutator is fenced on.
	EndedAt *time.Time `json:"endedAt,omitempty"`
}

// TableName pins the table name so a struct rename cannot silently move the
// table.
func (RunCycle) TableName() string { return "run_cycles" }

// CyclePullRequest is the pull request identity a cycle learns from one
// pull_request delivery: what the agent opened, as the HOST describes it.
//
// The four facts travel together because they are one observation. They are also
// learned twice — the `opened` delivery and, when that one was missed, the
// `closed` backfill — and a partial write from either would leave the cycle
// describing a pull request that does not exist.
type CyclePullRequest struct {
	Branch string
	Number int
	// URL is the host's own page for the pull request (GitHub's `html_url`).
	URL string
	// Draft is the pull request's draft flag at the moment of the delivery. It is
	// part of the identity, not a separate fact: a pull request marked ready is
	// the SAME pull request.
	Draft bool
}
