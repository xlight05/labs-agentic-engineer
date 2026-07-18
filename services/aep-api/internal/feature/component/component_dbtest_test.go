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

package component

// DBTEST tier (skips under -short; `make test-db` runs it): the SQL-shaped
// behaviors of the component feature driven against a pristine per-test Postgres
// (dbtest.New).
//
//  1. The REAL repositories.ConfigRepository — get/update round-trip + the
//     (org_id, project_name, component_name) scoping, with MANY decoy rows so a
//     broken WHERE clause returns the wrong row rather than flaky-passing under
//     random UUID ordering.
//  2. The TraitSyncWatcher's DISTINCT (org_id, project_id, component_name) sweep
//     over the component_tasks table — dedup, the empty-field exclusion, and the
//     k8s-name transform — observed through the OC client the reconcile fans out to.

import (
	"context"
	"testing"

	"github.com/wso2/aep/aep-api/internal/spec"
	"github.com/wso2/aep/aep-api/internal/platform/dbtest"
	"github.com/wso2/aep/aep-api/models"
	"github.com/wso2/aep/aep-api/repositories"
)

// --- ConfigRepository round-trip + scoping ------------------------------------

func seedConfig(t *testing.T, repo repositories.ConfigRepository, org, proj, comp, k, v string) {
	t.Helper()
	if err := repo.Upsert(context.Background(), &models.ComponentConfig{
		OrgID: org, ProjectName: proj, ComponentName: comp,
		EnvVars: models.EnvVarSlice{{Key: k, Value: v}},
	}); err != nil {
		t.Fatalf("seed config (%s/%s/%s): %v", org, proj, comp, err)
	}
}

func TestConfigRepository_RoundTripAndScoping_DB(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t)
	ctx := context.Background()
	repo := repositories.NewConfigRepository(db)

	// The target row, surrounded by MANY decoys that differ in exactly one of the
	// three scope columns (plus a same-project/other-component and an other-org
	// twin). A WHERE that drops any of the three columns would return a decoy.
	seedConfig(t, repo, "acme", "web", "svc-a", "TARGET", "1")
	seedConfig(t, repo, "acme", "web", "svc-b", "DECOY_COMPONENT", "1")
	seedConfig(t, repo, "acme", "api", "svc-a", "DECOY_PROJECT", "1")
	seedConfig(t, repo, "intruder", "web", "svc-a", "CROSS_ORG", "1")
	seedConfig(t, repo, "acme", "web", "svc-c", "DECOY_2", "1")
	seedConfig(t, repo, "other", "api", "svc-z", "DECOY_3", "1")
	seedConfig(t, repo, "acme", "mobile", "svc-a", "DECOY_PROJECT_2", "1")

	// Exact-tuple read returns exactly the target's env, not any decoy's.
	got, err := repo.GetByComponent(ctx, "acme", "web", "svc-a")
	if err != nil || got == nil {
		t.Fatalf("get target: got=%+v err=%v", got, err)
	}
	if len(got.EnvVars) != 1 || got.EnvVars[0].Key != "TARGET" {
		t.Fatalf("scope leak: got env %+v, want [TARGET]", got.EnvVars)
	}
	targetID := got.ID

	// Cross-org twin (same project + component, different org) is a DISTINCT row.
	crossOrg, err := repo.GetByComponent(ctx, "intruder", "web", "svc-a")
	if err != nil || crossOrg == nil || crossOrg.EnvVars[0].Key != "CROSS_ORG" {
		t.Fatalf("cross-org row must be isolated, got %+v (err %v)", crossOrg, err)
	}
	if crossOrg.ID == targetID {
		t.Fatalf("cross-org row shares the target's ID %q — not isolated", targetID)
	}

	// A missing tuple is (nil, nil), never a decoy.
	missing, err := repo.GetByComponent(ctx, "acme", "web", "does-not-exist")
	if err != nil || missing != nil {
		t.Fatalf("missing tuple must be (nil,nil), got (%+v,%v)", missing, err)
	}

	// Upsert on the existing tuple UPDATES in place (same ID) and replaces env.
	if err := repo.Upsert(ctx, &models.ComponentConfig{
		OrgID: "acme", ProjectName: "web", ComponentName: "svc-a",
		EnvVars: models.EnvVarSlice{{Key: "TARGET", Value: "2"}, {Key: "EXTRA", Value: "3"}},
	}); err != nil {
		t.Fatalf("upsert-update: %v", err)
	}
	updated, err := repo.GetByComponent(ctx, "acme", "web", "svc-a")
	if err != nil || updated == nil {
		t.Fatalf("re-read after update: %v", err)
	}
	if updated.ID != targetID {
		t.Fatalf("update must keep the row ID stable: was %q now %q", targetID, updated.ID)
	}
	if len(updated.EnvVars) != 2 || updated.EnvVars[0].Value != "2" {
		t.Fatalf("update must replace env vars: got %+v", updated.EnvVars)
	}

	// The update must not have touched the cross-org twin.
	crossOrgAfter, _ := repo.GetByComponent(ctx, "intruder", "web", "svc-a")
	if crossOrgAfter == nil || crossOrgAfter.EnvVars[0].Key != "CROSS_ORG" {
		t.Fatalf("cross-org row was mutated by a same-slug update: %+v", crossOrgAfter)
	}

	// Upsert on a brand-new tuple INSERTS a fresh row.
	if err := repo.Upsert(ctx, &models.ComponentConfig{
		OrgID: "acme", ProjectName: "web", ComponentName: "svc-new",
		EnvVars: models.EnvVarSlice{{Key: "NEW", Value: "1"}},
	}); err != nil {
		t.Fatalf("upsert-insert: %v", err)
	}
	inserted, err := repo.GetByComponent(ctx, "acme", "web", "svc-new")
	if err != nil || inserted == nil || inserted.ID == targetID {
		t.Fatalf("insert must create a new row, got %+v (err %v)", inserted, err)
	}
}

// --- TraitSyncWatcher DISTINCT tuple sweep over component_tasks ----------------

// multiComponentDesign returns a design tree containing one service component
// dir per supplied k8s name, so SyncComponentTraits finds a match for every
// tuple the sweep enumerates (regardless of which project it reads).
func multiComponentDesign(names ...string) map[string]string {
	files := map[string]string{spec.DesignRootFile: "# Overview\n"}
	for _, n := range names {
		files["components/"+n+"/design.json"] = "{\n  \"name\": \"" + n +
			"\",\n  \"type\": \"service\",\n  \"description\": \"body\",\n  \"dependencies\": []\n}\n"
	}
	return files
}
