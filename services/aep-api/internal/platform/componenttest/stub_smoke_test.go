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

package componenttest

import (
	"testing"

	"github.com/wso2/aep/aep-api/internal/api"
)

// TestStubSurface pins the migration-window contract (issue 002): an
// operation whose feature has NOT yet moved onto the strict interface answers
// a typed 501 not_implemented envelope — never a silent 404 — through the
// full production chain (so the gate still runs first: claimless is 401
// before the stub is reached). Delete when the last stub goes (issue 003).
func TestStubSurface(t *testing.T) {
	t.Parallel()
	h := New(t, Options{Deps: api.HumaDeps{}})

	// list-components is unmigrated at the tracer stage.
	resp := h.AsOrg("acme").Get("/api/v1/projects/web/components")
	if resp.Code != 501 {
		t.Fatalf("unmigrated op: want 501, got %d body=%s", resp.Code, resp.Body.String())
	}
	if e := DecodeEnvelope(t, resp.Body.String()); e.Code != "not_implemented" {
		t.Fatalf("stub envelope: %s", resp.Body.String())
	}

	// The deny-by-default gate outranks the stub: claimless is the gate's 401.
	if resp := h.NoAuth().Get("/api/v1/projects/web/components"); resp.Code != 401 {
		t.Fatalf("claimless unmigrated op: want gate 401, got %d body=%s", resp.Code, resp.Body.String())
	}

	// An unknown path stays a plain 404 (route-miss falls through the
	// validator to the mux — not a stub, not an envelope guarantee).
	if resp := h.AsOrg("acme").Get("/api/v1/definitely-not-a-route"); resp.Code != 404 {
		t.Fatalf("unknown route: want 404, got %d", resp.Code)
	}
}
