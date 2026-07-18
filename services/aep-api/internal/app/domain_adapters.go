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

	"github.com/wso2/aep/aep-api/internal/platform/gitfs/reaper"
	"github.com/wso2/aep/aep-api/models"
)

// The opsExecutionBridge that used to live here (satisfying ops.ExecutionReader
// from the legacy execution repository — the consumer-before-provider bridge P1
// existed to prove) was RETIRED in P6: delivery now owns the Execution store and
// supplies ops.ExecutionReader directly via execution.NewOpsExecutionReader.
// Nothing in internal/ops changed — the property the bridge was proving.

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
