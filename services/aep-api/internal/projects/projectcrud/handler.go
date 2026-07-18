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

package projectcrud

import (
	"context"

	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/apierr"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
	"github.com/wso2/aep/aep-api/internal/projects"
)

// Handler serves the projects feature on the strict interface. Every operation
// is org-scoped: the deny-by-default tenant gate bound the token org into the
// context before these run, and the handler passes it to the service as an
// explicit argument.
type Handler struct{ svc *projects.Service }

// New returns the slice's handler.
func New(svc *projects.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) ListProjects(ctx context.Context, request gen.ListProjectsRequestObject) (gen.ListProjectsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	cursor, search := "", ""
	if request.Params.Cursor != "" {
		cursor = request.Params.Cursor
	}
	if request.Params.Search != "" {
		search = request.Params.Search
	}
	list, err := h.svc.ListProjects(ctx, org, 100, cursor, search)
	if err != nil {
		return nil, projects.MapProjectError(err)
	}
	return gen.ListProjects200JSONResponse(*list), nil
}

func (h *Handler) CreateProject(ctx context.Context, request gen.CreateProjectRequestObject) (gen.CreateProjectResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if request.Body.Name == "" {
		return nil, apierr.BadRequest("name is required")
	}
	// repoName becomes a GitHub repo name verbatim — enforce the slug rule
	// here so GitHub can't silently normalize it into a repo whose actual
	// name diverges from what we store.
	if request.Body.RepoName != "" {
		if err := projects.RequireSlug("repoName", request.Body.RepoName); err != nil {
			return nil, err
		}
	}
	p, err := h.svc.CreateProject(ctx, org, request.Body)
	if err != nil {
		return nil, projects.MapProjectError(err)
	}
	return gen.CreateProject201JSONResponse(*p), nil
}

func (h *Handler) GetProject(ctx context.Context, request gen.GetProjectRequestObject) (gen.GetProjectResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	p, err := h.svc.GetProject(ctx, org, request.ProjectName)
	if err != nil {
		return nil, projects.MapProjectError(err)
	}
	return gen.GetProject200JSONResponse(*p), nil
}

func (h *Handler) DeleteProject(ctx context.Context, request gen.DeleteProjectRequestObject) (gen.DeleteProjectResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := h.svc.DeleteProject(ctx, org, request.ProjectName); err != nil {
		return nil, projects.MapProjectError(err)
	}
	return gen.DeleteProject204Response{}, nil
}

func (h *Handler) GetProjectStatus(ctx context.Context, request gen.GetProjectStatusRequestObject) (gen.GetProjectStatusResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	st, err := h.svc.GetProjectStatus(ctx, org, request.ProjectName)
	if err != nil {
		return nil, projects.MapProjectError(err)
	}
	return gen.GetProjectStatus200JSONResponse(*st), nil
}
