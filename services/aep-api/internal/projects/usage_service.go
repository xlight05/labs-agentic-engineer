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
	"sort"

	"github.com/wso2/aep/aep-api/internal/contracts"
	"github.com/wso2/aep/aep-api/internal/gen"
)

// UsageByProjectFunc rolls up captured agent usage per project (slug) across an
// org — the shape both capture domains expose (spec turns, delivery
// executions). CostUsd sums the frozen per-row stamps; nil when a project has
// no stamped row.
type UsageByProjectFunc func(ctx context.Context, orgID string) (map[string]contracts.StampedUsage, error)

// ProjectNameLister resolves the org's live projects to slug → display name.
// It is the *Service's ListProjects behind a narrow port so the usage service
// can mark deleted projects (a usage slug with no live project) and label the
// rest. Returning only the live set is enough: any slug missing from it is a
// deleted project whose spend still counts.
type ProjectNameLister func(ctx context.Context, orgID string) (map[string]string, error)

// ExecPhaseUsageFunc rolls up captured execution usage per project split into
// the build and validation phases — the delivery repository's shape.
type ExecPhaseUsageFunc func(ctx context.Context, orgID string) (build, validation map[string]contracts.StampedUsage, err error)

// UsageService assembles the org-wide Settings → Usage roll-up (#291): per
// project it folds the three SDLC phases (spec/design turns, build + coding
// executions, validation executions) into a lifetime total and a per-phase
// split, labels each project (live name, or the slug for a since-deleted one),
// and orders the cards so the biggest spenders lead. It reprices nothing —
// every costUsd is a sum of stamps frozen at capture (amended ADR-0011).
type UsageService struct {
	specUsage      UsageByProjectFunc
	execPhaseUsage ExecPhaseUsageFunc
	liveNames      ProjectNameLister
}

// NewUsageService wires the roll-up from the spec-turn source, the phase-split
// execution source, and the live project lister.
func NewUsageService(specUsage UsageByProjectFunc, execPhaseUsage ExecPhaseUsageFunc, liveNames ProjectNameLister) *UsageService {
	return &UsageService{specUsage: specUsage, execPhaseUsage: execPhaseUsage, liveNames: liveNames}
}

// ListProjectUsage returns a card for every LIVE project in the org (zero usage
// shown as $0 for ones that have not run an agent yet) plus every since-deleted
// project that still carries usage (greyed). Each card carries a per-phase
// split alongside its total. Ordered: stamped cost desc, then unpriced-but-
// active by tokens, then the $0 idle projects last.
func (s *UsageService) ListProjectUsage(ctx context.Context, orgID string) (gen.ProjectUsageList, error) {
	spec, err := s.specUsage(ctx, orgID)
	if err != nil {
		return gen.ProjectUsageList{}, err
	}
	build, validation, err := s.execPhaseUsage(ctx, orgID)
	if err != nil {
		return gen.ProjectUsageList{}, err
	}
	names, err := s.liveNames(ctx, orgID)
	if err != nil {
		return gen.ProjectUsageList{}, err
	}

	// The set of projects with real spend is the union of the three phase maps
	// (each already drops zero-token projects).
	slugs := map[string]struct{}{}
	for slug := range spec {
		slugs[slug] = struct{}{}
	}
	for slug := range build {
		slugs[slug] = struct{}{}
	}
	for slug := range validation {
		slugs[slug] = struct{}{}
	}

	cards := make([]gen.ProjectUsageCard, 0, len(slugs)+len(names))
	for slug := range slugs {
		displayName, live := names[slug]
		if !live {
			displayName = slug // deleted project: fall back to the stored slug
		}
		total := spec[slug].Add(build[slug]).Add(validation[slug])
		cards = append(cards, gen.ProjectUsageCard{
			ProjectName: slug,
			DisplayName: displayName,
			Deleted:     !live,
			Usage:       toGenUsage(total),
			Phases: gen.PhaseUsage{
				Spec:       phaseUsage(spec, slug),
				Build:      phaseUsage(build, slug),
				Validation: phaseUsage(validation, slug),
			},
		})
	}
	// Every live project with no spend yet gets a $0 card so the page lists the
	// org's projects, not only the ones that happen to have run an agent.
	for slug, displayName := range names {
		if _, hasSpend := slugs[slug]; hasSpend {
			continue
		}
		cards = append(cards, gen.ProjectUsageCard{
			ProjectName: slug,
			DisplayName: displayName,
			Deleted:     false,
			Usage:       zeroUsage(),
			Phases:      gen.PhaseUsage{Spec: zeroUsage(), Build: zeroUsage(), Validation: zeroUsage()},
		})
	}
	sortUsageCards(cards)
	return gen.ProjectUsageList{Projects: cards}, nil
}

// phaseUsage maps a project's phase entry to wire Usage: a captured phase keeps
// its stamped/unpriced figures; a phase that never ran shows a real $0 rather
// than a null "0 tok", so all three phase rows read cleanly.
func phaseUsage(m map[string]contracts.StampedUsage, slug string) gen.Usage {
	if u, ok := m[slug]; ok {
		return toGenUsage(u)
	}
	return zeroUsage()
}

// zeroUsage is an idle phase/project: a real $0 (the model is known to be
// nothing spent, distinct from an unpriced null on a captured row).
func zeroUsage() gen.Usage {
	zero := 0.0
	return gen.Usage{CostUsd: &zero}
}

// toGenUsage maps the internal stamped aggregate to the wire Usage shape.
func toGenUsage(u contracts.StampedUsage) gen.Usage {
	return gen.Usage{
		InputTokens:         u.Tokens.InputTokens,
		OutputTokens:        u.Tokens.OutputTokens,
		CacheReadTokens:     u.Tokens.CacheReadTokens,
		CacheCreationTokens: u.Tokens.CacheCreationTokens,
		Model:               u.Tokens.Model,
		CostUsd:             u.CostUsd,
	}
}

// sortUsageCards orders cards into three intuitive tiers, each broken by slug:
//  0. real stamped spend (costUsd > 0) — by cost descending (biggest first)
//  1. captured usage the platform could not price (null cost) — by total tokens
//     descending, so a busy unstamped project still ranks above idle ones
//  2. idle projects ($0, no tokens) — last
func sortUsageCards(cards []gen.ProjectUsageCard) {
	tier := func(c gen.ProjectUsageCard) int {
		switch {
		case c.Usage.CostUsd != nil && *c.Usage.CostUsd > 0:
			return 0
		case tokenTotal(c.Usage) > 0:
			return 1 // has usage but null (or $0-but-nonzero-token) cost
		default:
			return 2 // idle $0
		}
	}
	sort.SliceStable(cards, func(i, j int) bool {
		ti, tj := tier(cards[i]), tier(cards[j])
		if ti != tj {
			return ti < tj
		}
		switch ti {
		case 0:
			if a, b := *cards[i].Usage.CostUsd, *cards[j].Usage.CostUsd; a != b {
				return a > b
			}
		case 1:
			if a, b := tokenTotal(cards[i].Usage), tokenTotal(cards[j].Usage); a != b {
				return a > b
			}
		}
		return cards[i].ProjectName < cards[j].ProjectName
	})
}

func tokenTotal(u gen.Usage) int64 {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheCreationTokens
}
