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

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// RunCycleRepository is the write-authority over a run's cycle records — one
// row per dispatch. Lookups miss with (nil, nil), never gorm.ErrRecordNotFound,
// and every mutator is guarded on the cycle still being open (ended_at IS NULL)
// so a duplicate webhook is a no-op returning (nil, nil) rather than a rewrite
// of a closed cycle.
//
// Mutators are keyed by cycle id and are deliberately NOT org-scoped: they are
// platform-internal writes driven by dispatch and by webhooks, both of which
// reached the cycle through already-org-resolved facts. The reads that serve the
// HTTP surface take an orgID and fence on it.
type RunCycleRepository interface {
	// Append inserts a fresh cycle for a run, with Attempts at zero — the first
	// NoteDispatch takes it to one. Kind must be one of the CycleKind* values.
	Append(ctx context.Context, cycle *RunCycle) error

	// NoteDispatch records a dispatch of the cycle: it increments Attempts and
	// re-points the row at the newly dispatched Job. The supervisor compares the
	// returned Attempts against RunMaxRedispatchPerCycle to decide whether the
	// per-cycle re-dispatch budget is spent. Guarded on the cycle being open.
	NoteDispatch(ctx context.Context, id, jobRef string) (*RunCycle, error)

	// NotePullRequest records the pull request the agent actually opened, learned
	// from the pull_request webhook — the platform never dictates branch identity
	// or link, it observes them. Guarded on the cycle being open.
	NotePullRequest(ctx context.Context, id string, pr CyclePullRequest) (*RunCycle, error)

	// NoteMergeDecision records what the merge policy decided about the cycle's
	// pull request: the matched issue set, and the verdict (with its reason) when
	// the pull request did not merge.
	//
	// It is a SEPARATE mutator from NotePullRequest on purpose. Pull request
	// identity is backfilled from the merge webhook too, and a backfill has no
	// decision in hand — folding both into one update would let it clobber a
	// recorded verdict with zero values. Guarded on the cycle being open.
	NoteMergeDecision(ctx context.Context, id string, resolves []int, verdict, reason string) (*RunCycle, error)

	// Finish closes the cycle: it stamps ended_at and records the merge SHA the
	// cycle landed. mergeSHA is empty for a cycle that ended without a merge
	// (agent death, budget exhaustion, cancel). Guarded on the cycle being open,
	// so the first close wins.
	Finish(ctx context.Context, id, mergeSHA string) (*RunCycle, error)

	// Latest returns a run's newest cycle, or (nil, nil) when the run has not
	// dispatched yet. This is how loop POSITION is read — never from a stored
	// phase enum on the run row.
	Latest(ctx context.Context, orgID, runID string) (*RunCycle, error)

	// ListByRun returns a run's cycles oldest first — the cycle timeline.
	ListByRun(ctx context.Context, orgID, runID string) ([]RunCycle, error)

	// ListRecentDispatched returns every cycle that has launched a Job and is
	// either still open or closed no earlier than `since` — the watcher's claim
	// set for capturing agent logs.
	//
	// It is deliberately NOT "open cycles only": the agent Job exits the moment
	// it opens its pull request, and the auto-merge that CLOSES the cycle follows
	// within seconds, so a watcher restricted to open cycles would routinely
	// arrive after the cycle had closed and capture nothing. The window instead
	// tracks how long the Job's pod survives (its TTL), which is what actually
	// bounds the capture.
	//
	// Unscoped by org on purpose: it drives a platform watcher, not an HTTP read.
	ListRecentDispatched(ctx context.Context, since time.Time) ([]RunCycle, error)

	// DeleteByProject purges a project's cycle records — the project-delete
	// cascade, paired with MilestoneRunRepository.DeleteByProject so a recreated
	// same-named project starts with a clean timeline.
	DeleteByProject(ctx context.Context, orgID, projectID string) error
}

type runCycleRepository struct{ db *gorm.DB }

// NewRunCycleRepository wires the gorm-backed repository.
func NewRunCycleRepository(db *gorm.DB) RunCycleRepository {
	return &runCycleRepository{db: db}
}

