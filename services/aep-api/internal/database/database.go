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

package database

import (
	"fmt"
	"log/slog"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/wso2/aep/aep-api/models"
)

// BaseModels is the single source of truth for the AutoMigrate set that must
// exist before migrations.RunAll runs its ALTERs — the tables RunAll assumes
// already exist. Both the production boot path (cmd/aep-api/main.go) and the
// dbtest template migrator build the schema from this one list, so the two can
// never drift. Tasks are GitHub issues now (no component_tasks table), and
// org_credentials lives in git-service — the BFF neither auto-migrates nor reads
// it locally.
func BaseModels() []any {
	return []any{
		&models.ComponentConfig{},
		&models.WebhookDelivery{},
		&models.WebhookPayload{},
		&models.Organization{},
		&models.Execution{},
		&models.AgentTurn{},
		&models.DevflowRun{},
	}
}

// Open connects to the PostgreSQL database and auto-migrates the given models.
func Open(dsn string, models ...any) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger:                 gormlogger.Default.LogMode(gormlogger.Silent),
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	if len(models) > 0 {
		if err := db.AutoMigrate(models...); err != nil {
			return nil, fmt.Errorf("auto-migrate: %w", err)
		}
	}

	slog.Info("database connected and migrated")
	return db, nil
}
