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

package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/wso2/aep/aep-api/models"
)

// The agent_turns store was extracted out of the genai turn engine (now internal/spec) during the
// spec-domain fold (P4): the ORM stays fenced to repositories/ while the turn
// VOCABULARY (the TurnTerminal shape, ErrTurnActive, the status/reason strings)
// re-exports back into the domain via type aliases, so the turn engine reads as
// one package. The gorm-into-<domain>/repository.go move defers to P9, as with
// organization's stores (docs/design/domain-oriented-architecture.md §19.5.1).

// Turn statuses (models.AgentTurn.Status).
const (
	turnStatusRunning   = "running"
	turnStatusCompleted = "completed"
	turnStatusFailed    = "failed"
)

// Failure reasons (models.AgentTurn.Reason and the terminal event's `reason`).
const (
	turnReasonStreamDied     = "stream-died"
	turnReasonFoldParity     = "fold-parity"
	turnReasonBaseMoved      = "base-moved"
	turnReasonDispatchFailed = "dispatch-failed"
	turnReasonInternal       = "internal"
)

// ErrTurnActive is returned by TryStart when another turn holds the D18
// one-active-turn-per-project guard; the accompanying row is the active turn.
var ErrTurnActive = errors.New("a turn is already running for this project")

// TurnTerminal is the terminal state Finish stamps onto a running row.
type TurnTerminal struct {
	Status    string // turnStatusCompleted | turnStatusFailed
	CommitSHA string
	Reason    string
	Paths     []string
	NoChanges bool
	Message   string
}

// TurnRepository is the agent_turns row store (design D17/D18): the durable
// turn record, the one-active guard, and the stale-heartbeat sweep. Lookups
// miss with (nil, nil), matching the house convention.
type TurnRepository interface {
	// TryStart INSERTs the running row; on conflict with the D18 partial
	// unique index it fetches and returns the active row alongside
	// ErrTurnActive. On success the passed row (ID populated) is returned.
	TryStart(ctx context.Context, t *models.AgentTurn) (*models.AgentTurn, error)

	// Heartbeat bumps heartbeat_at on a still-running row (no-op otherwise).
	Heartbeat(ctx context.Context, id string) error

	// Finish transitions a running row to its terminal state. Guarded on
	// status='running' so it never overwrites a swept/terminal row; returns
	// false when the row was not running anymore.
	Finish(ctx context.Context, id string, terminal TurnTerminal) (bool, error)

	// Get returns the turn only when it belongs to (orgID, projectID) — the
	// tenant fence for the status/stream endpoints. (nil, nil) on miss.
	Get(ctx context.Context, orgID, projectID, turnID string) (*models.AgentTurn, error)

	// GetActive returns the project's running turn, or (nil, nil).
	GetActive(ctx context.Context, orgID, projectID string) (*models.AgentTurn, error)

	// LastTerminal returns the most recent completed/failed turn of a
	// conversation — the D20 filesChangedExternally / divergence-note input.
	LastTerminal(ctx context.Context, orgID, projectID, conversationID string) (*models.AgentTurn, error)

	// SweepStale fails every running row whose heartbeat predates olderThan
	// (reason stream-died, message "replica crashed or hung") and returns the
	// swept rows so the caller can emit broker terminals.
	SweepStale(ctx context.Context, olderThan time.Time) ([]models.AgentTurn, error)
}

type turnRepository struct {
	db *gorm.DB
}

// NewTurnRepository builds the agent_turns store.
func NewTurnRepository(db *gorm.DB) TurnRepository {
	return &turnRepository{db: db}
}

func (r *turnRepository) TryStart(ctx context.Context, t *models.AgentTurn) (*models.AgentTurn, error) {
	if t.Status == "" {
		t.Status = turnStatusRunning
	}
	if t.HeartbeatAt.IsZero() {
		t.HeartbeatAt = time.Now().UTC()
	}
	// Two attempts: a conflict whose active row finished between our INSERT
	// and the GetActive read retries the insert once instead of failing.
	for attempt := 0; attempt < 2; attempt++ {
		res := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(t)
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected > 0 {
			return t, nil
		}
		active, err := r.GetActive(ctx, t.OrgID, t.ProjectID)
		if err != nil {
			return nil, err
		}
		if active != nil {
			return active, ErrTurnActive
		}
	}
	return nil, errors.New("genai: turn start raced the guard twice — give up")
}

func (r *turnRepository) Heartbeat(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).
		Model(&models.AgentTurn{}).
		Where("id = ? AND status = ?", id, turnStatusRunning).
		Update("heartbeat_at", time.Now().UTC()).Error
}

func (r *turnRepository) Finish(ctx context.Context, id string, terminal TurnTerminal) (bool, error) {
	res := r.db.WithContext(ctx).
		Model(&models.AgentTurn{}).
		Where("id = ? AND status = ?", id, turnStatusRunning).
		Updates(map[string]any{
			"status":     terminal.Status,
			"commit_sha": terminal.CommitSHA,
			"reason":     terminal.Reason,
			"paths":      encodePaths(terminal.Paths),
			"no_changes": terminal.NoChanges,
			"message":    terminal.Message,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *turnRepository) Get(ctx context.Context, orgID, projectID, turnID string) (*models.AgentTurn, error) {
	var t models.AgentTurn
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND project_id = ? AND id = ?", orgID, projectID, turnID).
		First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *turnRepository) GetActive(ctx context.Context, orgID, projectID string) (*models.AgentTurn, error) {
	var t models.AgentTurn
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND project_id = ? AND status = ?", orgID, projectID, turnStatusRunning).
		First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *turnRepository) LastTerminal(ctx context.Context, orgID, projectID, conversationID string) (*models.AgentTurn, error) {
	var t models.AgentTurn
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND project_id = ? AND conversation_id = ? AND status IN ?",
			orgID, projectID, conversationID, []string{turnStatusCompleted, turnStatusFailed}).
		Order("created_at DESC").
		First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *turnRepository) SweepStale(ctx context.Context, olderThan time.Time) ([]models.AgentTurn, error) {
	// One guarded UPDATE ... RETURNING so concurrent sweeps (or a Finish
	// racing the sweep) each claim a row at most once.
	var swept []models.AgentTurn
	err := r.db.WithContext(ctx).Raw(`
		UPDATE agent_turns
		SET status = ?, reason = ?, message = ?, updated_at = now()
		WHERE status = ? AND heartbeat_at < ?
		RETURNING *`,
		turnStatusFailed, turnReasonStreamDied, "replica crashed or hung",
		turnStatusRunning, olderThan.UTC()).
		Scan(&swept).Error
	if err != nil {
		return nil, err
	}
	return swept, nil
}

// encodePaths stores the conflicting-path list as a JSON array ("" for none).
func encodePaths(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	b, err := json.Marshal(paths)
	if err != nil {
		return ""
	}
	return string(b)
}

// decodePaths reads the JSON array back (nil for empty/invalid).
func decodePaths(raw string) []string {
	if raw == "" {
		return nil
	}
	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		return nil
	}
	return paths
}
