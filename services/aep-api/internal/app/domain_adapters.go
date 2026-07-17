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

package app

import (
	"context"

	"github.com/wso2/aep/aep-api/internal/ops"
	"github.com/wso2/aep/aep-api/internal/platform/gitfs/reaper"
	"github.com/wso2/aep/aep-api/models"
)

// opsExecutionBridge satisfies ops.ExecutionReader from the legacy execution
// repository — the consumer-before-provider bridge P1 exists to prove.
//
// aep:migration-shim retires=P6 reason=delivery lands the Execution store and implements ops.ExecutionReader directly
//
// ops consumes the Execution store, which the delivery domain will not own until
// P6. Rather than block a leaf domain behind the largest one, ops declares the
// port it needs in its OWN vocabulary (ops.ExecutionFact) and the composition
// root adapts whatever provides it today. That is the point of a consumer-side
// port: the direction of the dependency is chosen by the consumer, so the
// provider can arrive late without ops changing.
//
// When delivery lands, it implements ops.ExecutionReader and this file is
// deleted. Nothing in internal/ops changes — which is the property being tested.
type opsExecutionBridge struct {
	// execs is the legacy repositories.ExecutionRepository, narrowed to the one
	// method ops needs (declared inline so the bridge names no more of the
	// legacy surface than it uses).
	execs interface {
		LatestPerKindScoped(ctx context.Context, orgID, repo string, issueNumber int) (map[string]*models.Execution, error)
	}
}

// LatestExecutionPerKind maps the legacy Execution rows onto ops' vocabulary.
// The mapping is the whole bridge: ops never sees models.Execution, so the
// global models package can dissolve without touching the domain.
func (b opsExecutionBridge) LatestExecutionPerKind(ctx context.Context, orgID, repo string, issueNumber int) (map[string]ops.ExecutionFact, error) {
	if b.execs == nil {
		return nil, nil
	}
	rows, err := b.execs.LatestPerKindScoped(ctx, orgID, repo, issueNumber)
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

// reaperRepoLister projects git_repositories rows onto the reaper's coordinate
// port, so the kernel reaper never names the GitRepository entity.
//
// This is a permanent composition-root adapter, not a migration bridge: the
// reaper is a kernel package and will always take its own vocabulary, while the
// repository will always speak rows. It lives beside the ops bridge because both
// are the same pattern — the root translating between a consumer's port and a
// provider's model.
type reaperRepoLister struct {
	repos interface {
		ListAll(ctx context.Context) ([]models.GitRepository, error)
	}
}

func (l reaperRepoLister) ListAll(ctx context.Context) ([]reaper.RepoCoordinate, error) {
	rows, err := l.repos.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]reaper.RepoCoordinate, len(rows))
	for i := range rows {
		out[i] = reaper.RepoCoordinate{
			OrgID:         rows[i].OrgID,
			ProjectID:     rows[i].ProjectID,
			WorkspaceSlug: rows[i].WorkspaceSlug(),
		}
	}
	return out, nil
}
