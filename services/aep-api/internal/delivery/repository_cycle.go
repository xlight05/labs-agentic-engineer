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

	// NotePullRequest records the branch and PR the agent actually opened,
	// learned from the pull_request webhook — the platform never dictates branch
	// identity, it observes it. Guarded on the cycle being open.
	NotePullRequest(ctx context.Context, id, branch string, prNumber int) (*RunCycle, error)

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

func (r *runCycleRepository) NotePullRequest(ctx context.Context, id, branch string, prNumber int) (*RunCycle, error) {
	return r.updateOpen(ctx, id, map[string]any{
		"branch":    branch,
		"pr_number": prNumber,
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
	res := r.db.WithContext(ctx).
		Model(&RunCycle{}).
		Where("id = ? AND ended_at IS NULL", id).
		Updates(updates)
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
