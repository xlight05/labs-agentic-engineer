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
	"testing"

	"github.com/wso2/aep/aep-api/models"
)

// stubDesignListingService is a minimal in-package ArtifactService stub used
// only by TestReadDesign_ResolvesOrgServiceDependencies below. It embeds the
// (nil) ArtifactService interface so it satisfies the type without
// implementing every method — ReadDesign only calls ListDesignFiles, so no
// other method is ever invoked. (artifactstest.FakeArtifactService is not
// usable here: it imports this package, and this file lives in package
// artifacts — importing it back would be a cycle.)
type stubDesignListingService struct {
	ArtifactService
	files map[string]string
}

func (s stubDesignListingService) ListDesignFiles(_ context.Context, _, _ string) (map[string]string, error) {
	return s.files, nil
}

// fakeOrgServiceResolver is a static OrgServiceResolver: `visible` lists the
// org-service names published namespace-visible, `exists` lists every name
// that has any endpoint in the catalog (regardless of visibility). A name in
// `visible` is implicitly in `exists`. `visibleErr`/`existsErr` let a test
// force a resolver error for a specific name.
type fakeOrgServiceResolver struct {
	visible    map[string]bool
	exists     map[string]bool
	visibleErr map[string]error
	existsErr  map[string]error
}

func (f fakeOrgServiceResolver) IsNamespaceVisible(_ context.Context, _, name string) (bool, error) {
	if err, ok := f.visibleErr[name]; ok {
		return false, err
	}
	return f.visible[name], nil
}

func (f fakeOrgServiceResolver) ExistsAnyVisibility(_ context.Context, _, name string) (bool, error) {
	if err, ok := f.existsErr[name]; ok {
		return false, err
	}
	return f.exists[name] || f.visible[name], nil
}

// TestResolveOrgServices_Resolved asserts the 4-state model: a
// namespace-visible org-service produces status="resolved" with no reason.
func TestResolveOrgServices_Resolved(t *testing.T) {
	store := &ArtifactStore{}
	store.SetOrgServiceResolver(fakeOrgServiceResolver{
		visible: map[string]bool{"employee-api": true},
	})

	d := &DesignFile{Components: []models.DesignComponent{{
		Name: "web",
		Dependencies: []models.Dependency{
			{Kind: models.DependencyKindOrgService, Name: "employee-api"},
		},
	}}}

	store.resolveOrgServices(context.Background(), "org", d)

	dep := d.Components[0].Dependencies[0]
	if dep.Status != models.DependencyStatusResolved {
		t.Errorf("namespace-visible org-service: status = %q, want %q", dep.Status, models.DependencyStatusResolved)
	}
	if dep.Reason != "" {
		t.Errorf("namespace-visible org-service: reason = %q, want empty", dep.Reason)
	}
}

// TestResolveOrgServices_BlockedAccessRequired asserts the 4-state model: a
// project-only org-service (exists but NOT namespace-visible) must produce
// status="blocked" / reason="access-required".
func TestResolveOrgServices_BlockedAccessRequired(t *testing.T) {
	store := &ArtifactStore{}
	store.SetOrgServiceResolver(fakeOrgServiceResolver{
		visible: map[string]bool{},
		exists:  map[string]bool{"payroll-internal": true},
	})

	d := &DesignFile{Components: []models.DesignComponent{{
		Name: "consumer",
		Dependencies: []models.Dependency{
			{Kind: models.DependencyKindOrgService, Name: "payroll-internal"},
		},
	}}}

	store.resolveOrgServices(context.Background(), "org", d)

	dep := d.Components[0].Dependencies[0]
	if dep.Status != models.DependencyStatusBlocked {
		t.Errorf("project-only org-service: status = %q, want %q", dep.Status, models.DependencyStatusBlocked)
	}
	if dep.Reason != models.DependencyReasonAccessRequired {
		t.Errorf("project-only org-service: reason = %q, want %q", dep.Reason, models.DependencyReasonAccessRequired)
	}
}

