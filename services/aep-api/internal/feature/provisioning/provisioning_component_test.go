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

// COMPONENT tier: the REAL provisioning service
// behind the REAL production handler chain — faked auth → contract validation
// → the deny-by-default tenant gate in ENFORCE → the strict handlers
// (handlers_provisioning.go) and their mapProvisionError dialect — driven
// in-process via the componenttest harness with the service's out-of-process
// ports faked. The provisioning behavior itself is unit-proven in
// provisioning_test.go; this tier pins the HTTP contract: routes, status
// codes, the flat error envelope, and the no-claims 401.
//
// External test package: the harness imports api, which imports provisioning —
// an in-package test file would be an import cycle. (The in-package fakes in
// provisioning_test.go are not visible here; this file keeps its own minimal
// set.)
package provisioning_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wso2/aep/aep-api/internal/api/apigen"

	"github.com/wso2/aep/aep-api/internal/api"
	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/feature/gitrepo"
	"github.com/wso2/aep/aep-api/internal/feature/provisioning"
	"github.com/wso2/aep/aep-api/internal/platform/componenttest"
	"github.com/wso2/aep/aep-api/models"
)

// ----- minimal port fakes ------------------------------------------------------

type cCatalog struct {
	entries map[string]*models.ExternalResource
	deleted []string
}

func (f *cCatalog) Get(_ context.Context, _, name string) (*models.ExternalResource, error) {
	return f.entries[name], nil
}
func (f *cCatalog) List(_ context.Context, _ string) ([]models.ExternalResource, error) {
	out := make([]models.ExternalResource, 0, len(f.entries))
	for _, e := range f.entries {
		out = append(out, *e)
	}
	return out, nil
}
func (f *cCatalog) Delete(_ context.Context, _, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}

type cDesign struct{ comps []models.DesignComponent }

func (f cDesign) ReadDesignComponents(context.Context, string, string) ([]models.DesignComponent, error) {
	return f.comps, nil
}

type cProjects struct{ refs []provisioning.ProjectRef }

func (f cProjects) ListProjects(_ context.Context, orgID string) ([]provisioning.ProjectRef, error) {
	var out []provisioning.ProjectRef
	for _, r := range f.refs {
		if r.OrgID == orgID {
			out = append(out, r)
		}
	}
	return out, nil
}

type cBindings struct {
	byName map[string]*openchoreo.ResourceReleaseBinding
}

func (f *cBindings) GetBinding(_ context.Context, _, name string) (*openchoreo.ResourceReleaseBinding, error) {
	return f.byName[name], nil
}

type cIssues struct{}

func (cIssues) ListIssues(context.Context, string, string, []string) ([]gitrepo.IssueInfo, error) {
	return nil, nil
}
func (cIssues) CreateIssue(context.Context, string, string, gitrepo.CreateIssueRequest) (*gitrepo.IssueResult, error) {
	return &gitrepo.IssueResult{Number: 1}, nil
}
func (cIssues) CloseIssue(context.Context, string, string, int, string) error   { return nil }
func (cIssues) CommentIssue(context.Context, string, string, int, string) error { return nil }

type cRepos struct{}

func (cRepos) RepoFullName(context.Context, string, string) (string, error) { return "acme/shop", nil }
func (cRepos) ByFullName(context.Context, string) (string, string, error) {
	return "acme", "shop", nil
}

func readyBindingWith(outputs ...string) *openchoreo.ResourceReleaseBinding {
	st := &openchoreo.ResourceReleaseBindingStatus{
		Conditions: []openchoreo.OCCondition{{Type: "Ready", Status: "True"}},
	}
	for _, o := range outputs {
		st.Outputs = append(st.Outputs, openchoreo.ResolvedOutput{Name: o})
	}
	return &openchoreo.ResourceReleaseBinding{Status: st}
}

// stripeConsumerDesign declares an external dep "stripe" on project "proj"'s
// component "orders" — the consumer the catalog list/delete guard scans for.
func stripeConsumerDesign() []models.DesignComponent {
	return []models.DesignComponent{{
		Name: "orders",
		Dependencies: []models.Dependency{
			{Kind: models.DependencyKindExternal, Name: "stripe", Config: []models.ConfigKey{
				{Key: "api_key", Secret: true}, {Key: "region"},
			}},
		},
	}}
}

