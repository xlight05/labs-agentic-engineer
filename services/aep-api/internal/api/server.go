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

package api

import (
	"net/http"

	deliveryhttpapi "github.com/wso2/aep/aep-api/internal/delivery/httpapi"
	"github.com/wso2/aep/aep-api/internal/gen"
	opshttpapi "github.com/wso2/aep/aep-api/internal/ops/httpapi"
	orghttpapi "github.com/wso2/aep/aep-api/internal/organization/httpapi"
	"github.com/wso2/aep/aep-api/internal/platform/httpkit"
	projectshttpapi "github.com/wso2/aep/aep-api/internal/projects/httpapi"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	schttpapi "github.com/wso2/aep/aep-api/internal/sourcecontrol/httpapi"
	spechttpapi "github.com/wso2/aep/aep-api/internal/spec/httpapi"
)

// legacyHandlers holds every operation that has NOT yet moved into its domain
// slice — one per-feature handlers_*.go file. Handlers read the gate-bound org
// via tenant.BoundOrgFromContext and pass it to services as an explicit
// argument — services never dig org out of context.
//
// It shrinks by one method per migrated operation and is deleted in P9, when
// the last op lands in its domain (docs/design/domain-oriented-architecture.md
// §19.3).
type legacyHandlers struct {
	deps Deps
}

// legacyShim exists for exactly one reason: to equalise embedding depth.
//
// aep:migration-shim retires=P9 reason=deleted with legacyHandlers once every op has moved into its domain
//
// The domain path is two levels deep — apiServer → <domain>/httpapi.Handlers →
// <slice>.Handler — so a migrated op's method reaches apiServer at depth-2. If
// legacyHandlers were embedded DIRECTLY, its methods would sit at depth-1, and
// Go's promotion rule (shallowest wins, silently) means an op that was added to
// its domain but NOT cut from legacy would still resolve to the stale legacy
// body: it compiles green, the new slice is dead code, and componenttest cannot
// see it because both bodies return the same shape.
//
// Wrapping legacy in this shim puts its methods at depth-2 as well, so the two
// candidates tie — and a tie is an `ambiguous selector` COMPILE error, not a
// silent stale serve. The likeliest migration slip becomes a build failure.
// (Verified against all four states; §19.1 records the matrix.)
type legacyShim struct{ *legacyHandlers }

// apiServer implements the generated strict interface (gen.StrictServerInterface)
// for the public /api/v1 edge by METHOD PROMOTION ONLY — it declares no methods
// of its own. Every operation of the committed contract is promoted from exactly
// one embed, all at equal depth:
//
//   - legacyShim              ops not yet migrated (shrinks each phase, gone in P9)
//   - *<domain>/httpapi.Handlers   one embed per landed domain (added from P1)
//
// TestMethodOrigin pins WHICH embed each op comes from, so a migration that
// forgets to cut the legacy method fails the build (ambiguity) and a migration
// that moves an op silently fails the test.
type apiServer struct {
	legacyShim
	// One embed per landed domain phase (P1–P8).
	*opsHandlers           // P1 — ops (Incident RCA)
	*sourcecontrolHandlers // P2 — sourcecontrol (Source Control & Webhooks)
	*organizationHandlers  // P3 — organization (Org Config & Organizations)
	*specHandlers          // P4 — spec (Spec Authoring & Versioning)
	*deliveryHandlers      // P6 — delivery (Build, Tasks & Task-log stream)
	*projectsHandlers      // P7 — projects (Projects, Components, Builds & Config)
}

// An embedded field is named by its UNQUALIFIED type name, so every domain's
// *httpapi.Handlers would collide as "Handlers". Local aliases give distinct
// field names while each domain keeps the clean, unstuttering type name (§6).
// One alias per landed domain.
type (
	opsHandlers           = opshttpapi.Handlers
	sourcecontrolHandlers = schttpapi.Handlers
	organizationHandlers  = orghttpapi.Handlers
	specHandlers          = spechttpapi.Handlers
	deliveryHandlers      = deliveryhttpapi.Handlers
	projectsHandlers      = projectshttpapi.Handlers
)

