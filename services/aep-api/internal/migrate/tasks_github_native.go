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
	"fmt"
	"log/slog"

	"gorm.io/gorm"
)

// tableExists reports whether a public table exists — the guard the legacy
// component_tasks migrations use so they no-op on a fresh DB. Under the
// tasks-github-native model the base AutoMigrate no longer creates
// component_tasks (Tasks are GitHub issues), so on a fresh DB the table never
// exists and every component_tasks column-add migration must skip rather than
// error on a missing table.
func tableExists(db *gorm.DB, name string) bool {
	var exists struct{ Exists bool }
	if err := db.Raw(
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=?)`,
		name,
	).Scan(&exists).Error; err != nil {
		return false
	}
	return exists.Exists
}

// RunTasksGitHubNative is the tasks-github-native cutover migration
// (docs/design/tasks-github-native.md §10.1, §12): drop the component_tasks
// table (Tasks are GitHub issues now — no task rows) and the
// git_repositories.github_project_id cache column (GitHub Projects v2 is gone).
// Both were AutoMigrate-created, so AutoMigrate never drops them — this is the
// explicit SQL. Runs last (after the git_repositories AutoMigrate step), and is
// idempotent (IF EXISTS). On a fresh DB both are already absent → no-ops.
func RunTasksGitHubNative(db *gorm.DB) error {
	stmts := []string{
		`DROP TABLE IF EXISTS component_tasks CASCADE`,
		`ALTER TABLE IF EXISTS git_repositories DROP COLUMN IF EXISTS github_project_id`,
	}
	for _, ddl := range stmts {
		if err := db.Exec(ddl).Error; err != nil {
			return fmt.Errorf("tasks_github_native migration: %q: %w", ddl, err)
		}
	}
	slog.Info("tasks_github_native migration: dropped component_tasks + git_repositories.github_project_id")
	return nil
}
