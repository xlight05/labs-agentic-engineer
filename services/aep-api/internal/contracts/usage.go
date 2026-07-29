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

package contracts

// TokenUsage is the cross-runtime token-usage shape (#245/#249): what both
// agent runtimes report per unit of work and what the usage columns persist
// (amended ADR-0011 — tokens + model are the stored truth; USD is stamped at
// capture time onto the row's cost_usd from the model_rates then in force, and
// is not carried on this shape). Field names match the runner NDJSON `usage`
// object and the agents-stream manifest `usage` field byte-for-byte.
type TokenUsage struct {
	InputTokens         int64 `json:"inputTokens"`
	OutputTokens        int64 `json:"outputTokens"`
	CacheReadTokens     int64 `json:"cacheReadTokens"`
	CacheCreationTokens int64 `json:"cacheCreationTokens"`
	// Model is the model id the work ran on; "" on aggregates that mix
	// models (or when the runtime could not determine it).
	Model string `json:"model,omitempty"`
}

// IsZero reports a record with no token traffic at all.
func (u TokenUsage) IsZero() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheReadTokens == 0 && u.CacheCreationTokens == 0
}

// Add folds another record into an aggregate: tokens sum; the model id
// survives only while every non-zero contributor agrees on it.
func (u TokenUsage) Add(other TokenUsage) TokenUsage {
	model := u.Model
	switch {
	case u.IsZero():
		model = other.Model
	case other.IsZero():
		// keep u.Model
	case u.Model != other.Model:
		model = ""
	}
	return TokenUsage{
		InputTokens:         u.InputTokens + other.InputTokens,
		OutputTokens:        u.OutputTokens + other.OutputTokens,
		CacheReadTokens:     u.CacheReadTokens + other.CacheReadTokens,
		CacheCreationTokens: u.CacheCreationTokens + other.CacheCreationTokens,
		Model:               model,
	}
}

// StampedUsage is an aggregate of usage rows plus the sum of the USD stamped on
// them at capture (#291). CostUsd is nil when NO contributing row carried a
// stamp (SQL SUM(cost_usd) over all-null rows is NULL) — the console then shows
// tokens only. It never reprices: this sums frozen per-row stamps.
type StampedUsage struct {
	Tokens  TokenUsage
	CostUsd *float64
}

// Add folds another stamped aggregate in: tokens via TokenUsage.Add, and the
// USD sums only where present — nil + nil stays nil, nil + x is x.
func (s StampedUsage) Add(other StampedUsage) StampedUsage {
	return StampedUsage{
		Tokens:  s.Tokens.Add(other.Tokens),
		CostUsd: addCost(s.CostUsd, other.CostUsd),
	}
}

func addCost(a, b *float64) *float64 {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	default:
		sum := *a + *b
		return &sum
	}
}