// Proves the METHOD SET only — never the wiring: it uses a nil pointer, so a
// nil sub-handler inside a Module still satisfies this and panics at runtime.
// Non-nil wiring is asserted by the per-domain assembly tests.
var _ gen.StrictServerInterface = (*apiServer)(nil)

// newAPIV1Handler assembles the whole contract-first serving chain for the
// public edge, innermost first:
//
//	strict impl (apiServer)               promotion-only composite: legacyShim
//	                                       (handlers_*.go) + one embed per landed domain
//	→ tenant gate                          deny-by-default, tenant_gate.go
//	→ strict wrapper                       generated; envelope error writers
//	→ generated std ServeMux router        one pattern per contract operation
//	→ read-file catch-all                  nested {path} segments (see below)
//	→ request validator                    kin-openapi against the contract
//
// The caller mounts the result under the outer jwt → orgensure → gate-mode
// middleware (mountSurfaces), exactly where the Huma mux used to sit.
func newAPIV1Handler(deps Deps) http.Handler {
	strict := gen.NewStrictHandlerWithOptions(
		&apiServer{
			legacyShim:            legacyShim{&legacyHandlers{deps: deps}},
			opsHandlers:           deps.Ops,
			sourcecontrolHandlers: sourceControlOrEmpty(deps.SourceControl),
			organizationHandlers:  deps.Organization,
			specHandlers:          deps.Spec,
			deliveryHandlers:      deps.Delivery,
			projectsHandlers:      deps.Projects,
		},
		[]gen.StrictMiddlewareFunc{tenantGate},
		gen.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  writeRequestError,
			ResponseErrorHandlerFunc: writeResponseError,
		},
	)

	mux := http.NewServeMux()
	gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseURL:          httpkit.APIV1,
		BaseRouter:       mux,
		ErrorHandlerFunc: writeRequestError,
	})

	// read-file's {path} is a trailing wildcard (documented in the contract):
	// the generated single-segment pattern can't match nested spec paths, so
	// the same wrapped handler is also registered under the ServeMux catch-all.
	// Single-segment requests keep hitting the generated pattern (more
	// specific); multi-segment ones land here. PathValue("path") serves both.
	siw := &gen.ServerInterfaceWrapper{Handler: strict, ErrorHandlerFunc: writeRequestError}
	mux.HandleFunc("GET "+httpkit.APIV1+"/projects/{projectName}/files/{path...}", siw.ReadFile)

	return capRequestBody(requestValidator(mux))
}

// sourceControlOrEmpty keeps the harness contract Deps documents: a component
// test wires only the feature under test, and an unwired surface answers 503
// rather than panicking.
//
// A domain is embedded as a POINTER, so an unwired domain is a nil embed and any
// of its ops panics — a 500 where the pre-migration handler nil-guarded to 503.
// Assembling the domain with zero Deps restores it: sourcecontrol's ports are
// nil-tolerant by design, so every slice degrades to 503 exactly as before.
// (ops is different and deliberately so: its pre-migration handlers had no nil
// guard either, so it keeps failing loudly.)
func sourceControlOrEmpty(h *sourcecontrolHandlers) *sourcecontrolHandlers {
	if h != nil {
		return h
	}
	empty, err := schttpapi.New(sourcecontrol.Deps{})
	if err != nil {
		// Unreachable: zero Deps is a supported configuration for this domain.
		panic("api: assembling an empty sourcecontrol domain: " + err.Error())
	}
	return empty
}

// maxBodyBytes is the edge-wide request-body ceiling (413 beyond it). 10 MiB
// was the largest of the retired per-op caps (the files batch apply);
// import-skill keeps its own tighter in-handler limit.
const maxBodyBytes = 10 << 20

// capRequestBody bounds every request body before the validator (the first
// reader) touches it; an oversized body surfaces as *http.MaxBytesError and
// answers 413 (writeValidationError's dedicated branch).
func capRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}
