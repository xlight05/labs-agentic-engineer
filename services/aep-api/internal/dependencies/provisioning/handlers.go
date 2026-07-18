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

package provisioning

import (
	"context"
	"errors"

	"github.com/wso2/aep/aep-api/internal/dependencies"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/apierr"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
	"github.com/wso2/aep/aep-api/models"
)

// Handler is the dependency-provisioning slice of the strict interface: the
// external-resource catalog, value collection, platform-resource provisioning/
// status, and the cross-project org-service access-request surface. Every
// operation is org-scoped; the org from the gate serves as both the OC
// namespace/issues org and the SM-API org id. A nil service answers 503 (the
// surface exists with the feature unwired) — mirroring the pre-migration edge's
// RegisterResources/registerAccess nil guards.
type Handler struct {
	svc *Service
}

// NewHandler wires the slice over the provisioning service. A nil svc is a
// supported configuration: every op degrades to 503.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// errProvisioningUnavailable is the nil-service guard's 503.
func errProvisioningUnavailable() error {
	return apierr.ServiceUnavailable("provisioning is not configured")
}

func (h *Handler) ListExternalResources(ctx context.Context, _ gen.ListExternalResourcesRequestObject) (gen.ListExternalResourcesResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if h.svc == nil {
		return nil, errProvisioningUnavailable()
	}
	views, err := h.svc.ListExternalResources(ctx, org)
	if err != nil {
		return nil, mapProvisionError(err)
	}
	return gen.ListExternalResources200JSONResponse(toExternalResourceDTOs(views)), nil
}

func (h *Handler) DeleteExternalResource(ctx context.Context, request gen.DeleteExternalResourceRequestObject) (gen.DeleteExternalResourceResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if h.svc == nil {
		return nil, errProvisioningUnavailable()
	}
	if err := h.svc.DeleteExternalResource(ctx, org, request.Name); err != nil {
		return nil, mapProvisionError(err)
	}
	return gen.DeleteExternalResource204Response{}, nil
}

func (h *Handler) CollectExternalResourceValues(ctx context.Context, request gen.CollectExternalResourceValuesRequestObject) (gen.CollectExternalResourceValuesResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if h.svc == nil {
		return nil, errProvisioningUnavailable()
	}
	var envs map[string]map[string]string
	if request.Body != nil {
		envs = request.Body.Environments
	}
	// org serves as both the OC namespace/issues org and the SM-API org id; the
	// ctx carries the user JWT the SM-API writer reads for the vault path.
	if err := h.svc.SaveValues(ctx, org, org, request.ProjectName, request.Name, envs); err != nil {
		return nil, mapProvisionError(err)
	}
	return gen.CollectExternalResourceValues200JSONResponse(gen.StatusMsg{Status: "provisioned"}), nil
}

func (h *Handler) ProvisionPlatformResource(ctx context.Context, request gen.ProvisionPlatformResourceRequestObject) (gen.ProvisionPlatformResourceResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if h.svc == nil {
		return nil, errProvisioningUnavailable()
	}
	var params map[string]any
	var envs []string
	if request.Body != nil {
		// ProvisionBody.params is free-form in the contract (mixed scalars —
		// string, number, boolean — exactly what the service accepts).
		params = request.Body.Params
		envs = request.Body.Environments
	}
	if err := h.svc.Provision(ctx, org, request.ProjectName, request.DepName, params, envs); err != nil {
		return nil, mapProvisionError(err)
	}
	return gen.ProvisionPlatformResource202JSONResponse(gen.StatusMsg{Status: "provisioning"}), nil
}

func (h *Handler) GetDependencyStatus(ctx context.Context, request gen.GetDependencyStatusRequestObject) (gen.GetDependencyStatusResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if h.svc == nil {
		return nil, errProvisioningUnavailable()
	}
	env := ""
	if request.Params.Environment != "" {
		env = request.Params.Environment
	}
	st, err := h.svc.Status(ctx, org, request.ProjectName, request.DepName, env)
	if err != nil {
		return nil, mapProvisionError(err)
	}
	return gen.GetDependencyStatus200JSONResponse(gen.DependencyStatus{
		Status:  st.Status,
		Ready:   st.Ready,
		Outputs: st.Outputs,
	}), nil
}

func (h *Handler) RequestOrgServiceAccess(ctx context.Context, request gen.RequestOrgServiceAccessRequestObject) (gen.RequestOrgServiceAccessResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if h.svc == nil {
		return nil, errProvisioningUnavailable()
	}
	ar, err := h.svc.RequestAccess(ctx, org, request.ProjectName, request.ComponentName, request.DepName)
	if err != nil {
		return nil, mapProvisionError(err)
	}
	return gen.RequestOrgServiceAccess201JSONResponse(*ar), nil
}

func (h *Handler) ListAccessRequests(ctx context.Context, request gen.ListAccessRequestsRequestObject) (gen.ListAccessRequestsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if h.svc == nil {
		return nil, errProvisioningUnavailable()
	}
	reqs, err := h.svc.ListAccessRequests(ctx, org, request.ProjectName)
	if err != nil {
		return nil, mapProvisionError(err)
	}
	if reqs == nil {
		reqs = []models.AccessRequest{}
	}
	return gen.ListAccessRequests200JSONResponse(reqs), nil
}

// mapProvisionError translates the provisioning sentinels into the envelope:
// wrong kind → 400, not-found / not-registered → 404, in-use → 409, provision
// failure → 502, else an opaque 500. It names both the domain-root resource
// sentinels (dependencies.Err*) and this slice's own (ErrOrgServiceNotFound /
// ErrExternalResourceInUse).
func mapProvisionError(err error) error {
	switch {
	case errors.Is(err, dependencies.ErrDepWrongKind):
		return apierr.BadRequest(err.Error())
	case errors.Is(err, dependencies.ErrDepNotFound),
		errors.Is(err, dependencies.ErrNotRegistered),
		errors.Is(err, ErrOrgServiceNotFound):
		return apierr.NotFound(err.Error())
	case errors.Is(err, ErrExternalResourceInUse):
		return apierr.Conflict(err.Error())
	case errors.Is(err, dependencies.ErrProvisionFailed):
		return apierr.BadGateway(err.Error())
	}
	return apierr.Internal("provisioning failed")
}

func toExternalResourceDTOs(views []ExternalResourceView) []gen.ExternalResourceDTO {
	out := make([]gen.ExternalResourceDTO, 0, len(views))
	for _, v := range views {
		keys := make([]gen.ConfigKeyDTO, 0, len(v.Config))
		for _, k := range v.Config {
			keys = append(keys, gen.ConfigKeyDTO{Key: k.Key, Secret: k.Secret, Description: k.Description, DefaultValue: k.DefaultValue})
		}
		consumers := make([]gen.ConsumerDTO, 0, len(v.Consumers))
		for _, c := range v.Consumers {
			consumers = append(consumers, gen.ConsumerDTO{ProjectID: c.ProjectID, ComponentName: c.ComponentName})
		}
		out = append(out, gen.ExternalResourceDTO{
			Name:        v.Name,
			Description: v.Description,
			Config:      keys,
			Consumers:   consumers,
		})
	}
	return out
}
