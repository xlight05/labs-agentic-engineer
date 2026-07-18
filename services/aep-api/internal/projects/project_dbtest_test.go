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

// DBTEST tier for the tasks-github-native project semantics: DeleteProject
// purges the project's platform-owned executions rows (no component_tasks table
// exists any more), and the purge is org-scoped so a project slug reused across
// orgs cannot cross-delete. Runs over a real Postgres (dbtest.New; self-skips
// under -short).
package projects_test

import (
	"context"
	"testing"

	"github.com/wso2/aep/aep-api/internal/delivery"

	ocmocks "github.com/wso2/aep/aep-api/internal/clients/openchoreo/mocks"
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/platform/dbtest"
	"github.com/wso2/aep/aep-api/internal/projects"
)

func TestDeleteProject_PurgesExecutions_OrgScoped_DB(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := context.Background()
	execRepo := delivery.NewExecutionRepository(db)

	seed := func(org, proj string, issue int) {
		if _, _, err := execRepo.TryAdmit(ctx, &delivery.Execution{
			OrgID: org, ProjectID: proj, Repo: org + "/r", IssueNumber: issue, Kind: string(taskmeta.KindCoding),
		}); err != nil {
			t.Fatalf("seed execution (%s/%s#%d): %v", org, proj, issue, err)
		}
	}
	// Same project slug "widgets" across two orgs — the purge must be org-scoped.
	seed("acme", "widgets", 1)
	seed("acme", "widgets", 2)
	seed("evil", "widgets", 1)

	oc := &ocmocks.ProjectClientMock{
		DeleteProjectFunc: func(context.Context, string, string) error { return nil },
	}
	// repoSvc + others nil (skipped); the real executions repo does the purge.
	svc := projects.NewProjectService(oc, nil, nil, nil, execRepo)

	if err := svc.DeleteProject(ctx, "acme", "widgets"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	if rows, _ := execRepo.ListByIssue(ctx, "acme/r", 1); len(rows) != 0 {
		t.Errorf("acme executions must be purged, got %d rows", len(rows))
	}
	if rows, _ := execRepo.ListByIssue(ctx, "acme/r", 2); len(rows) != 0 {
		t.Errorf("acme executions must be purged, got %d rows", len(rows))
	}
	// The same-slug project in another org is untouched.
	if rows, _ := execRepo.ListByIssue(ctx, "evil/r", 1); len(rows) != 1 {
		t.Errorf("another org's same-slug project must survive, got %d rows", len(rows))
	}
}