func (r *runCycleRepository) Append(ctx context.Context, cycle *RunCycle) error {
	switch cycle.Kind {
	case CycleKindCoding, CycleKindConflict, CycleKindFix, CycleKindValidation:
	default:
		return fmt.Errorf("run cycle: unknown kind %q", cycle.Kind)
	}
	if cycle.RunID == "" {
		return errors.New("run cycle: RunID is required")
	}
	return r.db.WithContext(ctx).Create(cycle).Error
}

func (r *runCycleRepository) NoteDispatch(ctx context.Context, id, jobRef string) (*RunCycle, error) {
	return r.updateOpen(ctx, id, map[string]any{
		"attempts": gorm.Expr("attempts + 1"),
		"job_ref":  jobRef,
	})
}

func (r *runCycleRepository) NotePullRequest(ctx context.Context, id string, pr CyclePullRequest) (*RunCycle, error) {
	return r.updateOpen(ctx, id, map[string]any{
		"branch":    pr.Branch,
		"pr_number": pr.Number,
		"pr_url":    pr.URL,
		"pr_draft":  pr.Draft,
	})
}

func (r *runCycleRepository) NoteMergeDecision(ctx context.Context, id string, resolves []int, verdict, reason string) (*RunCycle, error) {
	// A STRUCT update, not the map the other mutators use: resolves is a
	// serializer-backed jsonb column, and only the struct path runs the schema's
	// serializer. Select names the three columns so blanks are written too — the
	// row is a snapshot of the LATEST decision, so a pull request that was
	// declined and then re-pushed into a merge must not keep its stale verdict.
	return r.updateOpenColumns(ctx, id,
		[]string{"resolves", "merge_verdict", "merge_reason"},
		RunCycle{
			Resolves:     IssueNumbers(resolves),
			MergeVerdict: verdict,
			MergeReason:  reason,
		})
}

func (r *runCycleRepository) Finish(ctx context.Context, id, mergeSHA string) (*RunCycle, error) {
	return r.updateOpen(ctx, id, map[string]any{
		"merge_sha": mergeSHA,
		"ended_at":  time.Now().UTC(),
	})
}

func (r *runCycleRepository) Latest(ctx context.Context, orgID, runID string) (*RunCycle, error) {
	var row RunCycle
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND run_id = ?", orgID, runID).
		Order("created_at DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *runCycleRepository) ListByRun(ctx context.Context, orgID, runID string) ([]RunCycle, error) {
	var rows []RunCycle
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND run_id = ?", orgID, runID).
		Order("created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *runCycleRepository) ListRecentDispatched(ctx context.Context, since time.Time) ([]RunCycle, error) {
	var rows []RunCycle
	err := r.db.WithContext(ctx).
		Where("job_ref <> '' AND (ended_at IS NULL OR ended_at >= ?)", since.UTC()).
		Order("created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *runCycleRepository) DeleteByProject(ctx context.Context, orgID, projectID string) error {
	return r.db.WithContext(ctx).
		Where("org_id = ? AND project_id = ?", orgID, projectID).
		Delete(&RunCycle{}).Error
}

// updateOpen applies a guarded update to a cycle that has not been closed and
// re-reads it. It is the ONE place the "a closed cycle is never rewritten"
// fence lives, so every mutator inherits it — and the (nil, nil) no-op contract
// on RowsAffected == 0.
func (r *runCycleRepository) updateOpen(ctx context.Context, id string, updates map[string]any) (*RunCycle, error) {
	return r.applyOpen(ctx, id, nil, updates)
}

// updateOpenColumns is updateOpen for a STRUCT update: the named columns are
// written even when their value is a zero value, and serializer-backed columns
// go through the schema rather than being handed to the driver raw.
func (r *runCycleRepository) updateOpenColumns(ctx context.Context, id string, columns []string, values RunCycle) (*RunCycle, error) {
	return r.applyOpen(ctx, id, columns, values)
}

func (r *runCycleRepository) applyOpen(ctx context.Context, id string, columns []string, values any) (*RunCycle, error) {
	tx := r.db.WithContext(ctx).
		Model(&RunCycle{}).
		Where("id = ? AND ended_at IS NULL", id)
	if len(columns) > 0 {
		tx = tx.Select(columns)
	}
	res := tx.Updates(values)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return r.getByID(ctx, id)
}

func (r *runCycleRepository) getByID(ctx context.Context, id string) (*RunCycle, error) {
	var row RunCycle
	err := r.db.WithContext(ctx).First(&row, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}
