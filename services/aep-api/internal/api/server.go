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

	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/httpkit"
)

// apiServer implements the generated strict interface (gen.StrictServerInterface)
// for the public /api/v1 edge — every operation of the committed contract, one
// per-feature handlers_*.go file. Handlers read the gate-bound org via
// tenant.BoundOrgFromContext and pass it to services as an explicit argument —
// services never dig org out of context.
type apiServer struct {
	deps Deps
}

var _ gen.StrictServerInterface = (*apiServer)(nil)

// newAPIV1Handler assembles the whole contract-first serving chain for the
// public edge, innermost first:
//
//	strict impl (apiServer)               handlers_*.go, per feature
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
		&apiServer{deps: deps},
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
