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

// COMPONENT tier: the platform-resource-type
// discovery endpoint through the REAL production handler chain — faked auth →
// contract validation → the deny-by-default tenant gate in ENFORCE → the
// strict handler (handlers_dependencies.go) — with only the resource-type
// lister faked. Replaces the retired Huma registration/spec test; the
// domain→DTO projection assertions now ride the wire response.
//
// External test package: the harness imports api, which imports dependencies —
// an in-package test file would be an import cycle.
package dependencies_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wso2/aep/aep-api/internal/gen"

	"github.com/wso2/aep/aep-api/internal/api"
	"github.com/wso2/aep/aep-api/internal/feature/dependencies/resources"
	"github.com/wso2/aep/aep-api/internal/platform/componenttest"
)

// stubResourceTypeLister satisfies dependencies.ResourceTypeLister.
type stubResourceTypeLister struct {
	types []resources.PlatformResourceType
	err   error
}

func (s stubResourceTypeLister) List(context.Context) ([]resources.PlatformResourceType, error) {
	return s.types, s.err
}

func newLister(t *testing.T, lister stubResourceTypeLister) *componenttest.Harness {
	t.Helper()
	return componenttest.New(t, componenttest.Options{Deps: api.Deps{ResourceTypeCatalog: lister}})
}

// TestPlatformResourceTypes_MapsDomainToDTOs verifies the domain→DTO
// projection on the wire: the architect-facing fields (name, description,
// parameters, outputs) map through, and a type with no parameters/outputs
// projects to empties.
func TestPlatformResourceTypes_MapsDomainToDTOs(t *testing.T) {
	t.Parallel()
	h := newLister(t, stubResourceTypeLister{types: []resources.PlatformResourceType{
		{
			Name:        "postgres-cnpg",
			Description: "Managed PostgreSQL",
			Parameters:  map[string]any{"size": map[string]any{"type": "string"}},
			Outputs:     []string{"host", "port", "database"},
		},
		{Name: "redis-cache"}, // minimal: no description/params/outputs
	}})

	resp := h.AsOrg("acme").Get("/api/v1/dependencies/platform-resource-types")
	if resp.Code != 200 {
		t.Fatalf("list: got %d body=%s", resp.Code, resp.Body.String())
	}
	var got []gen.PlatformResourceTypeDTO
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("body: %v\n%s", err, resp.Body.String())
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "postgres-cnpg" || got[0].Description != "Managed PostgreSQL" {
		t.Errorf("name/description not mapped: %+v", got[0])
	}
	if _, ok := got[0].Parameters["size"]; !ok {
		t.Errorf("parameters not mapped: %+v", got[0].Parameters)
	}
	if len(got[0].Outputs) != 3 || got[0].Outputs[0] != "host" {
		t.Errorf("outputs not mapped: %+v", got[0].Outputs)
	}
	if got[1].Name != "redis-cache" || got[1].Parameters != nil || got[1].Outputs != nil {
		t.Errorf("minimal type not projected cleanly: %+v", got[1])
	}
}

// A catalog read failure is an upstream (data-plane) fault — 502, opaque.
func TestPlatformResourceTypes_UpstreamFailure502(t *testing.T) {
	t.Parallel()
	h := newLister(t, stubResourceTypeLister{err: context.DeadlineExceeded})

	resp := h.AsOrg("acme").Get("/api/v1/dependencies/platform-resource-types")
	if resp.Code != 502 {
		t.Fatalf("want 502, got %d body=%s", resp.Code, resp.Body.String())
	}
	e := componenttest.DecodeEnvelope(t, resp.Body.String())
	if e.Code != "bad_gateway" || e.Message != "failed to list platform resource types" {
		t.Fatalf("502 envelope = %+v", e)
	}
}

// A nil catalog keeps the surface present but unwired — 503, mirroring the
// retired RegisterResourceTypes nil guard.
func TestPlatformResourceTypes_Unconfigured503(t *testing.T) {
	t.Parallel()
	h := componenttest.New(t, componenttest.Options{Deps: api.Deps{}})

	resp := h.AsOrg("acme").Get("/api/v1/dependencies/platform-resource-types")
	if resp.Code != 503 {
		t.Fatalf("want 503, got %d body=%s", resp.Code, resp.Body.String())
	}
	e := componenttest.DecodeEnvelope(t, resp.Body.String())
	if e.Code != "service_unavailable" || e.Message != "resource-type catalog is not configured" {
		t.Fatalf("503 envelope = %+v", e)
	}
}

// The endpoint requires an authenticated caller (the org-scoped auth fence):
// claimless is the gate's ENFORCE 401.
func TestPlatformResourceTypes_NoClaims401(t *testing.T) {
	t.Parallel()
	h := newLister(t, stubResourceTypeLister{})

	if resp := h.NoAuth().Get("/api/v1/dependencies/platform-resource-types"); resp.Code != 401 {
		t.Fatalf("claimless: want 401, got %d body=%s", resp.Code, resp.Body.String())
	}
}
