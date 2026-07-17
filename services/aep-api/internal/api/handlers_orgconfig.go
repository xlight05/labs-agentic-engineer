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

	"github.com/wso2/aep/aep-api/internal/feature/idp"
	"github.com/wso2/aep/aep-api/internal/feature/orgconfig"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/auth"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// Org-config feature on the strict interface: the GET/PATCH /config singleton
// plus the four action routes. The PATCH body is models.ConfigPatch, whose
// sections are three-state patch.Field values — absent = keep, null = clear,
// value = replace — decoded by Field.UnmarshalJSON exactly as before (the
// schemas are hand-written in models/org_config.go, excluded from codegen).
// All handler logic lives in orgconfig.Service; this file only maps HTTP <->
// domain and translates SectionError into section-pointered envelopes.

func (s *legacyHandlers) GetConfig(ctx context.Context, _ gen.GetConfigRequestObject) (gen.GetConfigResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	proj, err := s.deps.OrgConfigSvc.Get(ctx, org)
	if err != nil {
		return nil, errInternal("failed to load config")
	}
	return gen.GetConfig200JSONResponse(*proj), nil
}

func (s *legacyHandlers) UpdateConfig(ctx context.Context, request gen.UpdateConfigRequestObject) (gen.UpdateConfigResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	actor := auth.ActorFromContext(ctx)
	proj, err := s.deps.OrgConfigSvc.Patch(ctx, org, actor, *request.Body)
	if err != nil {
		return nil, mapPatchError(err)
	}
	return gen.UpdateConfig200JSONResponse(*proj), nil
}

func (s *legacyHandlers) StartGitProviderConnect(ctx context.Context, request gen.StartGitProviderConnectRequestObject) (gen.StartGitProviderConnectResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	actor := auth.ActorFromContext(ctx)
	var installationID int64
	if request.Body != nil {
		installationID = request.Body.InstallationID
	}
	authorizeURL, err := s.deps.OrgConfigSvc.StartGitHubConnect(ctx, org, actor, installationID)
	if err != nil {
		if errors.Is(err, orgconfig.ErrGitHubAppNotConfigured) {
			return nil, errServiceUnavailable("github app oauth client not configured")
		}
		return nil, errInternal("could not start connect")
	}
	return gen.StartGitProviderConnect200JSONResponse(gen.StartConnectOutputBody{AuthorizeURL: authorizeURL}), nil
}

func (s *legacyHandlers) DisconnectGitProvider(ctx context.Context, request gen.DisconnectGitProviderRequestObject) (gen.DisconnectGitProviderResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	// App-mode only: when false, leave the install on GitHub for later
	// re-adoption. Defaults true (contract default, previously a Huma default).
	uninstall := true
	if request.Params.Uninstall != nil {
		uninstall = *request.Params.Uninstall
	}
	connected, err := s.deps.OrgConfigSvc.DisconnectGitProvider(ctx, org, uninstall)
	if err != nil {
		return nil, errInternal("disconnect failed")
	}
	status := "not_connected"
	if connected {
		status = "disconnected"
	}
	return gen.DisconnectGitProvider200JSONResponse(gen.DisconnectOutputBody{Status: status}), nil
}

func (s *legacyHandlers) RotateIdpClientSecret(ctx context.Context, _ gen.RotateIdpClientSecretRequestObject) (gen.RotateIdpClientSecretResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	actor := auth.ActorFromContext(ctx)
	newSecret, err := s.deps.OrgConfigSvc.RotateIDPClientSecret(ctx, org, actor)
	if err != nil {
		if errors.Is(err, idp.ErrIDPThunderUnavailable) {
			return nil, errServiceUnavailable("Thunder admin client not configured")
		}
		return nil, errInternal("failed to regenerate client secret")
	}
	return gen.RotateIdpClientSecret200JSONResponse(gen.ClientSecretOutputBody{ClientSecret: newSecret}), nil
}

func (s *legacyHandlers) DiscoverIdp(ctx context.Context, request gen.DiscoverIdpRequestObject) (gen.DiscoverIdpResponseObject, error) {
	issuer := ""
	if request.Params.Issuer != "" {
		issuer = request.Params.Issuer
	}
	if issuer == "" {
		return nil, errBadRequest("issuer query param required")
	}
	issuerOut, jwksURL, err := s.deps.OrgConfigSvc.DiscoverIDP(ctx, issuer)
	if err != nil {
		return nil, errBadGateway(err.Error())
	}
	return gen.DiscoverIdp200JSONResponse(gen.DiscoverOutputBody{Issuer: issuerOut, JwksURL: jwksURL}), nil
}

// mapPatchError turns a PATCH failure into the envelope. A SectionError
// carries the offending section, so the response includes a body.<section>
// field the console uses to highlight that form section; anything else
// collapses to an opaque 500 that never echoes the internal cause. Probe
// rejections that were 422 under the problem-details dialect are 400 now
// (the error-model break).
func mapPatchError(err error) error {
	var se *orgconfig.SectionError
	if errors.As(err, &se) {
		details := []gen.ErrorDetail{{Field: "body." + se.Section, Message: se.Message}}
		switch se.Status {
		case http.StatusConflict:
			return &apiError{http.StatusConflict, CodeConflict, se.Message, details}
		case http.StatusBadGateway:
			return &apiError{http.StatusBadGateway, CodeBadGateway, se.Message, details}
		default:
			return &apiError{http.StatusBadRequest, CodeValidationFailed, se.Message, details}
		}
	}
	return errInternal("internal error")
}
