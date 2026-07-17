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

// DBTEST tier: the OrgDisconnectService cascade under the tasks-github-native
// model, exercised with a REAL CredentialService (honest tier-fit — the service
// takes the concrete *CredentialService, so faking it would prove nothing; ADR-
// 0003 Pilot B) over a real Postgres. The old per-task abandon cascade (Phase
// B/C over component_tasks) is GONE: Tasks are GitHub issues, so disconnect only
// severs the credential (Phase A confirm → Phase D finalize) and leaves the
// platform-owned executions rows untouched — severing the credential makes the
// org's issues inert to the webhook router, which is the disconnect effect.
package organization

import (
	"context"
	"errors"
	"testing"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/platform/dbtest"
	"github.com/wso2/aep/aep-api/models"
	"github.com/wso2/aep/aep-api/repositories"
)

func TestOrgDisconnect_SeversCredential_LeavesExecutions_DB(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := context.Background()

	gh := patHappyGitHub(t, "ada", "Ada", "ada@x.io")
	credSvc, _ := newCredSvcDB(t, db, gh)
	if _, err := credSvc.Connect(ctx, "acme", ConnectRequest{Kind: "user-pat", PAT: "ghp", GitHubLogin: "ada"}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Seed platform-owned executions rows for the org's Tasks. These must SURVIVE
	// the disconnect (the issues go inert; the rows are not purged — that is the
	// project-delete path, not disconnect).
	execRepo := repositories.NewExecutionRepository(db)
	for _, issue := range []int{7, 8} {
		if _, _, err := execRepo.TryAdmit(ctx, &models.Execution{
			OrgID: "acme", ProjectID: "web", Repo: "acme/web", IssueNumber: issue,
			Kind: string(taskmeta.KindCoding),
		}); err != nil {
			t.Fatalf("seed execution #%d: %v", issue, err)
		}
	}

	// issueSvc is nil: the disconnect cascade no longer touches issues (the task
	// abandon cascade that used it is gone).
	svc := NewOrgDisconnectService(credSvc, nil)
	if err := svc.Disconnect(ctx, "acme", "manual.disconnect", false); err != nil {
		t.Fatalf("disconnect: %v", err)
	}

	// Phase D: the credential row is finalized to disconnected.
	if row := getRow(t, db, "acme"); row.Status != "disconnected" {
		t.Fatalf("credential row status = %q, want disconnected", row.Status)
	}
	// The executions rows are untouched — disconnect severs credentials, it does
	// not purge platform state.
	for _, issue := range []int{7, 8} {
		if rows, _ := execRepo.ListByIssue(ctx, "acme/web", issue); len(rows) != 1 {
			t.Errorf("execution #%d must survive disconnect, got %d rows", issue, len(rows))
		}
	}
}

func TestOrgDisconnect_UnknownOrg_ReturnsNotFound_DB(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := context.Background()

	gh := patHappyGitHub(t, "ada", "Ada", "ada@x.io")
	credSvc, _ := newCredSvcDB(t, db, gh)
	svc := NewOrgDisconnectService(credSvc, nil)

	// Phase A existence check: no credential row → ErrOrgNotFound (the controller
	// maps it to an idempotent 200).
	if err := svc.Disconnect(ctx, "ghost", "manual.disconnect", false); !errors.Is(err, ErrOrgNotFound) {
		t.Fatalf("disconnect unknown org: err = %v, want ErrOrgNotFound", err)
	}
}

func TestOrgDisconnect_AlreadyDisconnected_NoOp_DB(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := context.Background()

	gh := patHappyGitHub(t, "ada", "Ada", "ada@x.io")
	credSvc, _ := newCredSvcDB(t, db, gh)
	if _, err := credSvc.Connect(ctx, "acme", ConnectRequest{Kind: "user-pat", PAT: "ghp", GitHubLogin: "ada"}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	svc := NewOrgDisconnectService(credSvc, nil)

	if err := svc.Disconnect(ctx, "acme", "manual.disconnect", false); err != nil {
		t.Fatalf("first disconnect: %v", err)
	}
	// A second disconnect on an already-finalized row is a clean no-op.
	if err := svc.Disconnect(ctx, "acme", "manual.disconnect", false); err != nil {
		t.Fatalf("second disconnect must be idempotent, got %v", err)
	}
	if row := getRow(t, db, "acme"); row.Status != "disconnected" {
		t.Fatalf("credential row status = %q, want disconnected", row.Status)
	}
}
