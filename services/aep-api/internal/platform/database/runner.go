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
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
)

// The migration MECHANISM — how a step runs, not which steps exist or in what
// order (docs/design/domain-oriented-architecture.md §7).
//
// The split is what keeps this kernel package domain-free: the ordered LIST
// names domain-owned steps and entities, so it lives in internal/migrate, which
// (like internal/edge) is permitted to import every domain. Nothing here knows a
// single table name.

// StepTimeout bounds each context-taking migration. Plain *gorm.DB migrations
// manage their own lifetimes and ignore it.
const StepTimeout = 30 * time.Second

// Step is one ordered schema migration, reduced to a name and a run func so the
// runner needs no knowledge of what it does.
type Step struct {
	Name string
	Run  func(context.Context) error
}

// DBStep adapts a migration that takes only *gorm.DB and manages its own
// timeout.
func DBStep(name string, db *gorm.DB, fn func(*gorm.DB) error) Step {
	return Step{Name: name, Run: func(context.Context) error { return fn(db) }}
}

// CtxStep adapts a context-taking migration, giving it a per-step timeout.
func CtxStep(name string, db *gorm.DB, fn func(context.Context, *gorm.DB) error) Step {
	return Step{Name: name, Run: func(ctx context.Context) error {
		c, cancel := context.WithTimeout(ctx, StepTimeout)
		defer cancel()
		return fn(c, db)
	}}
}

// Run applies every step in the ORDER GIVEN — the order is the caller's
// invariant, never re-derived here (it is load-bearing and non-obvious; see
// internal/migrate). Fails fast, naming the offending step.
func Run(ctx context.Context, steps []Step) error {
	for _, s := range steps {
		if err := s.Run(ctx); err != nil {
			return fmt.Errorf("%s migration: %w", s.Name, err)
		}
		slog.Debug("migration applied", "name", s.Name)
	}
	slog.Info("schema migrations applied", "count", len(steps))
	return nil
}
