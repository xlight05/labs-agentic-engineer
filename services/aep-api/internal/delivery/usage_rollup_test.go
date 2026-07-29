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

package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/wso2/aep/aep-api/internal/contracts"
)

// fakePhaseUsage is the canned answer either repository's phase-split read
// returns. Held as a NAMED field rather than embedded: embedding it beside the
// repository interface would make SumUsageByProjectPhase an ambiguous selector,
// and the fake would satisfy neither interface.
type fakePhaseUsage struct {
	build      map[string]contracts.StampedUsage
	validation map[string]contracts.StampedUsage
	err        error
}

func (f fakePhaseUsage) result() (map[string]contracts.StampedUsage, map[string]contracts.StampedUsage, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.build, f.validation, nil
}

// Each fake embeds its repository interface for the methods the rollup never
// calls (nil — calling one would panic, which is the point) and answers only
// the read under test.
type fakeExecUsage struct {
	ExecutionRepository
	usage fakePhaseUsage
}

func (f fakeExecUsage) SumUsageByProjectPhase(context.Context, string) (map[string]contracts.StampedUsage, map[string]contracts.StampedUsage, error) {
	return f.usage.result()
}

type fakeCycleUsage struct {
	RunCycleRepository
	usage fakePhaseUsage
}

func (f fakeCycleUsage) SumUsageByProjectPhase(context.Context, string) (map[string]contracts.StampedUsage, map[string]contracts.StampedUsage, error) {
	return f.usage.result()
}

func usd(v float64) *float64 { return &v }

func stamped(in, out int64, model string, cost *float64) contracts.StampedUsage {
	return contracts.StampedUsage{
		Tokens:  contracts.TokenUsage{InputTokens: in, OutputTokens: out, Model: model},
		CostUsd: cost,
	}
}

// The whole point of the rollup: a project's build spend is its cycle spend PLUS
// any execution spend, not one or the other. Before cycles were summed the build
// phase read empty for every project, because every agent run is a cycle.
func TestPhaseUsageRollup_SumsCyclesAndExecutions(t *testing.T) {
	cycles := fakeCycleUsage{usage: fakePhaseUsage{
		build:      map[string]contracts.StampedUsage{"shop": stamped(100, 20, "m1", usd(3))},
		validation: map[string]contracts.StampedUsage{"shop": stamped(10, 2, "m1", usd(1))},
	}}
	execs := fakeExecUsage{usage: fakePhaseUsage{
		build: map[string]contracts.StampedUsage{"shop": stamped(5, 1, "m1", usd(2))},
	}}

	build, validation, err := PhaseUsageRollup(execs, cycles)(context.Background(), "org1")
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}
	got := build["shop"]
	if got.Tokens.InputTokens != 105 || got.Tokens.OutputTokens != 21 {
		t.Fatalf("build tokens = %d/%d, want 105/21", got.Tokens.InputTokens, got.Tokens.OutputTokens)
	}
	if got.CostUsd == nil || *got.CostUsd != 5 {
		t.Fatalf("build cost = %v, want 5", got.CostUsd)
	}
	// Validation has no execution contributor — it must pass through untouched
	// rather than be dropped or zeroed.
	if v := validation["shop"]; v.CostUsd == nil || *v.CostUsd != 1 || v.Tokens.InputTokens != 10 {
		t.Fatalf("validation = %+v, want the cycle figures intact", v)
	}
}

// A project in only one source keeps its own figures, and the model id survives
// only while contributors agree — StampedUsage.Add's semantics must not be
// bypassed by the merge.
func TestPhaseUsageRollup_DisjointProjectsAndModelDisagreement(t *testing.T) {
	cycles := fakeCycleUsage{usage: fakePhaseUsage{
		build: map[string]contracts.StampedUsage{
			"only-cycles": stamped(7, 3, "m1", usd(1)),
			"mixed":       stamped(7, 3, "m1", usd(1)),
		},
	}}
	execs := fakeExecUsage{usage: fakePhaseUsage{
		build: map[string]contracts.StampedUsage{
			"only-execs": stamped(4, 2, "m2", nil),
			"mixed":      stamped(1, 1, "m2", usd(1)),
		},
	}}

	build, _, err := PhaseUsageRollup(execs, cycles)(context.Background(), "org1")
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if len(build) != 3 {
		t.Fatalf("build projects = %d, want 3: %+v", len(build), build)
	}
	if u := build["only-cycles"]; u.Tokens.Model != "m1" || u.Tokens.InputTokens != 7 {
		t.Fatalf("cycle-only project = %+v", u)
	}
	// Unstamped stays unstamped: nil cost must not become 0.
	if u := build["only-execs"]; u.CostUsd != nil {
		t.Fatalf("exec-only project cost = %v, want nil (unpriced)", u.CostUsd)
	}
	if u := build["mixed"]; u.Tokens.Model != "" {
		t.Fatalf("mixed-model aggregate model = %q, want \"\"", u.Tokens.Model)
	}
}

// A failure in either source must surface, not silently under-report spend as a
// partial total the console would render as authoritative.
func TestPhaseUsageRollup_PropagatesEitherError(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name   string
		execs  fakeExecUsage
		cycles fakeCycleUsage
	}{
		{
			name:   "cycle source fails",
			cycles: fakeCycleUsage{usage: fakePhaseUsage{err: boom}},
		},
		{
			name:  "execution source fails",
			execs: fakeExecUsage{usage: fakePhaseUsage{err: boom}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			build, validation, err := PhaseUsageRollup(tc.execs, tc.cycles)(context.Background(), "org1")
			if !errors.Is(err, boom) {
				t.Fatalf("err = %v, want boom", err)
			}
			if build != nil || validation != nil {
				t.Fatalf("maps must be nil on error, got %v / %v", build, validation)
			}
		})
	}
}
