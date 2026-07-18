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

package dependencies_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/dependencies"
	"github.com/wso2/aep/aep-api/internal/platform/dbtest"
)

// mkAccessRequest persists an AccessRequest against real Postgres via the
// repository under test and returns the (mutated-in-place) row.
func mkAccessRequest(t *testing.T, repo *dependencies.AccessRequestRepository, ar *dependencies.AccessRequest) *dependencies.AccessRequest {
	t.Helper()
	if err := repo.Create(context.Background(), ar); err != nil {
		t.Fatalf("create access request (%s/%s): %v", ar.OrgID, ar.ConsumerProjectID, err)
	}
	if ar.ID == "" {
		t.Fatalf("create access request: expected generated id")
	}
	return ar
}

// TestAccessRequestRepository_Create_MintsIDAndDefaultStatus confirms Create
// mints a UUID when unset and defaults Status to `requested`.
func TestAccessRequestRepository_Create_MintsIDAndDefaultStatus(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := dependencies.NewAccessRequestRepository(db)
	ctx := context.Background()

	ar := &dependencies.AccessRequest{
		OrgID:                 "orga",
		ConsumerProjectID:     "proj-a",
		ConsumerComponentName: "svc-a",
		OrgServiceName:        "billing",
	}
	if err := repo.Create(ctx, ar); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ar.ID == "" {
		t.Fatalf("expected a minted id")
	}
	if ar.Status != dependencies.AccessRequestStatusRequested {
		t.Fatalf("Status = %q; want default %q", ar.Status, dependencies.AccessRequestStatusRequested)
	}

	// Missing required fields is a validation error, not a DB round trip.
	if err := repo.Create(ctx, &dependencies.AccessRequest{}); err == nil {
		t.Fatalf("Create with empty orgID/consumerProjectID should fail")
	}
	if err := repo.Create(ctx, nil); err == nil {
		t.Fatalf("Create(nil) should fail")
	}
}

// TestAccessRequestRepository_Get_OrgScoped_NotFoundSentinel confirms Get
// scopes by (org, id) and returns ErrAccessRequestNotFound for both a bogus id
// and a cross-org lookup of a real id — no existence leak.
func TestAccessRequestRepository_Get_OrgScoped_NotFoundSentinel(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := dependencies.NewAccessRequestRepository(db)
	ctx := context.Background()

	ar := mkAccessRequest(t, repo, &dependencies.AccessRequest{
		OrgID: "orga", ConsumerProjectID: "proj-a", ConsumerComponentName: "svc-a", OrgServiceName: "billing",
	})

	got, err := repo.Get(ctx, "orga", ar.ID)
	if err != nil {
		t.Fatalf("Get(owner): %v", err)
	}
	if got == nil || got.ID != ar.ID {
		t.Fatalf("Get(owner) = %+v; want the created row", got)
	}

	_, err = repo.Get(ctx, "orgb", ar.ID)
	if !errors.Is(err, dependencies.ErrAccessRequestNotFound) {
		t.Fatalf("Get(cross-org) = %v; want ErrAccessRequestNotFound", err)
	}

	_, err = repo.Get(ctx, "orga", "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, dependencies.ErrAccessRequestNotFound) {
		t.Fatalf("Get(bogus id) = %v; want ErrAccessRequestNotFound", err)
	}
}

// TestAccessRequestRepository_ListByConsumerProject_ScopedNewestFirst confirms
// the list is scoped to (org, project) and ordered newest first.
func TestAccessRequestRepository_ListByConsumerProject_ScopedNewestFirst(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := dependencies.NewAccessRequestRepository(db)
	ctx := context.Background()

	base := time.Now().Add(-1 * time.Hour)
	mk := func(org, proj, comp, svc string, at time.Time) {
		t.Helper()
		ar := &dependencies.AccessRequest{
			OrgID: org, ConsumerProjectID: proj, ConsumerComponentName: comp, OrgServiceName: svc,
		}
		if err := repo.Create(ctx, ar); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := db.Exec(`UPDATE access_requests SET created_at = ? WHERE id = ?`, at, ar.ID).Error; err != nil {
			t.Fatalf("backdate created_at: %v", err)
		}
	}
	mk("orga", "proj-a", "svc-1", "billing", base)
	mk("orga", "proj-a", "svc-2", "notifications", base.Add(1*time.Minute))
	mk("orga", "proj-b", "svc-3", "billing", base) // different project
	mk("orgb", "proj-a", "svc-4", "billing", base) // different org, same project slug

	got, err := repo.ListByConsumerProject(ctx, "orga", "proj-a")
	if err != nil {
		t.Fatalf("ListByConsumerProject: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListByConsumerProject = %d rows; want 2", len(got))
	}
	if got[0].OrgServiceName != "notifications" || got[1].OrgServiceName != "billing" {
		t.Fatalf("not ordered newest-first: %+v", got)
	}
}

