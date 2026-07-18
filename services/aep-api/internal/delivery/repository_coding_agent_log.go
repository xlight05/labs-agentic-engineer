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

// CodingAgentLogRepository is the coding_agent_logs store: the JobWatcher
// captures a terminal agent pod's stdout once, keyed by (execution id, run
// name), and the AgentProgressReader reads that snapshot back for the log
// stream's final page. Extracted out of internal/delivery/codingagent during the
// delivery fold (P6) so the domain lands gorm-free — the ORM stays fenced to
// repositories/ (the gorm-into-<domain>/repository.go move defers to P9 with the
// entity, as every domain's did). Lookups miss with (nil, nil), matching the
// house convention.
type CodingAgentLogRepository interface {
	// GetByRun returns the captured log for (executionID, runName), or (nil,
	// nil) when none has been persisted yet — the pre-capture live-tail window.
	GetByRun(ctx context.Context, executionID uuid.UUID, runName string) (*CodingAgentLog, error)

	// Create persists the final captured log. Idempotency on (task_id, run_name)
	// is the caller's GetByRun-first guard, mirroring the extracted code.
	Create(ctx context.Context, row *CodingAgentLog) error
}

type codingAgentLogRepository struct {
	db *gorm.DB
}

// NewCodingAgentLogRepository builds the coding_agent_logs store.
func NewCodingAgentLogRepository(db *gorm.DB) CodingAgentLogRepository {
	return &codingAgentLogRepository{db: db}
}

func (r *codingAgentLogRepository) GetByRun(ctx context.Context, executionID uuid.UUID, runName string) (*CodingAgentLog, error) {
	var row CodingAgentLog
	err := r.db.WithContext(ctx).
		Where("task_id = ? AND run_name = ?", executionID, runName).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *codingAgentLogRepository) Create(ctx context.Context, row *CodingAgentLog) error {
	return r.db.WithContext(ctx).Create(row).Error
}
