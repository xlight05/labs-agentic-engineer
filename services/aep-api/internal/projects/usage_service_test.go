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

package projects

import (
	"context"
	"testing"

	"github.com/wso2/aep/aep-api/internal/contracts"
)

func usd(v float64) *float64 { return &v }

func stamped(input int64, cost *float64) contracts.StampedUsage {
	return contracts.StampedUsage{
		Tokens:  contracts.TokenUsage{InputTokens: input, Model: "claude-sonnet-5"},
		CostUsd: cost,
	}
}

// newTestUsageService wires the service from the three phase maps directly:
// spec (turns), build, validation (executions).
func newTestUsageService(spec, build, validation map[string]contracts.StampedUsage, live map[string]string) *UsageService {
	return NewUsageService(
		func(context.Context, string) (map[string]contracts.StampedUsage, error) { return spec, nil },
		func(context.Context, string) (map[string]contracts.StampedUsage, map[string]contracts.StampedUsage, error) {
			return build, validation, nil
		},
		func(context.Context, string) (map[string]string, error) { return live, nil },
	)
}

// The headline behaviour: spec + delivery fold per project; every live project
// gets a card (idle ones as $0); a since-deleted project keeps its spend
// (greyed, slug as name); and the tiers order stamped > unpriced-active > idle.
func TestListProjectUsageFoldsOrdersAndLabels(t *testing.T) {
	turns := map[string]contracts.StampedUsage{
		"storefront":  stamped(100_000, usd(3.00)),
		"legacy-crm":  stamped(50_000, usd(1.50)), // deleted project (not in live set)
		"spike-notes": stamped(20_000, nil),       // pre-stamping rows: tokens only
	}
	execs := map[string]contracts.StampedUsage{
		"storefront": stamped(200_000, usd(5.00)), // folds with the turn row → $8.00
	}
	live := map[string]string{
		"storefront":  "Storefront Webapp",
		"spike-notes": "Spike Notes",
		"fresh-idea":  "Fresh Idea", // brand-new live project, no usage yet
		// legacy-crm is absent → deleted
	}

	got, err := newTestUsageService(turns, execs, nil, live).ListProjectUsage(context.Background(), "org")
	if err != nil {
		t.Fatalf("ListProjectUsage: %v", err)
	}
	// storefront ($8), legacy-crm ($1.50), spike-notes (unpriced, tokens),
	// fresh-idea ($0 idle) — four cards.
	if len(got.Projects) != 4 {
		t.Fatalf("cards = %d, want 4", len(got.Projects))
	}

	// Tier 0 (stamped, cost desc): storefront then legacy-crm.
	if got.Projects[0].ProjectName != "storefront" {
		t.Errorf("first card = %q, want storefront", got.Projects[0].ProjectName)
	}
	if c := got.Projects[0].Usage.CostUsd; c == nil || *c != 8.00 {
		t.Errorf("storefront cost = %v, want 8.00 (turn $3 + exec $5)", c)
	}
	if got.Projects[0].Usage.InputTokens != 300_000 {
		t.Errorf("storefront input = %d, want 300000 (folded)", got.Projects[0].Usage.InputTokens)
	}
	if got.Projects[0].DisplayName != "Storefront Webapp" {
		t.Errorf("storefront displayName = %q, want live name", got.Projects[0].DisplayName)
	}
	// Per-phase split: spec $3 (turns), build $5 (execs), validation $0 (none ran).
	ph := got.Projects[0].Phases
	if c := ph.Spec.CostUsd; c == nil || *c != 3.00 {
		t.Errorf("storefront spec phase = %v, want 3.00", c)
	}
	if c := ph.Build.CostUsd; c == nil || *c != 5.00 {
		t.Errorf("storefront build phase = %v, want 5.00", c)
	}
	if c := ph.Validation.CostUsd; c == nil || *c != 0 {
		t.Errorf("storefront validation phase = %v, want $0 (never ran)", c)
	}

	deleted := got.Projects[1]
	if deleted.ProjectName != "legacy-crm" || !deleted.Deleted {
		t.Errorf("second card = %+v, want deleted legacy-crm", deleted)
	}
	if deleted.DisplayName != "legacy-crm" {
		t.Errorf("deleted displayName = %q, want slug fallback", deleted.DisplayName)
	}

	// Tier 1 (unpriced but has usage): spike-notes, before the idle project.
	third := got.Projects[2]
	if third.ProjectName != "spike-notes" || third.Usage.CostUsd != nil {
		t.Errorf("third card = %+v, want spike-notes with null cost", third)
	}

	// Tier 2 (idle live project): fresh-idea last, $0, not deleted.
	idle := got.Projects[3]
	if idle.ProjectName != "fresh-idea" || idle.Deleted {
		t.Errorf("last card = %+v, want live idle fresh-idea", idle)
	}
	if c := idle.Usage.CostUsd; c == nil || *c != 0 {
		t.Errorf("idle cost = %v, want $0 (not null)", c)
	}
	if tokenTotal(idle.Usage) != 0 {
		t.Errorf("idle tokens = %d, want 0", tokenTotal(idle.Usage))
	}
}

func TestListProjectUsageListsIdleLiveProjects(t *testing.T) {
	// No usage anywhere, but two live projects — both show as $0 cards.
	live := map[string]string{"alpha": "Alpha", "beta": "Beta"}
	got, err := newTestUsageService(nil, nil, nil, live).ListProjectUsage(context.Background(), "org")
	if err != nil {
		t.Fatalf("ListProjectUsage: %v", err)
	}
	if len(got.Projects) != 2 {
		t.Fatalf("cards = %d, want 2 idle live projects", len(got.Projects))
	}
	for _, c := range got.Projects {
		if c.Deleted {
			t.Errorf("%q marked deleted, want live", c.ProjectName)
		}
		if c.Usage.CostUsd == nil || *c.Usage.CostUsd != 0 {
			t.Errorf("%q cost = %v, want $0", c.ProjectName, c.Usage.CostUsd)
		}
	}
}

func TestListProjectUsageEmpty(t *testing.T) {
	got, err := newTestUsageService(nil, nil, nil, nil).ListProjectUsage(context.Background(), "org")
	if err != nil {
		t.Fatalf("ListProjectUsage: %v", err)
	}
	if len(got.Projects) != 0 {
		t.Fatalf("cards = %d, want 0 for an org with no projects or usage", len(got.Projects))
	}
}
