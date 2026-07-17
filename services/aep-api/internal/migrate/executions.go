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

// RunExecutions creates the admission-mutex partial unique index on the
// executions table (docs/design/tasks-github-native.md §5): at most one active
// (queued|running) Execution per (repo, issue_number, kind). This is the
// authoritative dispatch lock — the funnel's gate check is advisory, and
// dispatch begins with INSERT … ON CONFLICT DO NOTHING against this index, so
// racing entrants (webhook delivery ∥ reconciliation sweep) resolve to exactly
// one admitted Execution. AutoMigrate creates the executions table from the
// model but cannot express a partial (WHERE-clause) index, so it is added here.
//
// Idempotent: CREATE UNIQUE INDEX IF NOT EXISTS is a no-op on re-run, and the
// step no-ops entirely if the table is not present yet.
func RunExecutions(ctx context.Context, db *gorm.DB) error {
	if !hasTable(db, "executions") {
		return nil
	}
	if err := db.WithContext(ctx).Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS ux_executions_admission
		ON executions (repo, issue_number, kind)
		WHERE status IN ('queued', 'running')`).Error; err != nil {
		return fmt.Errorf("executions admission-mutex index: %w", err)
	}
	return nil
}
