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

package spec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wso2/aep/aep-api/models"
)

// --- deriveEndUserAuth (pure function) ---------------------------------------

// thunderDep builds a platform-resource dependency of the SAMPLE resourceType
// "thunder-app". The name is arbitrary sample data — derivation keys on the CRT
// role MARKER (authRole), never on the name (see
// TestDeriveEndUserAuth_UnlabeledTypeUntouchedEvenIfNamedThunderApp).
func thunderDep(name string) models.Dependency {
	return models.Dependency{Kind: models.DependencyKindPlatformResource, Name: name, ResourceType: "thunder-app"}
}

// authRole returns a marker map flagging resourceType as carrying the
// end-user-auth role — the labeled sample type the derivation stamps on.
func authRole(resourceType string) map[string]CRTMarkers {
	return map[string]CRTMarkers{resourceType: {EndUserAuth: true}}
}

// fakeMarkerCatalog is the resourceMarkerCatalog port double for SaveAndProceed
// integration tests: it records whether it was consulted and serves a canned
// marker map (or an error to exercise the fail-closed save gate).
type fakeMarkerCatalog struct {
	markers map[string]CRTMarkers
	err     error
	calls   int
}

func (f *fakeMarkerCatalog) MarkersByName(context.Context) (map[string]CRTMarkers, error) {
	f.calls++
	return f.markers, f.err
}