// TestResolveOrgServices_AbsentNotFound asserts the 4-state model: an
// org-service absent from the catalog must produce status="unresolved" /
// reason="not-found".
func TestResolveOrgServices_AbsentNotFound(t *testing.T) {
	store := &ArtifactStore{}
	store.SetOrgServiceResolver(fakeOrgServiceResolver{
		visible: map[string]bool{},
		exists:  map[string]bool{},
	})

	d := &DesignFile{Components: []models.DesignComponent{{
		Name: "consumer",
		Dependencies: []models.Dependency{
			{Kind: models.DependencyKindOrgService, Name: "ghost-svc"},
		},
	}}}

	store.resolveOrgServices(context.Background(), "org", d)

	dep := d.Components[0].Dependencies[0]
	if dep.Status != models.DependencyStatusUnresolved {
		t.Errorf("absent org-service: status = %q, want %q", dep.Status, models.DependencyStatusUnresolved)
	}
	if dep.Reason != models.DependencyReasonNotFound {
		t.Errorf("absent org-service: reason = %q, want %q", dep.Reason, models.DependencyReasonNotFound)
	}
}

// TestResolveOrgServices_ReasonSplit asserts the full 4-state refinement in a
// single component: namespace-visible → resolved/"";
// exists-but-project-only → blocked/"access-required"; absent →
// unresolved/"not-found". Non-org-service dependency kinds must be left
// completely untouched.
func TestResolveOrgServices_ReasonSplit(t *testing.T) {
	store := &ArtifactStore{}
	store.SetOrgServiceResolver(fakeOrgServiceResolver{
		visible: map[string]bool{"employee-api": true},
		exists:  map[string]bool{"payroll-internal": true},
	})

	d := &DesignFile{Components: []models.DesignComponent{{
		Name: "web",
		Dependencies: []models.Dependency{
			{Kind: models.DependencyKindOrgService, Name: "employee-api"},     // namespace-visible
			{Kind: models.DependencyKindOrgService, Name: "payroll-internal"}, // exists, project-only
			{Kind: models.DependencyKindOrgService, Name: "ghost"},            // not in catalog
			{Kind: models.DependencyKindComponent, Name: "cart"},              // untouched — wrong kind
		},
	}}}

	store.resolveOrgServices(context.Background(), "org", d)

	deps := d.Components[0].Dependencies
	cases := []struct {
		name       string
		wantStatus string
		wantReason string
	}{
		{"employee-api", models.DependencyStatusResolved, ""},
		{"payroll-internal", models.DependencyStatusBlocked, models.DependencyReasonAccessRequired},
		{"ghost", models.DependencyStatusUnresolved, models.DependencyReasonNotFound},
		{"cart", "", ""},
	}
	for i, c := range cases {
		if deps[i].Name != c.name {
			t.Fatalf("dep[%d]: name = %q, want %q", i, deps[i].Name, c.name)
		}
		if deps[i].Status != c.wantStatus {
			t.Errorf("%s: status = %q, want %q", c.name, deps[i].Status, c.wantStatus)
		}
		if deps[i].Reason != c.wantReason {
			t.Errorf("%s: reason = %q, want %q", c.name, deps[i].Reason, c.wantReason)
		}
	}
}

// TestResolveOrgServices_NoResolverWired asserts the adaptation from the
// ported source: this codebase deleted the static ExternalAPICatalog
// fallback (task A1), so when no resolver has been wired via
// SetOrgServiceResolver, org-service dependencies must keep whatever
// Status/Reason they already carry (always empty — Status/Reason are never
// persisted to design.json) rather than falling back to a static catalog.
func TestResolveOrgServices_NoResolverWired(t *testing.T) {
	store := &ArtifactStore{}

	d := &DesignFile{Components: []models.DesignComponent{{
		Name: "web",
		Dependencies: []models.Dependency{
			{Kind: models.DependencyKindOrgService, Name: "employee-api"},
		},
	}}}

	store.resolveOrgServices(context.Background(), "org", d)

	dep := d.Components[0].Dependencies[0]
	if dep.Status != "" {
		t.Errorf("no resolver wired: status = %q, want empty", dep.Status)
	}
	if dep.Reason != "" {
		t.Errorf("no resolver wired: reason = %q, want empty", dep.Reason)
	}
}

// TestResolveOrgServices_IsNamespaceVisibleError asserts a resolver error
// from IsNamespaceVisible never fails the design read: it leaves the
// dependency's Status/Reason exactly as they were (matches the ported
// source's "leave whatever status is stored" behavior).
func TestResolveOrgServices_IsNamespaceVisibleError(t *testing.T) {
	store := &ArtifactStore{}
	store.SetOrgServiceResolver(fakeOrgServiceResolver{
		visibleErr: map[string]error{"employee-api": errors.New("transient OC error")},
	})

	d := &DesignFile{Components: []models.DesignComponent{{
		Name: "web",
		Dependencies: []models.Dependency{
			{Kind: models.DependencyKindOrgService, Name: "employee-api"},
		},
	}}}

	store.resolveOrgServices(context.Background(), "org", d)

	dep := d.Components[0].Dependencies[0]
	if dep.Status != "" {
		t.Errorf("IsNamespaceVisible error: status = %q, want untouched (empty)", dep.Status)
	}
	if dep.Reason != "" {
		t.Errorf("IsNamespaceVisible error: reason = %q, want untouched (empty)", dep.Reason)
	}
}

