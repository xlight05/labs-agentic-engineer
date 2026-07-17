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
	"net/http"

	"github.com/wso2/aep/aep-api/internal/feature/component"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/ocerr"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
	"github.com/wso2/aep/aep-api/internal/platform/validate"
	"github.com/wso2/aep/aep-api/models"
)

// Component + config features on the strict interface. Every operation is
// org-scoped: the deny-by-default tenant gate bound the token org into the
// context before these run, and the handlers pass it to the services as an
// explicit argument. projectName/componentName/buildName path params are
// validated as DNS-label slugs (400 on malformed) via the requireSlug helpers
// below — before any service (OC client / repo) is touched.

func (s *legacyHandlers) ListComponents(ctx context.Context, request gen.ListComponentsRequestObject) (gen.ListComponentsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := requireSlug("projectName", request.ProjectName); err != nil {
		return nil, err
	}
	list, err := s.deps.ComponentSvc.ListComponents(ctx, org, request.ProjectName, 100, "")
	if err != nil {
		return nil, mapComponentError(err, "failed to list components")
	}
	return gen.ListComponents200JSONResponse(*list), nil
}

func (s *legacyHandlers) GetComponent(ctx context.Context, request gen.GetComponentRequestObject) (gen.GetComponentResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := requireComponentSlugs(request.ProjectName, request.ComponentName); err != nil {
		return nil, err
	}
	comp, err := s.deps.ComponentSvc.GetComponent(ctx, org, request.ProjectName, request.ComponentName)
	if err != nil {
		// A missing component surfaces as openchoreo.ErrNotFound from the
		// client and is mapped to 404 by mapComponentError. (GetComponent
		// never returns the feature-local ErrComponentNotFound — only the
		// openapi handler below does.)
		return nil, mapComponentError(err, "failed to get component")
	}
	return gen.GetComponent200JSONResponse(*comp), nil
}

// --- Build operations --------------------------------------------------------

func (s *legacyHandlers) TriggerBuild(ctx context.Context, request gen.TriggerBuildRequestObject) (gen.TriggerBuildResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := requireComponentSlugs(request.ProjectName, request.ComponentName); err != nil {
		return nil, err
	}
	run, err := s.deps.ComponentSvc.TriggerBuild(ctx, org, request.ProjectName, request.ComponentName)
	if err != nil {
		return nil, mapComponentError(err, "failed to trigger build")
	}
	return gen.TriggerBuild201JSONResponse(*run), nil
}

func (s *legacyHandlers) ListBuilds(ctx context.Context, request gen.ListBuildsRequestObject) (gen.ListBuildsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := requireComponentSlugs(request.ProjectName, request.ComponentName); err != nil {
		return nil, err
	}
	list, err := s.deps.ComponentSvc.ListBuilds(ctx, org, request.ProjectName, request.ComponentName, 20, "")
	if err != nil {
		return nil, mapComponentError(err, "failed to list builds")
	}
	return gen.ListBuilds200JSONResponse(*list), nil
}

func (s *legacyHandlers) GetBuildLogs(ctx context.Context, request gen.GetBuildLogsRequestObject) (gen.GetBuildLogsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := requireComponentSlugs(request.ProjectName, request.ComponentName); err != nil {
		return nil, err
	}
	if err := requireSlug("buildName", request.BuildName); err != nil {
		return nil, err
	}
	logs, err := s.deps.ComponentSvc.GetBuildLogs(ctx, org, request.ProjectName, request.ComponentName, request.BuildName)
	if err != nil {
		if errors.Is(err, component.ErrLogsUnavailable) {
			return nil, errServiceUnavailable("build logs service not available")
		}
		return nil, mapComponentError(err, "failed to get build logs")
	}
	return gen.GetBuildLogs200JSONResponse(*logs), nil
}

// --- Deploy operations (read-only) --------------------------------------------
// OC's Component controller drives the deploy chain via AutoDeploy. The list
// reflects materialised ReleaseBindings for this component.

func (s *legacyHandlers) ListDeployments(ctx context.Context, request gen.ListDeploymentsRequestObject) (gen.ListDeploymentsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := requireComponentSlugs(request.ProjectName, request.ComponentName); err != nil {
		return nil, err
	}
	list, err := s.deps.ComponentSvc.ListDeployments(ctx, org, request.ProjectName, request.ComponentName)
	if err != nil {
		return nil, mapComponentError(err, "failed to list deployments")
	}
	return gen.ListDeployments200JSONResponse(*list), nil
}

