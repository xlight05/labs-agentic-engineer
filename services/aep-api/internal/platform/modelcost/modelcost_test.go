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

package modelcost

import "testing"

func sonnetStamper() *Stamper {
	return NewStamper([]ModelRate{{
		ModelID:           "claude-sonnet-5",
		InputPerMTok:      2.00,
		OutputPerMTok:     10.00,
		CacheReadPerMTok:  0.20,
		CacheWritePerMTok: 2.50,
	}})
}

func TestCostStampsAtSeededRates(t *testing.T) {
	s := sonnetStamper()
	// 1M input @ $2 + 1M output @ $10 + 1M cache-read @ $0.20 + 1M cache-write
	// @ $2.50 = $14.70, rounded to cents.
	got := s.Cost(Tokens{
		ModelID:             "claude-sonnet-5",
		InputTokens:         1_000_000,
		OutputTokens:        1_000_000,
		CacheReadTokens:     1_000_000,
		CacheCreationTokens: 1_000_000,
	})
	if got == nil {
		t.Fatal("expected a stamped cost, got nil")
	}
	if *got != 14.70 {
		t.Fatalf("cost = %v, want 14.70", *got)
	}
}

func TestCostRoundsToCents(t *testing.T) {
	s := sonnetStamper()
	// 12,345 input @ $2/MTok = $0.02469 → rounds to $0.02.
	got := s.Cost(Tokens{ModelID: "claude-sonnet-5", InputTokens: 12_345})
	if got == nil || *got != 0.02 {
		t.Fatalf("cost = %v, want 0.02", got)
	}
}

func TestCostNilWhenNoRateForModel(t *testing.T) {
	s := sonnetStamper()
	// A model with no rate row cannot be priced honestly — null, not zero.
	if got := s.Cost(Tokens{ModelID: "claude-opus-4-8", InputTokens: 1_000}); got != nil {
		t.Fatalf("cost = %v, want nil for an unpriced model", *got)
	}
}

func TestCostNilWhenNoModel(t *testing.T) {
	s := sonnetStamper()
	// A mixed-model aggregate (model "") is unpriceable at the row level.
	if got := s.Cost(Tokens{ModelID: "", InputTokens: 1_000}); got != nil {
		t.Fatalf("cost = %v, want nil for an empty model id", *got)
	}
}

func TestCostZeroTokensStampsZero(t *testing.T) {
	s := sonnetStamper()
	// A priced model with no traffic stamps $0 (distinct from an unpriceable
	// null): the model was known, the spend was genuinely nothing.
	got := s.Cost(Tokens{ModelID: "claude-sonnet-5"})
	if got == nil || *got != 0 {
		t.Fatalf("cost = %v, want 0", got)
	}
}
