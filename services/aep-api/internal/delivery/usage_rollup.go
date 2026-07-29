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
	"fmt"

	"github.com/wso2/aep/aep-api/internal/contracts"
)

// PhaseUsageRollup returns delivery's whole answer to "what did this org's agent
// work cost, per project, per SDLC phase" (#291): the sum of both places
// delivery captures usage.
//
// There are two because delivery has two dispatch histories:
//
//   - CYCLES are where agent spend lives now. After the issue-driven flip every
//     token-burning dispatch is a cycle — coding, fix, conflict, validation.
//   - EXECUTIONS are the older per-issue funnel's rows. The only kind still
//     minted is KindProvision, which stands up OpenChoreo resources and runs no
//     model, so in practice this side contributes nothing today. It is summed
//     anyway rather than dropped: the column and its capture path are real, and a
//     rollup that silently ignored a populated table would under-report spend
//     rather than fail visibly.
//
// The fold lives HERE, not in the composition root and not in the usage service,
// because "which dispatch records count, and which phase each belongs to" is
// delivery's knowledge. Callers see one function with the phase-split shape the
// usage service already consumes.
func PhaseUsageRollup(execs ExecutionRepository, cycles RunCycleRepository) func(ctx context.Context, orgID string) (build, validation map[string]contracts.StampedUsage, err error) {
	return func(ctx context.Context, orgID string) (map[string]contracts.StampedUsage, map[string]contracts.StampedUsage, error) {
		cycleBuild, cycleValidation, err := cycles.SumUsageByProjectPhase(ctx, orgID)
		if err != nil {
			return nil, nil, fmt.Errorf("delivery: cycle usage rollup: %w", err)
		}
		execBuild, execValidation, err := execs.SumUsageByProjectPhase(ctx, orgID)
		if err != nil {
			return nil, nil, fmt.Errorf("delivery: execution usage rollup: %w", err)
		}
		return mergeStamped(cycleBuild, execBuild), mergeStamped(cycleValidation, execValidation), nil
	}
}

// mergeStamped folds b into a copy of a, per project. StampedUsage.Add carries
// the model-agreement and nil-cost semantics, so a project present in only one
// source keeps its own figures untouched.
func mergeStamped(a, b map[string]contracts.StampedUsage) map[string]contracts.StampedUsage {
	out := make(map[string]contracts.StampedUsage, len(a)+len(b))
	for slug, u := range a {
		out[slug] = u
	}
	for slug, u := range b {
		out[slug] = out[slug].Add(u)
	}
	return out
}
