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
	"context"
	"errors"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/feature/gitrepo"
	"github.com/wso2/aep/aep-api/internal/feature/project"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/ocerr"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// Projects feature on the strict interface. Every operation is org-scoped:
// the deny-by-default tenant gate bound the token org into the context before
// these run, and the handlers pass it to the service as an explicit argument.

func (s *legacyHandlers) ListProjects(ctx context.Context, request gen.ListProjectsRequestObject) (gen.ListProjectsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	cursor, search := "", ""
	if request.Params.Cursor != "" {
		cursor = request.Params.Cursor
	}
	if request.Params.Search != "" {
		search = request.Params.Search
	}
	list, err := s.deps.ProjectSvc.ListProjects(ctx, org, 100, cursor, search)
	if err != nil {
		return nil, mapProjectError(err)
	}
	return gen.ListProjects200JSONResponse(*list), nil
}

func (s *legacyHandlers) CreateProject(ctx context.Context, request gen.CreateProjectRequestObject) (gen.CreateProjectResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if request.Body.Name == "" {
		return nil, errBadRequest("name is required")
	}
	// repoName becomes a GitHub repo name verbatim — enforce the slug rule
	// here so GitHub can't silently normalize it into a repo whose actual
	// name diverges from what we store.
	if request.Body.RepoName != "" {
		if err := requireSlug("repoName", request.Body.RepoName); err != nil {
			return nil, err
		}
	}
	p, err := s.deps.ProjectSvc.CreateProject(ctx, org, request.Body)
	if err != nil {
		return nil, mapProjectError(err)
	}
	return gen.CreateProject201JSONResponse(*p), nil
}

func (s *legacyHandlers) GetProject(ctx context.Context, request gen.GetProjectRequestObject) (gen.GetProjectResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	p, err := s.deps.ProjectSvc.GetProject(ctx, org, request.ProjectName)
	if err != nil {
		return nil, mapProjectError(err)
	}
	return gen.GetProject200JSONResponse(*p), nil
}

func (s *legacyHandlers) DeleteProject(ctx context.Context, request gen.DeleteProjectRequestObject) (gen.DeleteProjectResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := s.deps.ProjectSvc.DeleteProject(ctx, org, request.ProjectName); err != nil {
		return nil, mapProjectError(err)
	}
	return gen.DeleteProject204Response{}, nil
}

func (s *legacyHandlers) GetProjectStatus(ctx context.Context, request gen.GetProjectStatusRequestObject) (gen.GetProjectStatusResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	st, err := s.deps.ProjectSvc.GetProjectStatus(ctx, org, request.ProjectName)
	if err != nil {
		return nil, mapProjectError(err)
	}
	return gen.GetProjectStatus200JSONResponse(*st), nil
}

// mapProjectError translates project + OpenChoreo sentinel errors into the
// envelope. The feature sentinels (translated from OC by the service's
// translateHTTPError) carry the fixed user-facing messages; any remaining raw
// OC sentinel rides the shared ocerr classifier.
func mapProjectError(err error) error {
	switch {
	case errors.Is(err, project.ErrUnauthorized) || errors.Is(err, openchoreo.ErrUnauthorized):
		return errUnauthorized("invalid or expired token")
	case errors.Is(err, project.ErrProjectNotFound):
		return errNotFound("project not found")
	case errors.Is(err, project.ErrForbidden):
		return errForbidden("insufficient permissions to perform this action")
	case gitrepo.IsRepoNameConflict(err):
		return errConflict("a repository with this name already exists — choose another repository name")
	}
	if status, ok := ocerr.Status(err); ok {
		return errFromStatus(status, err.Error())
	}
	return errInternal("internal error")
}