// TestResolveOrgServices_ExistsAnyVisibilityError documents the ported
// source's exact (slightly asymmetric) error behavior: resolveOrgServices
// sets status=unresolved BEFORE calling ExistsAnyVisibility to refine it, so
// an ExistsAnyVisibility error leaves the dependency at status="unresolved"
// with an empty reason — NOT a fully untouched/empty status.
func TestResolveOrgServices_ExistsAnyVisibilityError(t *testing.T) {
	store := &ArtifactStore{}
	store.SetOrgServiceResolver(fakeOrgServiceResolver{
		existsErr: map[string]error{"payroll-internal": errors.New("transient OC error")},
	})

	d := &DesignFile{Components: []models.DesignComponent{{
		Name: "web",
		Dependencies: []models.Dependency{
			{Kind: models.DependencyKindOrgService, Name: "payroll-internal"},
		},
	}}}

	store.resolveOrgServices(context.Background(), "org", d)

	dep := d.Components[0].Dependencies[0]
	if dep.Status != models.DependencyStatusUnresolved {
		t.Errorf("ExistsAnyVisibility error: status = %q, want %q", dep.Status, models.DependencyStatusUnresolved)
	}
	if dep.Reason != "" {
		t.Errorf("ExistsAnyVisibility error: reason = %q, want empty", dep.Reason)
	}
}

// TestReadDesign_ResolvesOrgServiceDependencies is the read-path wiring
// test: resolveOrgServices must run AFTER AssembleDesign inside ReadDesign,
// mutating only the org-service dependency's Status/Reason. It also covers
// "resolver error never fails the read" at the ReadDesign level (not just
// the resolveOrgServices unit).
func TestReadDesign_ResolvesOrgServiceDependencies(t *testing.T) {
	files := map[string]string{
		DesignRootFile: "Some overview.\n",
		"components/consumer/design.json": `{
  "name": "consumer",
  "type": "service",
  "dependencies": [
    {
      "kind": "org-service",
      "name": "payroll-internal"
    }
  ]
}
`,
	}

	newStore := func(resolver OrgServiceResolver) *ArtifactStore {
		store := NewArtifactStore(stubDesignListingService{files: files})
		if resolver != nil {
			store.SetOrgServiceResolver(resolver)
		}
		return store
	}

	t.Run("resolver wired resolves the dependency", func(t *testing.T) {
		store := newStore(fakeOrgServiceResolver{visible: map[string]bool{"payroll-internal": true}})

		design, err := store.ReadDesign(context.Background(), "org1", "proj1")
		if err != nil {
			t.Fatalf("ReadDesign: %v", err)
		}
		dep := design.Components[0].Dependencies[0]
		if dep.Status != models.DependencyStatusResolved {
			t.Errorf("status = %q, want %q", dep.Status, models.DependencyStatusResolved)
		}
	})

	t.Run("no resolver wired leaves status empty", func(t *testing.T) {
		store := newStore(nil)

		design, err := store.ReadDesign(context.Background(), "org1", "proj1")
		if err != nil {
			t.Fatalf("ReadDesign: %v", err)
		}
		dep := design.Components[0].Dependencies[0]
		if dep.Status != "" {
			t.Errorf("status = %q, want empty", dep.Status)
		}
	})

	t.Run("resolver error still succeeds the read", func(t *testing.T) {
		store := newStore(fakeOrgServiceResolver{
			visibleErr: map[string]error{"payroll-internal": errors.New("transient OC error")},
		})

		design, err := store.ReadDesign(context.Background(), "org1", "proj1")
		if err != nil {
			t.Fatalf("ReadDesign: %v", err)
		}
		if design == nil {
			t.Fatal("ReadDesign: design is nil")
		}
		dep := design.Components[0].Dependencies[0]
		if dep.Status != "" {
			t.Errorf("status = %q, want untouched (empty)", dep.Status)
		}
	})
}