// --- OpenAPI spec (drives the Test tab) ----------------------------------------
// Read from specs/design/components/<name>/openapi.yaml. Service components
// have a guaranteed OpenAPI 3.0 doc; non-service components return 409 with
// the componentType so the UI can render a typed empty state.

func (s *legacyHandlers) GetComponentOpenapi(ctx context.Context, request gen.GetComponentOpenapiRequestObject) (gen.GetComponentOpenapiResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := requireComponentSlugs(request.ProjectName, request.ComponentName); err != nil {
		return nil, err
	}
	spec, err := s.deps.ComponentSvc.GetComponentOpenAPI(ctx, org, request.ProjectName, request.ComponentName)
	if err != nil {
		if errors.Is(err, component.ErrComponentNotFound) {
			return nil, errNotFound("no OpenAPI spec for this component")
		}
		if errors.Is(err, component.ErrComponentNotService) {
			// Hand the type back (409, contract-declared) so the client can
			// say "this is a web-app, not a service". The body still carries
			// componentType. Guard nil: only the concrete service happens to
			// pair the sentinel with a non-nil spec.
			if spec == nil {
				return nil, errConflict("component does not expose an API")
			}
			return gen.GetComponentOpenapi409JSONResponse(*spec), nil
		}
		return nil, mapComponentError(err, "failed to get OpenAPI spec")
	}
	return gen.GetComponentOpenapi200JSONResponse(*spec), nil
}

// --- Config -------------------------------------------------------------------

// getComponentConfigNull200Response preserves the legacy 200-with-JSON-null
// body when no config row exists (a nil *ComponentConfig marshaled to null
// under the retired code-first handler; the generated value-typed 200
// response cannot express it).
type getComponentConfigNull200Response struct{}

func (getComponentConfigNull200Response) VisitGetComponentConfigResponse(w http.ResponseWriter) error {
	return writeJSONBody(w, http.StatusOK, nil) // literal null body
}

func (s *legacyHandlers) GetComponentConfig(ctx context.Context, request gen.GetComponentConfigRequestObject) (gen.GetComponentConfigResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := requireComponentSlugs(request.ProjectName, request.ComponentName); err != nil {
		return nil, err
	}
	config, err := s.deps.ConfigSvc.GetConfig(ctx, org, request.ProjectName, request.ComponentName)
	if err != nil {
		return nil, errInternal("failed to get config")
	}
	if config == nil {
		return getComponentConfigNull200Response{}, nil
	}
	return gen.GetComponentConfig200JSONResponse(*config), nil
}

func (s *legacyHandlers) UpdateComponentConfig(ctx context.Context, request gen.UpdateComponentConfigRequestObject) (gen.UpdateComponentConfigResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := requireComponentSlugs(request.ProjectName, request.ComponentName); err != nil {
		return nil, err
	}
	config, err := s.deps.ConfigSvc.UpdateConfig(ctx, org, request.ProjectName, request.ComponentName, models.EnvVarSlice(request.Body.EnvVars))
	if err != nil {
		// Legacy mapped any update error to 400 with the error string
		// (validation: empty/duplicate keys, or repo upsert failure).
		return nil, errBadRequest(err.Error())
	}
	return gen.UpdateComponentConfig200JSONResponse(*config), nil
}

// --- error mapping + slug guards ------------------------------------------------

// mapComponentError translates an OpenChoreo sentinel that reached
// componentService into its envelope status via the shared ocerr classifier
// (401/403/404/409/400/500, matching project and organization). An error that
// is not an OC sentinel collapses to a fixed-message 500 that never echoes the
// internal cause. Handler-specific branches (409 not-service, 503
// logs-unavailable, 404 openapi-not-found) are handled at the call site before
// delegating here.
func mapComponentError(err error, internalMsg string) error {
	if status, ok := ocerr.Status(err); ok {
		return errFromStatus(status, err.Error())
	}
	return errInternal(internalMsg)
}

// requireSlug validates a single DNS-label slug path param, returning a 400
// envelope error on failure. Delegates to validate.Slug.
func requireSlug(name, v string) error {
	if err := validate.Slug(v); err != nil {
		return errBadRequest(name + ": " + err.Error())
	}
	return nil
}

// requireComponentSlugs validates the projectName + componentName path params
// as DNS-label slugs.
func requireComponentSlugs(projectName, componentName string) error {
	if err := requireSlug("projectName", projectName); err != nil {
		return err
	}
	return requireSlug("componentName", componentName)
}
