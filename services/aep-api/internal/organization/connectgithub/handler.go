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

package connectgithub

import (
	"context"
	"errors"

	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/organization"
	"github.com/wso2/aep/aep-api/internal/platform/apierr"
	"github.com/wso2/aep/aep-api/internal/platform/auth"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// Handler serves start-git-provider-connect.
type Handler struct{ config *organization.Service }

// New returns the slice's handler.
func New(config *organization.Service) *Handler { return &Handler{config: config} }

func (h *Handler) StartGitProviderConnect(ctx context.Context, request gen.StartGitProviderConnectRequestObject) (gen.StartGitProviderConnectResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	actor := auth.ActorFromContext(ctx)
	var installationID int64
	if request.Body != nil {
		installationID = request.Body.InstallationID
	}
	authorizeURL, err := h.config.StartGitHubConnect(ctx, org, actor, installationID)
	if err != nil {
		if errors.Is(err, organization.ErrGitHubAppNotConfigured) {
			return nil, apierr.ServiceUnavailable("github app oauth client not configured")
		}
		return nil, apierr.Internal("could not start connect")
	}
	return gen.StartGitProviderConnect200JSONResponse(gen.StartConnectOutputBody{AuthorizeURL: authorizeURL}), nil
}