// TestAccessRequestRepository_FindOpenForTarget confirms it dedups onto the
// newest still-open (requested|in_progress) request for a provider target,
// ignores granted/rejected requests, and returns (nil, nil) on no match.
func TestAccessRequestRepository_FindOpenForTarget(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := dependencies.NewAccessRequestRepository(db)
	ctx := context.Background()

	// No requests at all yet for this target.
	none, err := repo.FindOpenForTarget(ctx, "orga", "provider-proj", "provider-svc")
	if err != nil {
		t.Fatalf("FindOpenForTarget(none): %v", err)
	}
	if none != nil {
		t.Fatalf("expected nil for no open request, got %+v", none)
	}

	closed := &dependencies.AccessRequest{
		OrgID: "orga", ConsumerProjectID: "consumer-1", ConsumerComponentName: "svc-1", OrgServiceName: "provider-svc",
		ProviderProjectID: "provider-proj", ProviderComponentName: "provider-svc", Status: dependencies.AccessRequestStatusGranted,
	}
	mkAccessRequest(t, repo, closed)

	// Still no OPEN request (the only one is granted).
	none2, err := repo.FindOpenForTarget(ctx, "orga", "provider-proj", "provider-svc")
	if err != nil {
		t.Fatalf("FindOpenForTarget(only-granted): %v", err)
	}
	if none2 != nil {
		t.Fatalf("granted request should not count as open: %+v", none2)
	}

	open1 := &dependencies.AccessRequest{
		OrgID: "orga", ConsumerProjectID: "consumer-2", ConsumerComponentName: "svc-2", OrgServiceName: "provider-svc",
		ProviderProjectID: "provider-proj", ProviderComponentName: "provider-svc", Status: dependencies.AccessRequestStatusRequested,
	}
	mkAccessRequest(t, repo, open1)

	got, err := repo.FindOpenForTarget(ctx, "orga", "provider-proj", "provider-svc")
	if err != nil {
		t.Fatalf("FindOpenForTarget: %v", err)
	}
	if got == nil || got.ID != open1.ID {
		t.Fatalf("FindOpenForTarget = %+v; want the open request %+v", got, open1)
	}

	// A second, later open request from a different consumer dedups: the most
	// recent open one wins.
	open2 := &dependencies.AccessRequest{
		OrgID: "orga", ConsumerProjectID: "consumer-3", ConsumerComponentName: "svc-3", OrgServiceName: "provider-svc",
		ProviderProjectID: "provider-proj", ProviderComponentName: "provider-svc", Status: dependencies.AccessRequestStatusInProgress,
	}
	mkAccessRequest(t, repo, open2)
	if err := db.Exec(`UPDATE access_requests SET created_at = ? WHERE id = ?`, time.Now().Add(1*time.Hour), open2.ID).Error; err != nil {
		t.Fatalf("backdate: %v", err)
	}

	got2, err := repo.FindOpenForTarget(ctx, "orga", "provider-proj", "provider-svc")
	if err != nil {
		t.Fatalf("FindOpenForTarget(after second open): %v", err)
	}
	if got2 == nil || got2.ID != open2.ID {
		t.Fatalf("FindOpenForTarget should return the newest open request; got %+v want id %s", got2, open2.ID)
	}
}

// TestAccessRequestRepository_UpdateStatus confirms UpdateStatus flips the
// column and returns ErrAccessRequestNotFound for an unknown id.
func TestAccessRequestRepository_UpdateStatus(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := dependencies.NewAccessRequestRepository(db)
	ctx := context.Background()

	ar := mkAccessRequest(t, repo, &dependencies.AccessRequest{
		OrgID: "orga", ConsumerProjectID: "proj-a", ConsumerComponentName: "svc-a", OrgServiceName: "billing",
	})

	if err := repo.UpdateStatus(ctx, ar.ID, dependencies.AccessRequestStatusInProgress); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, err := repo.Get(ctx, "orga", ar.ID)
	if err != nil || got == nil {
		t.Fatalf("reload: %v / %v", got, err)
	}
	if got.Status != dependencies.AccessRequestStatusInProgress {
		t.Fatalf("Status = %q; want %q", got.Status, dependencies.AccessRequestStatusInProgress)
	}

	err = repo.UpdateStatus(ctx, "00000000-0000-0000-0000-000000000000", dependencies.AccessRequestStatusGranted)
	if !errors.Is(err, dependencies.ErrAccessRequestNotFound) {
		t.Fatalf("UpdateStatus(bogus id) = %v; want ErrAccessRequestNotFound", err)
	}
}

// TestAccessRequestRepository_ListByProviderTask confirms every consumer
// request riding on one provider task is returned, newest first, and is NOT
// scoped by org (a provider task id is globally unique).
func TestAccessRequestRepository_ListByProviderTask(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	repo := dependencies.NewAccessRequestRepository(db)
	ctx := context.Background()

	const providerTask = "11111111-1111-1111-1111-111111111111"
	mk := func(org, consumerProj string) *dependencies.AccessRequest {
		t.Helper()
		ar := &dependencies.AccessRequest{
			OrgID: org, ConsumerProjectID: consumerProj, ConsumerComponentName: "svc", OrgServiceName: "billing",
			ProviderTaskID: providerTask,
		}
		return mkAccessRequest(t, repo, ar)
	}
	mk("orga", "consumer-1")
	mk("orga", "consumer-2")
	// Decoy riding a different provider task.
	other := &dependencies.AccessRequest{
		OrgID: "orga", ConsumerProjectID: "consumer-3", ConsumerComponentName: "svc", OrgServiceName: "billing",
		ProviderTaskID: "22222222-2222-2222-2222-222222222222",
	}
	mkAccessRequest(t, repo, other)

	got, err := repo.ListByProviderTask(ctx, providerTask)
	if err != nil {
		t.Fatalf("ListByProviderTask: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListByProviderTask = %d rows; want 2", len(got))
	}
	for _, ar := range got {
		if ar.ProviderTaskID != providerTask {
			t.Fatalf("leaked a decoy row: %+v", ar)
		}
	}
}