func newProvHarness(t *testing.T, svc *provisioning.Service) *componenttest.Harness {
	t.Helper()
	return componenttest.New(t, componenttest.Options{Deps: api.Deps{ProvisioningSvc: svc}})
}

// ----- tests --------------------------------------------------------------------

// A nil service keeps every provisioning route present but unwired — 503 with
// the flat envelope, mirroring the retired RegisterResources nil guard.
func TestProvisioningComponent_Unconfigured503(t *testing.T) {
	t.Parallel()
	h := componenttest.New(t, componenttest.Options{Deps: api.Deps{}})

	resp := h.AsOrg("acme").Get("/api/v1/dependencies/external-resources")
	if resp.Code != 503 {
		t.Fatalf("want 503, got %d body=%s", resp.Code, resp.Body.String())
	}
	e := componenttest.DecodeEnvelope(t, resp.Body.String())
	if e.Code != "service_unavailable" || e.Message != "provisioning is not configured" {
		t.Fatalf("503 envelope = %+v", e)
	}
}

func TestProvisioningComponent_NoClaims401(t *testing.T) {
	t.Parallel()
	h := newProvHarness(t, provisioning.NewService(provisioning.Deps{}))

	if resp := h.NoAuth().Get("/api/v1/dependencies/external-resources"); resp.Code != 401 {
		t.Fatalf("claimless: want the gate's ENFORCE 401, got %d body=%s", resp.Code, resp.Body.String())
	}
}

// list-external-resources: the catalog entry rides the wire with its config
// schema (never values) and its design-scanned consumers.
func TestProvisioningComponent_ListExternalResources(t *testing.T) {
	t.Parallel()
	svc := provisioning.NewService(provisioning.Deps{
		Catalog: &cCatalog{entries: map[string]*models.ExternalResource{
			"stripe": {Name: "stripe", Description: "payments", ConfigKeys: []models.ConfigKey{
				{Key: "api_key", Secret: true}, {Key: "region"},
			}},
		}},
		Design:   cDesign{comps: stripeConsumerDesign()},
		Projects: cProjects{refs: []provisioning.ProjectRef{{OrgID: "acme", ProjectID: "proj"}}},
	})
	h := newProvHarness(t, svc)

	resp := h.AsOrg("acme").Get("/api/v1/dependencies/external-resources")
	if resp.Code != 200 {
		t.Fatalf("list: got %d body=%s", resp.Code, resp.Body.String())
	}
	var got []apigen.ExternalResourceDTO
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("body: %v\n%s", err, resp.Body.String())
	}
	if len(got) != 1 || got[0].Name != "stripe" || got[0].Description != "payments" {
		t.Fatalf("resources = %+v, want the stripe entry", got)
	}
	if len(got[0].Config) != 2 || got[0].Config[0].Key != "api_key" || !got[0].Config[0].Secret {
		t.Errorf("config schema = %+v", got[0].Config)
	}
	if len(got[0].Consumers) != 1 || got[0].Consumers[0].ProjectID != "proj" || got[0].Consumers[0].ComponentName != "orders" {
		t.Errorf("consumers = %+v, want the design-scanned orders consumer", got[0].Consumers)
	}
}

// delete-external-resource: in use → 409 conflict envelope, nothing deleted;
// unused → 204 and the catalog entry is gone.
func TestProvisioningComponent_DeleteExternalResource(t *testing.T) {
	t.Parallel()
	catalog := &cCatalog{}
	svc := provisioning.NewService(provisioning.Deps{
		Catalog:  catalog,
		Design:   cDesign{comps: stripeConsumerDesign()},
		Projects: cProjects{refs: []provisioning.ProjectRef{{OrgID: "acme", ProjectID: "proj"}}},
	})
	h := newProvHarness(t, svc)

	resp := h.AsOrg("acme").Delete("/api/v1/dependencies/external-resources/stripe")
	if resp.Code != 409 {
		t.Fatalf("in-use delete: want 409, got %d body=%s", resp.Code, resp.Body.String())
	}
	e := componenttest.DecodeEnvelope(t, resp.Body.String())
	if e.Code != "conflict" || !strings.Contains(e.Message, "in use") {
		t.Fatalf("409 envelope = %+v", e)
	}
	if len(catalog.deleted) != 0 {
		t.Fatalf("an in-use resource must not be deleted")
	}

	resp = h.AsOrg("acme").Delete("/api/v1/dependencies/external-resources/unused")
	if resp.Code != 204 {
		t.Fatalf("unused delete: want 204, got %d body=%s", resp.Code, resp.Body.String())
	}
	if len(catalog.deleted) != 1 || catalog.deleted[0] != "unused" {
		t.Fatalf("unused resource must be deleted, got %v", catalog.deleted)
	}
}

