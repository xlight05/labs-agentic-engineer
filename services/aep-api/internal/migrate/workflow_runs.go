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

// RunWorkflowRuns creates the one-running-task-workflow-per-issue partial
// unique index on the workflow_runs table (the Temporal devflow lookup
// index): at most one running task workflow per (repo, issue_number), so a
// webhook signaler always resolves to a single workflow. AutoMigrate creates
// the table from the model but cannot express a partial (WHERE-clause)
// index, so it is added here.
//
// Idempotent: CREATE UNIQUE INDEX IF NOT EXISTS is a no-op on re-run, and
// the step no-ops entirely if the table is not present yet.
func RunWorkflowRuns(ctx context.Context, db *gorm.DB) error {
	if !hasTable(db, "workflow_runs") {
		return nil
	}
	if err := db.WithContext(ctx).Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS ux_workflow_runs_task_running
		ON workflow_runs (repo, issue_number)
		WHERE kind = 'task' AND status = 'running'`).Error; err != nil {
		return fmt.Errorf("workflow_runs task-running index: %w", err)
	}
	return nil
}
