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

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// RunCycleLogRepository is the run_cycle_logs store: the JobWatcher captures a
// terminal cycle pod's stdout once, keyed by (cycle id, run name), and the run
// progress stream reads that snapshot back once the pod is gone. Lookups miss
// with (nil, nil), matching the house convention.
type RunCycleLogRepository interface {
	// GetByRun returns the captured log for (cycleID, runName), or (nil, nil)
	// when none has been persisted yet — the pre-capture live-tail window.
	GetByRun(ctx context.Context, cycleID uuid.UUID, runName string) (*RunCycleLog, error)

	// Create persists the final captured log. Idempotency on (cycle_id,
	// run_name) is the caller's GetByRun-first guard, matching the
	// coding_agent_logs precedent.
	Create(ctx context.Context, row *RunCycleLog) error
}

type runCycleLogRepository struct{ db *gorm.DB }

// NewRunCycleLogRepository builds the run_cycle_logs store.
func NewRunCycleLogRepository(db *gorm.DB) RunCycleLogRepository {
	return &runCycleLogRepository{db: db}
}

func (r *runCycleLogRepository) GetByRun(ctx context.Context, cycleID uuid.UUID, runName string) (*RunCycleLog, error) {
	var row RunCycleLog
	err := r.db.WithContext(ctx).
		Where("cycle_id = ? AND run_name = ?", cycleID, runName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *runCycleLogRepository) Create(ctx context.Context, row *RunCycleLog) error {
	return r.db.WithContext(ctx).Create(row).Error
}