// get-dependency-status: a ready binding reports ready with outputs masked to
// names (the default environment applies when the query param is absent).
func TestProvisioningComponent_DependencyStatus(t *testing.T) {
	t.Parallel()
	svc := provisioning.NewService(provisioning.Deps{
		Bindings: &cBindings{byName: map[string]*openchoreo.ResourceReleaseBinding{
			"proj-orders-db-development": readyBindingWith("host", "port"),
		}},
	})
	h := newProvHarness(t, svc)

	resp := h.AsOrg("acme").Get("/api/v1/projects/proj/components/orders/dependencies/orders-db/status")
	if resp.Code != 200 {
		t.Fatalf("status: got %d body=%s", resp.Code, resp.Body.String())
	}
	var st apigen.DependencyStatus
	if err := json.Unmarshal(resp.Body.Bytes(), &st); err != nil {
		t.Fatalf("body: %v\n%s", err, resp.Body.String())
	}
	if !st.Ready || st.Status != "ready" {
		t.Fatalf("want ready, got %+v", st)
	}
	if len(st.Outputs) != 2 || st.Outputs[0] != "host" {
		t.Fatalf("outputs must be the masked names, got %+v", st.Outputs)
	}
}

// provision-platform-resource on an external dependency is wrong-kind → 400
// bad_request carrying the sentinel text.
func TestProvisioningComponent_ProvisionWrongKind400(t *testing.T) {
	t.Parallel()
	svc := provisioning.NewService(provisioning.Deps{
		Issues: cIssues{},
		Repos:  cRepos{},
		Design: cDesign{comps: stripeConsumerDesign()},
	})
	h := newProvHarness(t, svc)

	resp := h.AsOrg("acme").Post("/api/v1/projects/proj/components/orders/dependencies/stripe/provision", "")
	if resp.Code != 400 {
		t.Fatalf("wrong-kind provision: want 400, got %d body=%s", resp.Code, resp.Body.String())
	}
	e := componenttest.DecodeEnvelope(t, resp.Body.String())
	if e.Code != "bad_request" || !strings.Contains(e.Message, "dependency kind does not support this action") {
		t.Fatalf("400 envelope = %+v", e)
	}
}

// request-org-service-access with no resolvable provider → 404 not_found; the
// consumer project's request list stays an empty (non-null) array.
func TestProvisioningComponent_AccessRequests(t *testing.T) {
	t.Parallel()
	svc := provisioning.NewService(provisioning.Deps{
		Design: cDesign{comps: stripeConsumerDesign()},
	})
	h := newProvHarness(t, svc)

	resp := h.AsOrg("acme").Post("/api/v1/projects/proj/components/web/dependencies/inventory/access-request", "")
	if resp.Code != 404 {
		t.Fatalf("unresolvable provider: want 404, got %d body=%s", resp.Code, resp.Body.String())
	}
	if e := componenttest.DecodeEnvelope(t, resp.Body.String()); e.Code != "not_found" {
		t.Fatalf("404 envelope = %+v", e)
	}

	resp = h.AsOrg("acme").Get("/api/v1/projects/proj/dependencies/access-requests")
	if resp.Code != 200 {
		t.Fatalf("list access requests: got %d body=%s", resp.Code, resp.Body.String())
	}
	if body := strings.TrimSpace(resp.Body.String()); body != "[]" {
		t.Fatalf("empty list must serialize as [], got %s", body)
	}
}
