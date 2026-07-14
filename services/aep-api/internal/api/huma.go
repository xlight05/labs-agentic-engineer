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

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/wso2/aep/aep-api/internal/platform/humakit"
)

// apiVersion is the OpenAPI document version. Bump on contract changes.
const apiVersion = "1.0.0"

// newHumaAPI creates the Huma API over apiMux. apiMux is already wrapped by the
// JWT + orgensure middleware in NewHandler, so every Huma operation inherits
// user-JWT verification and JIT org provisioning; the per-route tenant gate is
// added by humakit.OrgScopedInput. Huma's built-in spec/docs routes stay at
// their defaults but are unreachable on apiMux (it only receives /api/*); the
// public spec + docs are served on the outer mux by registerHumaDocs.
//
// NewWithPrefix mounts every operation under humakit.APIV1: the adapter prepends
// the prefix to each served route (so apiMux answers /api/v1/...) AND auto-
// publishes it as the OpenAPI `servers` base URL, while operation Paths stay
// server-relative. That is why the version prefix appears once in the spec
// (servers) instead of in all ~58 path keys, with no per-request rewriting.
func newHumaAPI(apiMux *http.ServeMux) huma.API {
	cfg := huma.DefaultConfig("AEP BFF API", apiVersion)
	// Disable Huma's built-in spec/docs/schema routes: they would land on
	// apiMux (unreachable — it only receives /api/*), and clearing SchemasPath
	// also suppresses the $schema response-body link injection so converted
	// endpoints stay byte-compatible with the existing console. The public spec
	// + docs are served on the outer mux by registerHumaDocs.
	cfg.OpenAPIPath = ""
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	// The public edge hosts only user-JWT routes, so it declares only that
	// scheme — each surface's spec tells the truth about its own auth. The
	// runner Task-JWT / publisher-cc schemes live on the internal spec
	// (newInternalAPI); webhook HMAC + connect-state are bespoke external auth
	// not advertised in any generated spec.
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"userJWT": {Type: "http", Scheme: "bearer", BearerFormat: "JWT", Description: "Thunder end-user JWT (RS256, JWKS-verified)"},
	}
	return humago.NewWithPrefix(apiMux, humakit.APIV1, cfg)
}

// docsHTML is the Stoplight Elements docs UI, served publicly on the outer
// mux by registerContractDocs (server.go) against the committed contract.
const docsHTML = `<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    <title>AEP BFF API</title>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <script src="https://unpkg.com/@stoplight/elements/web-components.min.js"></script>
    <link rel="stylesheet" href="https://unpkg.com/@stoplight/elements/styles.min.css">
  </head>
  <body>
    <elements-api apiDescriptionUrl="/openapi.yaml" router="hash" layout="sidebar"></elements-api>
  </body>
</html>`
