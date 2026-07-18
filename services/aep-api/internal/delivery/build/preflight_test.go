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

package build

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wso2/aep/aep-api/internal/spec"
)

// ----- fakes -----------------------------------------------------------------

type fakeDesign struct {
	comps []spec.DesignComponent
	err   error
}

func (f fakeDesign) ReadDesignComponents(context.Context, string, string) ([]spec.DesignComponent, error) {
	return f.comps, f.err
}

// fakeStatus reports every dependency as NOT ready (nothing provisioned or
// in-flight) — the "everything still needs the drawer" baseline the
// filtering-rule tests build on.
type fakeStatus struct {
	err error
}

func (f fakeStatus) Ready(context.Context, string, string, string) (bool, error) {
	return false, f.err
}

// kindsByDep groups the emitted items' kinds by the dependency name they were
// raised for, so a test can assert "stripe produced exactly {external-spec,
// external-config}" without caring about item order.
func kindsByDep(items []PreflightItem) map[string][]string {
	out := make(map[string][]string, len(items))
	for _, it := range items {
		out[it.Dependency] = append(out[it.Dependency], it.Kind)
	}
	return out
}

// ----- tests -------------------------------------------------------------------

func TestPreflight_ItemsPerKind(t *testing.T) {
	comps := []spec.DesignComponent{{Name: "orders", ComponentType: spec.ComponentTypeService,
		Dependencies: []spec.Dependency{
			{Kind: spec.DependencyKindExternal, Name: "stripe", NeedsSpec: true,
				Config: []spec.ConfigKey{{Key: "STRIPE_KEY", Secret: true}, {Key: "STRIPE_ORG", Secret: false}}},
			{Kind: spec.DependencyKindPlatformResource, Name: "orders-db", ResourceType: "postgres-cnpg", Parameters: map[string]any{"instances": 1}},
			{Kind: spec.DependencyKindOrgService, Name: "billing", Status: spec.DependencyStatusUnresolved},
			{Kind: spec.DependencyKindOrgService, Name: "audit", Status: spec.DependencyStatusResolved},
		}}}
	svc := NewPreflightService(PreflightDeps{Design: fakeDesign{comps: comps}, Status: fakeStatus{}})
	pf, err := svc.Preflight(context.Background(), "acme", "shop")
	require.NoError(t, err)
	require.True(t, pf.NeedsInput)
	kinds := kindsByDep(pf.Items)
	require.ElementsMatch(t, []string{"external-spec", "external-config"}, kinds["stripe"])
	require.Equal(t, []string{"platform-resource"}, kinds["orders-db"])
	require.Equal(t, []string{"org-service"}, kinds["billing"])
	_, present := kinds["audit"]
	require.False(t, present)
}

// The external-config item's Config carries key/secret/description VIEWS only —
// never values (there are none to leak on a Dependency, but the shape must stay
// value-free so a future value-bearing field never rides along). The optional
// per-key description threads through so the drawer can render it as a hint.
func TestPreflight_ExternalConfigItem_CarriesKeySecretDescriptionViewsOnly(t *testing.T) {
	comps := []spec.DesignComponent{{Name: "orders", ComponentType: spec.ComponentTypeService,
		Dependencies: []spec.Dependency{
			{Kind: spec.DependencyKindExternal, Name: "stripe",
				Config: []spec.ConfigKey{{Key: "STRIPE_KEY", Secret: true, Description: "Your Stripe secret API key"}, {Key: "STRIPE_ORG", Secret: false, DefaultValue: "acme"}}},
		}}}
	svc := NewPreflightService(PreflightDeps{Design: fakeDesign{comps: comps}, Status: fakeStatus{}})
	pf, err := svc.Preflight(context.Background(), "acme", "shop")
	require.NoError(t, err)
	require.Len(t, pf.Items, 1)
	item := pf.Items[0]
	require.Equal(t, "external-config", item.Kind)
	require.Equal(t, []ConfigKeyView{{Key: "STRIPE_KEY", Secret: true, Description: "Your Stripe secret API key"}, {Key: "STRIPE_ORG", Secret: false, DefaultValue: "acme"}}, item.Config)
}

// A platform-resource item carries its ResourceType + Parameters through —
// the drawer's provision call needs both.
func TestPreflight_PlatformResourceItem_CarriesResourceTypeAndParameters(t *testing.T) {
	comps := []spec.DesignComponent{{Name: "orders", ComponentType: spec.ComponentTypeService,
		Dependencies: []spec.Dependency{
			{Kind: spec.DependencyKindPlatformResource, Name: "orders-db", ResourceType: "postgres-cnpg", Parameters: map[string]any{"instances": 1}},
		}}}
	svc := NewPreflightService(PreflightDeps{Design: fakeDesign{comps: comps}, Status: fakeStatus{}})
	pf, err := svc.Preflight(context.Background(), "acme", "shop")
	require.NoError(t, err)
	require.Len(t, pf.Items, 1)
	item := pf.Items[0]
	require.Equal(t, "orders-db", item.Dependency)
	require.Equal(t, "postgres-cnpg", item.ResourceType)
	require.Equal(t, map[string]any{"instances": 1}, item.Parameters)
}

