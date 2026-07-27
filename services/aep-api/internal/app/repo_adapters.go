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

	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// repoFullNameLookup resolves a project's "owner/name" from the repo row. It is
// the shared RepoLookup adapter behind the build service, the task-log stream
// and the provisioning repo namer — one resolution rule, wired once.
type repoFullNameLookup struct {
	repos sourcecontrol.RepoRepository
}

func (l repoFullNameLookup) RepoFullName(ctx context.Context, orgID, projectID string) (string, error) {
	row, err := l.repos.GetByOrgAndProjectID(ctx, orgID, projectID)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", sourcecontrol.ErrRepoNotFound
	}
	owner, name, err := sourcecontrol.ParseOwnerRepo(row.RepoURL)
	if err != nil {
		return "", err
	}
	return owner + "/" + name, nil
}
