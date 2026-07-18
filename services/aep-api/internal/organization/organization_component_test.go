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

// COMPONENT tier: list-organizations driven
// through the REAL production chain (faked auth at the jwt.WithClaims seam →
// orgensure → Huma parsing → mapOrganizationError) via the componenttest
// harness, with only the out-of-process NamespaceClient mocked. The happy-path
// body is asserted against the harvested golden
// testdata/harvest/golden/get_organizations.json.
//
// WHY NO GATE / NO NoAuth-401 CASE (deliberate, not a gap): list-organizations
// is the pre-org-selection carve-out (api.tenantGateCarveOuts, §6.6f) — the
// deny-by-default tenant gate skips it, so its only auth is the JWKS
// verifier, which this harness fakes away. So a
// tokenless request is NOT rejected with a gate 401; it reaches the handler
// (pinned positively in TestOrganizationComponent_CarveOut_NoGate below). The
// verifier's real tokenless/forged rejection is a DIFFERENT code path and is
// owned by the integration tier. The one 401 that
// IS assertable here (TestOrganizationComponent_ErrorMapping) is the
// error-MAPPING 401 the service raises when OC itself answers unauthorized — not
// an auth-gate 401.
//
// DB posture: List touches the DB only for a NON-empty namespace list (to join
// local UUID rows), and the org service has no store PORT to fake — so the
// golden happy-path is a Component+DB flavour over dbtest.New (skips under
// -short, runs under `make test-db`), exactly like the orgcreds anthropic
// component test. The error-mapping and carve-out cases fail/short-circuit
// before any DB access, so they run under `make test` with a nil DB.
//
// External test package: the harness imports api, which imports organization —
// an in-package test file would be an import cycle.
package organization_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/wso2/aep/aep-api/internal/gen"

	"github.com/wso2/aep/aep-api/internal/api"
	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	ocmocks "github.com/wso2/aep/aep-api/internal/clients/openchoreo/mocks"
	"github.com/wso2/aep/aep-api/internal/organization"
	"github.com/wso2/aep/aep-api/internal/platform/componenttest"
	"github.com/wso2/aep/aep-api/internal/platform/dbtest"
	"github.com/wso2/aep/aep-api/repositories"
)

const orgListPath = "/api/v1/organizations"

// topKeysSansSchema returns the sorted top-level keys of a JSON object with the
// Huma-injected "$schema" removed — the low-maintenance on-wire contract
// (pin the field SET, not volatile values), excluded on BOTH sides per the
// migration convention.
func topKeysSansSchema(t *testing.T, raw []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, raw)
	}
	delete(m, "$schema")
	return sortedKeys(m)
}

// firstItemKeys returns the sorted keys of items[0] — the array golden's element
// shape (no $schema exists at the element level).
func firstItemKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var obj struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("not an OrganizationList: %v\n%s", err, raw)
	}
	if len(obj.Items) == 0 {
		t.Fatalf("expected at least one item, got none:\n%s", raw)
	}
	return sortedKeys(obj.Items[0])
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func readGolden(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "harvest", "golden", "get_organizations.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	return raw
}

// TestOrganizationComponent_ListMatchesGoldenFieldSet is the Component+DB
// happy-path: a live namespace joins its backfilled local UUID row and the
// response's field set (top level sans $schema, plus items[0]) matches the
// harvested golden exactly. Values are volatile (UUID/timestamp), so only the
// SHAPE is pinned against the golden; the load-bearing semantics are asserted
// directly.
func TestOrganizationComponent_ListMatchesGoldenFieldSet(t *testing.T) {
	t.Parallel()
	db := dbtest.New(t) // skips under -short
	ns := &ocmocks.NamespaceClientMock{
		ListNamespacesFunc: func(context.Context) ([]gen.OrganizationView, error) {
			// DisplayName/Description left empty → omitempty drops them, matching
			// the golden's element shape {createdAt, name, status, uuid}.
			return []gen.OrganizationView{{Name: "default", Status: "Active"}}, nil
		},
	}
	svc := organization.NewOrganizationService(repositories.NewOrganizationRepository(db), ns)
	h := componenttest.New(t, componenttest.Options{Deps: api.Deps{Organization: mustNewOrgHandlers(t, organization.Deps{OrgSvc: svc})}})

	resp := h.AsOrg("acme").Get(orgListPath)
	if resp.Code != 200 {
		t.Fatalf("list: want 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	golden := readGolden(t)
	if got, want := topKeysSansSchema(t, resp.Body.Bytes()), topKeysSansSchema(t, golden); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("top-level field set drifted from golden:\n got %v\nwant %v", got, want)
	}
	if got, want := firstItemKeys(t, resp.Body.Bytes()), firstItemKeys(t, golden); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("items[0] field set drifted from golden:\n got %v\nwant %v", got, want)
	}

	// Semantics (values are scrubbed in the golden): the OC namespace name and
	// status flow through, and the item carries a non-nil backfilled UUID.
	var out gen.OrganizationList
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].Name != "default" || out.Items[0].Status != "Active" {
		t.Fatalf("list semantics: %s", resp.Body.String())
	}
	if out.Items[0].UUID.String() == "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("item must carry a backfilled UUID: %s", resp.Body.String())
	}
}

