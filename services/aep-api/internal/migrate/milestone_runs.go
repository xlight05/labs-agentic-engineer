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

// RunMilestoneRuns creates the spec-run mutex on the milestone_runs table: a
// partial unique index admitting at most ONE non-terminal spec-build run per
// (org_id, project_id). It is the database twin of the build endpoint's 409 —
// the endpoint answers the user, the index makes the invariant true under
// concurrency, because dispatch inserts with ON CONFLICT DO NOTHING against it
// and the losing racer writes zero rows.
//
// Incident-adoption runs are deliberately OUTSIDE the index: they work their
// own milestones and execute concurrently with each other and with a live spec
// run. Only 'waiting' and 'running' are covered, so a settled run never blocks
// the next build.
//
// AutoMigrate creates the milestone_runs and run_cycles tables from the models
// (migrate.BaseModels) but cannot express a partial (WHERE-clause) index, so it
// is added here.
//
// Idempotent: CREATE UNIQUE INDEX IF NOT EXISTS is a no-op on re-run, and the
// step no-ops entirely if the table is not present yet.
func RunMilestoneRuns(ctx context.Context, db *gorm.DB) error {
	if !hasTable(db, "milestone_runs") {
		return nil
	}
	if err := db.WithContext(ctx).Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS ux_milestone_runs_spec_active
		ON milestone_runs (org_id, project_id)
		WHERE origin = 'spec-build' AND state IN ('waiting', 'running')`).Error; err != nil {
		return fmt.Errorf("milestone_runs spec-run mutex index: %w", err)
	}
	return nil
}