// (a) service + thunder-app dep + nil ExposesAPI → ExposesAPI created with
// Auth end-user-required.
func TestDeriveEndUserAuth_StampsNilExposesAPI(t *testing.T) {
	t.Parallel()
	comps := []models.DesignComponent{{
		Name:          "orders-api",
		ComponentType: "service",
		Dependencies:  []models.Dependency{thunderDep("user-auth")},
	}}

	if err := deriveEndUserAuth(comps, authRole("thunder-app")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if comps[0].ExposesAPI == nil || comps[0].ExposesAPI.Auth != authEndUserRequired {
		t.Fatalf("ExposesAPI = %+v, want Auth=%q", comps[0].ExposesAPI, authEndUserRequired)
	}
}

// (b) service + dep + existing end-user-required → unchanged, no error (and
// sibling ExposesAPI fields survive untouched).
func TestDeriveEndUserAuth_ExistingEndUserRequiredUnchanged(t *testing.T) {
	t.Parallel()
	comps := []models.DesignComponent{{
		Name:          "orders-api",
		ComponentType: "service",
		Dependencies:  []models.Dependency{thunderDep("user-auth")},
		ExposesAPI:    &models.ExposesAPI{Auth: authEndUserRequired, Managed: true, UserContext: "X-User-Id"},
	}}

	if err := deriveEndUserAuth(comps, authRole("thunder-app")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := comps[0].ExposesAPI
	if got.Auth != authEndUserRequired || !got.Managed || got.UserContext != "X-User-Id" {
		t.Fatalf("ExposesAPI mutated unexpectedly: %+v", got)
	}
}

// (c) service + dep + service-required → error naming both the dependency and
// the conflicting value; nothing is mutated.
func TestDeriveEndUserAuth_ServiceRequiredConflictErrors(t *testing.T) {
	t.Parallel()
	comps := []models.DesignComponent{{
		Name:          "orders-api",
		ComponentType: "service",
		Dependencies:  []models.Dependency{thunderDep("user-auth")},
		ExposesAPI:    &models.ExposesAPI{Auth: authServiceRequired},
	}}

	err := deriveEndUserAuth(comps, authRole("thunder-app"))
	if err == nil {
		t.Fatal("want a conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "user-auth") {
		t.Fatalf("error must name the dependency %q: %v", "user-auth", err)
	}
	if !strings.Contains(err.Error(), authServiceRequired) {
		t.Fatalf("error must name the conflicting value %q: %v", authServiceRequired, err)
	}
	if comps[0].ExposesAPI.Auth != authServiceRequired {
		t.Fatalf("ExposesAPI must be left unchanged on conflict, got %+v", comps[0].ExposesAPI)
	}
}

// (d) web-app + dep → no ExposesAPI mutation (SPAs aren't gateway-exposed APIs).
func TestDeriveEndUserAuth_WebAppUntouched(t *testing.T) {
	t.Parallel()
	comps := []models.DesignComponent{{
		Name:          "storefront-web",
		ComponentType: "web-application",
		Dependencies:  []models.Dependency{thunderDep("user-auth")},
	}}

	if err := deriveEndUserAuth(comps, authRole("thunder-app")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if comps[0].ExposesAPI != nil {
		t.Fatalf("web-app ExposesAPI must stay nil, got %+v", comps[0].ExposesAPI)
	}
}

// (e) service without the dependency → untouched.
func TestDeriveEndUserAuth_DepLessServiceUntouched(t *testing.T) {
	t.Parallel()
	comps := []models.DesignComponent{{
		Name:          "orders-api",
		ComponentType: "service",
		Dependencies:  []models.Dependency{{Kind: models.DependencyKindComponent, Name: "sibling"}},
	}}

	if err := deriveEndUserAuth(comps, authRole("thunder-app")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if comps[0].ExposesAPI != nil {
		t.Fatalf("dep-less service ExposesAPI must stay nil, got %+v", comps[0].ExposesAPI)
	}
}

// Extra: a platform-resource dependency of a DIFFERENT resourceType (e.g.
// postgres-cnpg) must not trigger derivation.
func TestDeriveEndUserAuth_OtherResourceTypeUntouched(t *testing.T) {
	t.Parallel()
	comps := []models.DesignComponent{{
		Name:          "orders-api",
		ComponentType: "service",
		Dependencies: []models.Dependency{
			{Kind: models.DependencyKindPlatformResource, Name: "orders-db", ResourceType: "postgres-cnpg"},
		},
	}}

	if err := deriveEndUserAuth(comps, authRole("thunder-app")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if comps[0].ExposesAPI != nil {
		t.Fatalf("non-thunder-app resource ExposesAPI must stay nil, got %+v", comps[0].ExposesAPI)
	}
}

// The NAME must mean nothing now: a platform-resource dep whose resourceType
// happens to be "thunder-app" but which carries NO end-user-auth role marker
// (empty catalog) is left completely untouched. This is the crux of the
// generalization — derivation keys on the CRT marker, never on the name.
func TestDeriveEndUserAuth_UnlabeledTypeUntouchedEvenIfNamedThunderApp(t *testing.T) {
	t.Parallel()
	comps := []models.DesignComponent{{
		Name:          "orders-api",
		ComponentType: "service",
		Dependencies:  []models.Dependency{thunderDep("user-auth")},
	}}

	// Empty marker map: "thunder-app" carries no role — nothing to derive.
	if err := deriveEndUserAuth(comps, map[string]CRTMarkers{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if comps[0].ExposesAPI != nil {
		t.Fatalf("unlabeled type must stay untouched regardless of its name, got %+v", comps[0].ExposesAPI)
	}
}

// A labeled type with a name OTHER than "thunder-app" stamps just the same —
// the marker, not the name, is the signal.
func TestDeriveEndUserAuth_StampsAnyLabeledType(t *testing.T) {
	t.Parallel()
	comps := []models.DesignComponent{{
		Name:          "orders-api",
		ComponentType: "service",
		Dependencies: []models.Dependency{
			{Kind: models.DependencyKindPlatformResource, Name: "user-auth", ResourceType: "custom-oidc"},
		},
	}}

	if err := deriveEndUserAuth(comps, authRole("custom-oidc")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if comps[0].ExposesAPI == nil || comps[0].ExposesAPI.Auth != authEndUserRequired {
		t.Fatalf("labeled type of any name must stamp: ExposesAPI = %+v", comps[0].ExposesAPI)
	}
}

// Multiple components: only the qualifying service is stamped, everything
// else (a sibling service without the dep, and a web-app WITH the dep) is
// left alone in the same pass.
func TestDeriveEndUserAuth_MixedComponentsOnlyQualifyingServiceStamped(t *testing.T) {
	t.Parallel()
	comps := []models.DesignComponent{
		{Name: "orders-api", ComponentType: "service", Dependencies: []models.Dependency{thunderDep("user-auth")}},
		{Name: "billing-api", ComponentType: "service"},
		{Name: "storefront-web", ComponentType: "web-application", Dependencies: []models.Dependency{thunderDep("user-auth")}},
	}

	if err := deriveEndUserAuth(comps, authRole("thunder-app")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if comps[0].ExposesAPI == nil || comps[0].ExposesAPI.Auth != authEndUserRequired {
		t.Fatalf("orders-api: ExposesAPI = %+v, want Auth=%q", comps[0].ExposesAPI, authEndUserRequired)
	}
	if comps[1].ExposesAPI != nil {
		t.Fatalf("billing-api (no dep): ExposesAPI must stay nil, got %+v", comps[1].ExposesAPI)
	}
	if comps[2].ExposesAPI != nil {
		t.Fatalf("storefront-web: ExposesAPI must stay nil, got %+v", comps[2].ExposesAPI)
	}
}

// designFilesWithDepsAndAuth is designFilesWithDeps (proceed_gate_test.go)
// plus an authored exposesAPI block, for the conflict-path test below.
func designFilesWithDepsAndAuth(depsJSON, exposesAPIJSON string) map[string]string {
	files := designFilesWithDeps(depsJSON)
	marker := `"dependencies": ` + depsJSON
	key := "components/consumer/design.json"
	files[key] = strings.Replace(files[key], marker, marker+",\n  \"exposesAPI\": "+exposesAPIJSON, 1)
	return files
}

// --- DeriveEndUserAuthAtHead (the thin POST /build pre-tag step, #164) -------

// The build path derives + persists exactly like SaveAndProceed does, but
// standalone (no tag-cut): a commit lands with the stamped auth.
func TestDeriveEndUserAuthAtHead_PersistsBeforeReturn(t *testing.T) {
	t.Parallel()
	deps := `[{"kind":"platform-resource","name":"user-auth","resourceType":"thunder-app"}]`
	fake := happySave(designFilesWithDeps(deps))
	svc := newService(fake)
	fc := &fakeCommitter{}
	svc.fileCommitter = fc
	svc.resourceCatalog = &fakeMarkerCatalog{markers: authRole("thunder-app")}

	if err := svc.DeriveEndUserAuthAtHead(context.Background(), "acme", "web"); err != nil {
		t.Fatalf("DeriveEndUserAuthAtHead: %v", err)
	}
	if fc.commits != 1 || len(fc.writes) != 1 {
		t.Fatalf("want one derive-persist commit, got commits=%d writes=%d", fc.commits, len(fc.writes))
	}
	if !strings.Contains(fc.writes[0].Content, `"auth": "end-user-required"`) {
		t.Fatalf("committed design.json missing derived auth: %s", fc.writes[0].Content)
	}
}

// A conflicting explicit service-required surfaces the 409 sentinel and commits
// nothing.
func TestDeriveEndUserAuthAtHead_ConflictReturnsSentinel(t *testing.T) {
	t.Parallel()
	deps := `[{"kind":"platform-resource","name":"user-auth","resourceType":"thunder-app"}]`
	files := designFilesWithDepsAndAuth(deps, `{"auth": "service-required"}`)
	svc := newService(happySave(files))
	fc := &fakeCommitter{}
	svc.fileCommitter = fc
	svc.resourceCatalog = &fakeMarkerCatalog{markers: authRole("thunder-app")}

	err := svc.DeriveEndUserAuthAtHead(context.Background(), "acme", "web")
	if !errors.Is(err, ErrEndUserAuthConflict) {
		t.Fatalf("want ErrEndUserAuthConflict, got %v", err)
	}
	if fc.commits != 0 {
		t.Fatalf("conflict must commit nothing, got %d", fc.commits)
	}
}

// Fail-closed: a platform-resource dep with an unreachable catalog surfaces the
// 503 sentinel.
func TestDeriveEndUserAuthAtHead_CatalogDownFailsClosed(t *testing.T) {
	t.Parallel()
	deps := `[{"kind":"platform-resource","name":"user-auth","resourceType":"thunder-app"}]`
	svc := newService(happySave(designFilesWithDeps(deps)))
	svc.fileCommitter = &fakeCommitter{}
	svc.resourceCatalog = &fakeMarkerCatalog{err: errors.New("OC unreachable")}

	if err := svc.DeriveEndUserAuthAtHead(context.Background(), "acme", "web"); !errors.Is(err, ErrResourceCatalogUnavailable) {
		t.Fatalf("want ErrResourceCatalogUnavailable, got %v", err)
	}
}

// No platform-resource dependency → a no-op that touches neither catalog nor
// committer.
func TestDeriveEndUserAuthAtHead_NoPlatformResourceDepNoOp(t *testing.T) {
	t.Parallel()
	svc := newService(happySave(validDesignFiles()))
	fc := &fakeCommitter{}
	svc.fileCommitter = fc
	cat := &fakeMarkerCatalog{err: errors.New("must not be called")}
	svc.resourceCatalog = cat

	if err := svc.DeriveEndUserAuthAtHead(context.Background(), "acme", "web"); err != nil {
		t.Fatalf("auth-free derive: unexpected error: %v", err)
	}
	if cat.calls != 0 || fc.commits != 0 {
		t.Fatalf("auth-free derive must not touch catalog/committer: calls=%d commits=%d", cat.calls, fc.commits)
	}
}
