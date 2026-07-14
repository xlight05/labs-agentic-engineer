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

	"github.com/wso2/aep/aep-api/internal/api/gen"
	"github.com/wso2/aep/aep-api/internal/platform/humakit"
)

// apiServer implements the generated strict interface (gen.StrictServerInterface)
// for the public /api/v1 edge. Migrated operations are real methods in the
// per-feature handlers_*.go files; everything else falls through to the
// embedded stubServer's 501 until its feature migrates (issue 003). Handlers
// read the gate-bound org via tenant.BoundOrgFromContext and pass it to
// services as an explicit argument — services never dig org out of context.
type apiServer struct {
	stubServer
	deps HumaDeps
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
func newAPIV1Handler(deps HumaDeps) http.Handler {
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
		BaseURL:          humakit.APIV1,
		BaseRouter:       mux,
		ErrorHandlerFunc: writeRequestError,
	})

	// read-file's {path} is a trailing wildcard (documented in the contract):
	// the generated single-segment pattern can't match nested spec paths, so
	// the same wrapped handler is also registered under the ServeMux catch-all.
	// Single-segment requests keep hitting the generated pattern (more
	// specific); multi-segment ones land here. PathValue("path") serves both.
	siw := &gen.ServerInterfaceWrapper{Handler: strict, ErrorHandlerFunc: writeRequestError}
	mux.HandleFunc("GET "+humakit.APIV1+"/projects/{projectName}/files/{path...}", siw.ReadFile)

	return requestValidator(mux)
}

// registerContractDocs serves the committed contract (embedded, byte-for-byte)
// and the docs UI on the outer mux — public, no JWT, same posture as before.
// components.yaml is served beside openapi.yaml so $ref resolution works for
// docs tooling fetching over HTTP.
func registerContractDocs(mux *http.ServeMux) {
	serveYAML := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(mustReadContract(name))
		}
	}
	mux.HandleFunc("GET /openapi.yaml", serveYAML("contract/openapi.yaml"))
	mux.HandleFunc("GET /components.yaml", serveYAML("contract/components.yaml"))
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(docsHTML))
	})
}