// TestOrganizationComponent_ErrorMapping drives mapOrganizationError through the
// real chain: an OC unauthorized surfaces as the error-mapping 401 (NOT a gate
// 401 — this op has no gate), and any other failure is an opaque 500 whose body
// never leaks the underlying cause. Both fail before List touches the DB, so a
// nil DB is safe and these run under `make test`.
func TestOrganizationComponent_ErrorMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		clientErr  error
		wantStatus int
		wantDetail string // envelope message
	}{
		{"oc unauthorized → 401", openchoreo.ErrUnauthorized, 401, "invalid or expired token"},
		{"oc forbidden → opaque 500", openchoreo.ErrForbidden, 500, "failed to list organizations"},
		{"opaque error → 500 without leaking internals", errors.New("pg: connection refused"), 500, "failed to list organizations"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ns := &ocmocks.NamespaceClientMock{
				ListNamespacesFunc: func(context.Context) ([]gen.OrganizationView, error) {
					return nil, tc.clientErr
				},
			}
			svc := organization.NewOrganizationService(nil, ns) // nil DB: errors before any DB access
			h := componenttest.New(t, componenttest.Options{Deps: api.Deps{Organization: mustNewOrgHandlers(t, organization.Deps{OrgSvc: svc})}})

			resp := h.AsOrg("acme").Get(orgListPath)
			if resp.Code != tc.wantStatus {
				t.Fatalf("want %d, got %d body=%s", tc.wantStatus, resp.Code, resp.Body.String())
			}
			if e := componenttest.DecodeEnvelope(t, resp.Body.String()); e.Message != tc.wantDetail {
				t.Fatalf("message: got %q want %q", e.Message, tc.wantDetail)
			}
			if tc.wantStatus == 500 && strings.Contains(resp.Body.String(), "connection refused") {
				t.Fatalf("500 body leaks internals: %s", resp.Body.String())
			}
		})
	}
}

// TestOrganizationComponent_CarveOut_NoGate pins the carve-out positively: a
// tokenless request to list-organizations is NOT rejected with a gate 401 — it
// reaches the handler and returns 200 — because the op embeds no OrgScopedInput.
// (Contrast the gated ops, where NoAuth is a 401 at the gate; see
// componenttest/smoke_test.go.) The verifier's real tokenless rejection is a
// different code path, faked away here and owned by the integration tier.
func TestOrganizationComponent_CarveOut_NoGate(t *testing.T) {
	t.Parallel()
	ns := &ocmocks.NamespaceClientMock{
		ListNamespacesFunc: func(context.Context) ([]gen.OrganizationView, error) {
			return nil, nil // empty → List short-circuits before the DB (nil DB safe)
		},
	}
	svc := organization.NewOrganizationService(nil, ns)
	h := componenttest.New(t, componenttest.Options{Deps: api.Deps{Organization: mustNewOrgHandlers(t, organization.Deps{OrgSvc: svc})}})

	resp := h.NoAuth().Get(orgListPath)
	if resp.Code == 401 {
		t.Fatalf("ungated carve-out must not gate-reject a tokenless request; got 401 body=%s", resp.Body.String())
	}
	if resp.Code != 200 {
		t.Fatalf("tokenless request should reach the handler (200 empty), got %d body=%s", resp.Code, resp.Body.String())
	}
}
