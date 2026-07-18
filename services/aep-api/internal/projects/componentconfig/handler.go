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

package componentconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/apierr"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
	"github.com/wso2/aep/aep-api/internal/projects"
	"github.com/wso2/aep/aep-api/models"
)

// Handler serves the component config feature on the strict interface. Every
// operation is org-scoped: the deny-by-default tenant gate bound the token org
// into the context before these run, and the handler passes it to the service
// as an explicit argument. projectName/componentName path params are validated
// as DNS-label slugs (400 on malformed) before any service is touched.
type Handler struct{ cfg projects.ConfigService }

// New returns the slice's handler.
func New(cfg projects.ConfigService) *Handler { return &Handler{cfg: cfg} }

// getComponentConfigNull200Response preserves the legacy 200-with-JSON-null
// body when no config row exists (a nil *ComponentConfig marshaled to null
// under the retired code-first handler; the generated value-typed 200
// response cannot express it).
type getComponentConfigNull200Response struct{}

func (getComponentConfigNull200Response) VisitGetComponentConfigResponse(w http.ResponseWriter) error {
	return writeJSONBody(w, http.StatusOK, nil) // literal null body
}

func (h *Handler) GetComponentConfig(ctx context.Context, request gen.GetComponentConfigRequestObject) (gen.GetComponentConfigResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := projects.RequireComponentSlugs(request.ProjectName, request.ComponentName); err != nil {
		return nil, err
	}
	config, err := h.cfg.GetConfig(ctx, org, request.ProjectName, request.ComponentName)
	if err != nil {
		return nil, apierr.Internal("failed to get config")
	}
	if config == nil {
		return getComponentConfigNull200Response{}, nil
	}
	return gen.GetComponentConfig200JSONResponse(*config), nil
}

func (h *Handler) UpdateComponentConfig(ctx context.Context, request gen.UpdateComponentConfigRequestObject) (gen.UpdateComponentConfigResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := projects.RequireComponentSlugs(request.ProjectName, request.ComponentName); err != nil {
		return nil, err
	}
	config, err := h.cfg.UpdateConfig(ctx, org, request.ProjectName, request.ComponentName, models.EnvVarSlice(request.Body.EnvVars))
	if err != nil {
		// Legacy mapped any update error to 400 with the error string
		// (validation: empty/duplicate keys, or repo upsert failure).
		return nil, apierr.BadRequest(err.Error())
	}
	return gen.UpdateComponentConfig200JSONResponse(*config), nil
}

// writeJSONBody is the buffered JSON writer this slice needs for the literal-null
// 200 body above. Buffered: an encode failure surfaces as an error BEFORE headers
// commit (the strict wrapper then serves its 500 envelope) instead of a
// half-written body.
func writeJSONBody(w http.ResponseWriter, status int, body any) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}
