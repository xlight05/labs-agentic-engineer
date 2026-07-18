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

package componentread

import (
	"context"

	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
	"github.com/wso2/aep/aep-api/internal/projects"
)

// Handler serves the component read feature on the strict interface. Every
// operation is org-scoped: the deny-by-default tenant gate bound the token org
// into the context before these run, and the handler passes it to the service
// as an explicit argument. projectName/componentName path params are validated
// as DNS-label slugs (400 on malformed) before any service (OC client / repo)
// is touched.
type Handler struct{ comp projects.ComponentService }

// New returns the slice's handler.
func New(comp projects.ComponentService) *Handler { return &Handler{comp: comp} }

func (h *Handler) ListComponents(ctx context.Context, request gen.ListComponentsRequestObject) (gen.ListComponentsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := projects.RequireSlug("projectName", request.ProjectName); err != nil {
		return nil, err
	}
	list, err := h.comp.ListComponents(ctx, org, request.ProjectName, 100, "")
	if err != nil {
		return nil, projects.MapComponentError(err, "failed to list components")
	}
	return gen.ListComponents200JSONResponse(*list), nil
}

func (h *Handler) GetComponent(ctx context.Context, request gen.GetComponentRequestObject) (gen.GetComponentResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := projects.RequireComponentSlugs(request.ProjectName, request.ComponentName); err != nil {
		return nil, err
	}
	comp, err := h.comp.GetComponent(ctx, org, request.ProjectName, request.ComponentName)
	if err != nil {
		// A missing component surfaces as openchoreo.ErrNotFound from the
		// client and is mapped to 404 by MapComponentError. (GetComponent
		// never returns the feature-local ErrComponentNotFound — only the
		// openapi handler does.)
		return nil, projects.MapComponentError(err, "failed to get component")
	}
	return gen.GetComponent200JSONResponse(*comp), nil
}
