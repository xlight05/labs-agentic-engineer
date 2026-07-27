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

// run_cycle_logs.go — the milestone loop's captured agent log.
//
// The cycle-keyed twin of coding_agent_logs. It cannot be that table: it is
// FK'd to executions(id), and a milestone run mints no execution row, so a
// cycle's Job log has nothing there to hang off. Keyed instead to
// run_cycles(id), cascading on the cycle so a project delete takes its logs.
//
// Written as raw SQL rather than an AutoMigrate base model for the same reason
// coding_agent_logs is: the FK and the composite primary key are the point, and
// keeping the two sidecars shaped identically makes the pair easy to read.
// Runs after milestone_runs, whose entities (run_cycles included) AutoMigrate at
// database.Open — so the FK target exists.
func RunRunCycleLogs(ctx context.Context, db *gorm.DB) error {
	stmt := `
		CREATE TABLE IF NOT EXISTS run_cycle_logs (
		  cycle_id     UUID         NOT NULL REFERENCES run_cycles(id) ON DELETE CASCADE,
		  run_name     TEXT         NOT NULL,
		  final_phase  TEXT         NOT NULL,
		  captured_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
		  log_text     TEXT         NOT NULL,
		  size_bytes   BIGINT       NOT NULL,
		  PRIMARY KEY (cycle_id, run_name)
		);
		CREATE INDEX IF NOT EXISTS idx_run_cycle_logs_cycle_id ON run_cycle_logs(cycle_id);`
	if err := db.WithContext(ctx).Exec(stmt).Error; err != nil {
		return fmt.Errorf("run_cycle_logs: %w", err)
	}
	return nil
}
