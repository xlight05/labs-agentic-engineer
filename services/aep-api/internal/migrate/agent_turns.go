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

package migrate

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// RunAgentTurns creates the one-active-turn-per-project partial unique index
// on the agent_turns table: at most one running turn per (org_id, project_id),
// across every use case. Turn start is INSERT ... ON CONFLICT DO NOTHING
// against this index, so racing POSTs resolve to exactly one admitted turn
// and the loser reads the active row for its 409 {activeTurnId}. AutoMigrate
// creates the table from the model but cannot express a partial (WHERE-clause)
// index.
//
// Idempotent: CREATE UNIQUE INDEX IF NOT EXISTS is a no-op on re-run, and the
// step no-ops entirely if the table is not present yet.
func RunAgentTurns(ctx context.Context, db *gorm.DB) error {
	if !hasTable(db, "agent_turns") {
		return nil
	}
	if err := db.WithContext(ctx).Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS ux_agent_turns_active
		ON agent_turns (org_id, project_id)
		WHERE status = 'running'`).Error; err != nil {
		return fmt.Errorf("agent_turns active-guard index: %w", err)
	}
	return nil
}
