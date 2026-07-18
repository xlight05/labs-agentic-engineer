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

package execution

import (
	"context"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/ops"
)

// OpsExecutionReader implements ops.ExecutionReader over the executions store.
// It is the P6 retirement of the app-root opsExecutionBridge: ops declared the
// consumer-side port (ops.ExecutionReader, in ops.ExecutionFact vocabulary)
// BEFORE its provider existed, and the composition root adapted the legacy
// repository in the meantime (the "consumer-before-provider" bridge P1 exists to
// prove). Now that delivery owns the Execution store, the delivery domain IS the
// provider and supplies the port directly — nothing in internal/ops changed, and
// the root bridge is deleted. The mapping stays the whole job: ops never sees
// delivery.Execution, so the global models package can still dissolve (P9) without
// touching either domain.
type OpsExecutionReader struct{ execs opsExecutionReads }

// opsExecutionReads is the one org-scoped read the ops correlation needs,
// narrowed so this reader names no more of the store than it uses. Satisfied by
// delivery.ExecutionRepository.
type opsExecutionReads interface {
	LatestPerKindScoped(ctx context.Context, orgID, repo string, issueNumber int) (map[string]*delivery.Execution, error)
}

// NewOpsExecutionReader builds the ops.ExecutionReader over the executions store.
func NewOpsExecutionReader(execs opsExecutionReads) *OpsExecutionReader {
	return &OpsExecutionReader{execs: execs}
}

var _ ops.ExecutionReader = (*OpsExecutionReader)(nil)

// LatestExecutionPerKind projects the latest Execution row per kind onto ops'
// ExecutionFact vocabulary. A nil reader/store yields no facts (the honest
// degradation ops documents for a nil ExecutionReader).
func (r *OpsExecutionReader) LatestExecutionPerKind(ctx context.Context, orgID, repo string, issueNumber int) (map[string]ops.ExecutionFact, error) {
	if r == nil || r.execs == nil {
		return nil, nil
	}
	rows, err := r.execs.LatestPerKindScoped(ctx, orgID, repo, issueNumber)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ops.ExecutionFact, len(rows))
	for kind, e := range rows {
		if e == nil {
			continue
		}
		out[kind] = ops.ExecutionFact{Kind: e.Kind, Status: e.Status, EndedAt: e.EndedAt}
	}
	return out, nil
}
