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

// Package modelcost owns model pricing and the write-time USD stamp (#291,
// amended ADR-0011). Rates are DATA — a model_rates row per model — not
// service config. USD is stamped at CAPTURE time from the rates then in force
// and persisted on the usage row; a later rate change never reprices history.
//
// The Stamper is an immutable in-memory price card built once at boot from the
// model_rates table (rates are ops-managed and change rarely, so a per-capture
// DB read would be wasteful; a rate edit takes effect on the next restart).
// This package imports no gorm: ModelRate carries struct tags only, migrate
// owns its AutoMigrate + seed, and app owns the boot-time load.
package modelcost

import "math"

// ModelRate is the model_rates row: one model's USD-per-MTok price card.
// Struct tags only — the gorm dependency lives in migrate (AutoMigrate) and
// app (boot load), never here (§ arch: gorm is fenced to repositories + the
// kernel allowlist).
type ModelRate struct {
	ModelID           string  `gorm:"primaryKey;type:text"`
	InputPerMTok      float64 `gorm:"not null;default:0"`
	OutputPerMTok     float64 `gorm:"not null;default:0"`
	CacheReadPerMTok  float64 `gorm:"not null;default:0"`
	CacheWritePerMTok float64 `gorm:"not null;default:0"`
	UpdatedAt         int64   `gorm:"autoUpdateTime:milli"`
}

// TableName pins the table so the entity can live in platform/ rather than a
// domain package without gorm inferring "model_rates" differently.
func (ModelRate) TableName() string { return "model_rates" }

// Tokens is the capture-time token count a stamp prices. It mirrors
// contracts.TokenUsage's fields without importing it — modelcost is a leaf the
// capture paths depend on, so it must not depend back on the contract shape.
type Tokens struct {
	ModelID             string
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

// Stamper prices token counts against a fixed set of rates loaded at boot.
// Immutable after New — safe to share across the spec + delivery capture paths
// with no locking.
type Stamper struct {
	rates map[string]ModelRate
}

// NewStamper builds the price card from the model_rates rows.
func NewStamper(rows []ModelRate) *Stamper {
	rates := make(map[string]ModelRate, len(rows))
	for _, r := range rows {
		rates[r.ModelID] = r
	}
	return &Stamper{rates: rates}
}

// Cost returns the stamped USD for a captured record (rounded to cents), or
// nil when it cannot be priced: no model id, or no rate row for that model.
// A nil stamp persists as a null cost_usd — the console then shows tokens
// only for any aggregate that includes the unstamped row. The historical
// record is immutable: this is computed once, at capture, and never revisited.
func (s *Stamper) Cost(t Tokens) *float64 {
	rate, ok := s.rates[t.ModelID]
	if !ok || t.ModelID == "" {
		return nil
	}
	usd := (float64(t.InputTokens)*rate.InputPerMTok +
		float64(t.OutputTokens)*rate.OutputPerMTok +
		float64(t.CacheReadTokens)*rate.CacheReadPerMTok +
		float64(t.CacheCreationTokens)*rate.CacheWritePerMTok) / 1e6
	usd = math.Round(usd*100) / 100
	return &usd
}