// A "blocked" or "ambiguous" org-service dependency also needs the drawer —
// only "resolved" is skipped.
func TestPreflight_OrgServiceBlockedAndAmbiguous_AlsoEmit(t *testing.T) {
	comps := []spec.DesignComponent{{Name: "orders", ComponentType: spec.ComponentTypeService,
		Dependencies: []spec.Dependency{
			{Kind: spec.DependencyKindOrgService, Name: "payments", Status: spec.DependencyStatusBlocked},
			{Kind: spec.DependencyKindOrgService, Name: "shipping", Status: spec.DependencyStatusAmbiguous},
		}}}
	svc := NewPreflightService(PreflightDeps{Design: fakeDesign{comps: comps}, Status: fakeStatus{}})
	pf, err := svc.Preflight(context.Background(), "acme", "shop")
	require.NoError(t, err)
	kinds := kindsByDep(pf.Items)
	require.Equal(t, []string{"org-service"}, kinds["payments"])
	require.Equal(t, []string{"org-service"}, kinds["shipping"])
}

// A "component" kind dependency (sibling component) is never emitted — it is
// not provisioned via the drawer.
func TestPreflight_ComponentKindDependency_NeverEmits(t *testing.T) {
	comps := []spec.DesignComponent{{Name: "orders", ComponentType: spec.ComponentTypeService,
		Dependencies: []spec.Dependency{
			{Kind: spec.DependencyKindComponent, Name: "catalog"},
		}}}
	svc := NewPreflightService(PreflightDeps{Design: fakeDesign{comps: comps}, Status: fakeStatus{}})
	pf, err := svc.Preflight(context.Background(), "acme", "shop")
	require.NoError(t, err)
	require.False(t, pf.NeedsInput)
	require.Empty(t, pf.Items)
}

// A dependency already Ready (provisioned or in-flight) is not re-asked: the
// external-config and platform-resource items disappear once Status reports
// ready, but external-spec still fires independently (it gates the spec, not
// provisioning).
func TestPreflight_ReadyDependency_SkipsConfigAndResourceItems(t *testing.T) {
	comps := []spec.DesignComponent{{Name: "orders", ComponentType: spec.ComponentTypeService,
		Dependencies: []spec.Dependency{
			{Kind: spec.DependencyKindExternal, Name: "stripe", NeedsSpec: true},
			{Kind: spec.DependencyKindPlatformResource, Name: "orders-db", ResourceType: "postgres-cnpg"},
		}}}
	svc := NewPreflightService(PreflightDeps{Design: fakeDesign{comps: comps}, Status: readyStatus{}})
	pf, err := svc.Preflight(context.Background(), "acme", "shop")
	require.NoError(t, err)
	kinds := kindsByDep(pf.Items)
	require.Equal(t, []string{"external-spec"}, kinds["stripe"])
	_, present := kinds["orders-db"]
	require.False(t, present)
}

// external-spec is skipped once the design already carries a SpecPath or
// SpecUrl — the spec has already been supplied.
func TestPreflight_ExternalSpecAlreadySupplied_SkipsSpecItem(t *testing.T) {
	comps := []spec.DesignComponent{{Name: "orders", ComponentType: spec.ComponentTypeService,
		Dependencies: []spec.Dependency{
			{Kind: spec.DependencyKindExternal, Name: "stripe", NeedsSpec: true, SpecPath: "dependencies/stripe.openapi.yaml"},
		}}}
	svc := NewPreflightService(PreflightDeps{Design: fakeDesign{comps: comps}, Status: readyStatus{}})
	pf, err := svc.Preflight(context.Background(), "acme", "shop")
	require.NoError(t, err)
	require.False(t, pf.NeedsInput)
	require.Empty(t, pf.Items)
}

// Non-service components (e.g. web-application) carry no provisionable
// dependencies through the drawer.
func TestPreflight_NonServiceComponent_Skipped(t *testing.T) {
	comps := []spec.DesignComponent{{Name: "web", ComponentType: spec.ComponentTypeWebApplication,
		Dependencies: []spec.Dependency{
			{Kind: spec.DependencyKindExternal, Name: "stripe", NeedsSpec: true},
		}}}
	svc := NewPreflightService(PreflightDeps{Design: fakeDesign{comps: comps}, Status: fakeStatus{}})
	pf, err := svc.Preflight(context.Background(), "acme", "shop")
	require.NoError(t, err)
	require.False(t, pf.NeedsInput)
	require.Empty(t, pf.Items)
}

// readyStatus reports every dependency as already ready — the "nothing left
// to ask" fake used by the skip-on-ready tests.
type readyStatus struct{}

func (readyStatus) Ready(context.Context, string, string, string) (bool, error) { return true, nil }
