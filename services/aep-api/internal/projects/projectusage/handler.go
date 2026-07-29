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

package projectusage

import (
	"context"

	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
	"github.com/wso2/aep/aep-api/internal/projects"
)

// Handler serves list-project-usage (#291): the org-wide Settings → Usage
// roll-up. All aggregation + labelling lives in projects.UsageService; the
// slice is pure edge wiring — resolve the bound org, delegate, respond.
type Handler struct {
	svc *projects.UsageService
}

// New returns the slice's handler.
func New(svc *projects.UsageService) *Handler { return &Handler{svc: svc} }

func (h *Handler) ListProjectUsage(ctx context.Context, _ gen.ListProjectUsageRequestObject) (gen.ListProjectUsageResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	list, err := h.svc.ListProjectUsage(ctx, org)
	if err != nil {
		return nil, projects.MapProjectError(err)
	}
	return gen.ListProjectUsage200JSONResponse(list), nil
}
