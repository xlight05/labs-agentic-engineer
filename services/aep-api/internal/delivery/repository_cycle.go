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

	"github.com/wso2/aep/aep-api/internal/contracts"
	"github.com/wso2/aep/aep-api/internal/platform/modelcost"
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

	// RecordUsage stamps the cycle's captured token usage onto the row, and its
	// write-time USD onto cost_usd (#291).
	//
	// It is the ONE mutator NOT guarded on the cycle being open, and that is the
	// whole point: usage arrives from the terminal-log capture, and a cycle
	// CLOSES on the merge webhook seconds after its agent Job exits — routinely
	// before the watcher's next tick. Fencing this on ended_at IS NULL would
	// discard nearly every capture. Idempotent by value: the capture re-derives
	// the same figures from the same log, so a repeat write is a no-op in effect.
	RecordUsage(ctx context.Context, id string, u contracts.TokenUsage) error

	// SumUsageByProjectPhase rolls up captured CYCLE usage per project across an
	// org, split into the build and validation SDLC phases (#291) — delivery's
	// agent spend, since every token-burning dispatch is a cycle after the
	// issue-driven flip. Validation cycles are the validation phase; coding, fix
	// and conflict cycles are the build phase (the UsagePhase* constants). Each map
	// is keyed by project id; CostUsd sums the frozen per-row stamps and is nil
	// when no contributing row was stamped.
	SumUsageByProjectPhase(ctx context.Context, orgID string) (build, validation map[string]contracts.StampedUsage, err error)
}

type runCycleRepository struct {
	db      *gorm.DB
	stamper *modelcost.Stamper
}

// NewRunCycleRepository wires the gorm-backed repository. stamper prices
// captured cycle usage at write time (#291); nil disables stamping (tests) and
// cost_usd stays null.
func NewRunCycleRepository(db *gorm.DB, stamper *modelcost.Stamper) RunCycleRepository {
	return &runCycleRepository{db: db, stamper: stamper}
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

func (r *runCycleRepository) RecordUsage(ctx context.Context, id string, u contracts.TokenUsage) error {
	updates := map[string]any{
		"input_tokens":          u.InputTokens,
		"output_tokens":         u.OutputTokens,
		"cache_read_tokens":     u.CacheReadTokens,
		"cache_creation_tokens": u.CacheCreationTokens,
		"model_id":              u.Model,
	}
	// Stamp USD at capture from the rates in force now (#291): frozen on the row,
	// never re-derived. Null when unpriceable (no rate row / no model).
	if r.stamper != nil {
		updates["cost_usd"] = r.stamper.Cost(modelcost.Tokens{
			ModelID:             u.Model,
			InputTokens:         u.InputTokens,
			OutputTokens:        u.OutputTokens,
			CacheReadTokens:     u.CacheReadTokens,
			CacheCreationTokens: u.CacheCreationTokens,
		})
	}
	// NOT applyOpen: see RecordUsage's contract — a closed cycle is exactly the
	// case this has to serve.
	return r.db.WithContext(ctx).
		Model(&RunCycle{}).
		Where("id = ?", id).
		Updates(updates).Error
}

func (r *runCycleRepository) SumUsageByProjectPhase(ctx context.Context, orgID string) (build, validation map[string]contracts.StampedUsage, err error) {
	var rows []cyclePhaseUsageRow
	err = r.db.WithContext(ctx).
		Model(&RunCycle{}).
		Select("project_id, "+
			// Validation is its own phase; every other kind (coding, fix, conflict) is
			// the build phase. This CASE is where that classification LIVES — see the
			// UsagePhase* constants.
			"CASE WHEN kind = ? THEN ? ELSE ? END AS phase, "+
			"COALESCE(SUM(input_tokens),0) AS input_tokens, "+
			"COALESCE(SUM(output_tokens),0) AS output_tokens, "+
			"COALESCE(SUM(cache_read_tokens),0) AS cache_read_tokens, "+
			"COALESCE(SUM(cache_creation_tokens),0) AS cache_creation_tokens, "+
			"SUM(cost_usd) AS cost_usd, "+ // NULL when no row is stamped — the #291 semantic
			// The phase's model id survives only while every contributor THAT SPENT
			// TOKENS agrees on it — exactly contracts.TokenUsage.Add, which keeps the
			// model across a zero-token contributor and blanks it on a genuine
			// disagreement. Hence the CASE: a cycle that captured nothing carries
			// model_id '' and must not drag a single-model phase to "unknown".
			// COUNT(DISTINCT …) and MAX(…) both ignore NULL, so those rows drop out.
			"COUNT(DISTINCT CASE WHEN "+cycleHasTokens+" THEN model_id END) AS models, "+
			"COALESCE(MAX(CASE WHEN "+cycleHasTokens+" THEN model_id END), '') AS max_model",
			CycleKindValidation, UsagePhaseValidation, UsagePhaseBuild).
		Where("org_id = ? AND project_id <> ''", orgID).
		Group("project_id, phase").
		// Only phases with real token traffic — a cycle that captured nothing
		// leaves a 0-token row that must not conjure a phase out of nothing.
		Having("SUM(input_tokens) + SUM(output_tokens) + SUM(cache_read_tokens) + SUM(cache_creation_tokens) > 0").
		Scan(&rows).Error
	if err != nil {
		return nil, nil, err
	}
	build = make(map[string]contracts.StampedUsage)
	validation = make(map[string]contracts.StampedUsage)
	for _, row := range rows {
		u := contracts.TokenUsage{
			InputTokens:         row.InputTokens,
			OutputTokens:        row.OutputTokens,
			CacheReadTokens:     row.CacheReadTokens,
			CacheCreationTokens: row.CacheCreationTokens,
		}
		if row.Models == 1 {
			u.Model = row.MaxModel
		}
		stamped := contracts.StampedUsage{Tokens: u, CostUsd: row.CostUsd}
		if row.Phase == UsagePhaseValidation {
			validation[row.ProjectID] = stamped
		} else {
			build[row.ProjectID] = stamped
		}
	}
	return build, validation, nil
}

// cycleHasTokens is the "this row actually spent something" predicate, shared by
// the model-agreement CASEs so the notion of a contributing row is spelled once.
const cycleHasTokens = "input_tokens + output_tokens + cache_read_tokens + cache_creation_tokens > 0"

// cyclePhaseUsageRow is the per-(project, phase) aggregate scan shape (#291).
type cyclePhaseUsageRow struct {
	ProjectID           string
	Phase               string
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	CostUsd             *float64
	Models              int64
	MaxModel            string
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
