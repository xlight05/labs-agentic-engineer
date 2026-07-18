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

package discoveridp

import (
	"context"

	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/organization"
	"github.com/wso2/aep/aep-api/internal/platform/apierr"
)

// Handler serves discover-idp. It carries no org: the console probes an issuer
// before committing it to the idp section.
type Handler struct{ config *organization.Service }

// New returns the slice's handler.
func New(config *organization.Service) *Handler { return &Handler{config: config} }

func (h *Handler) DiscoverIdp(ctx context.Context, request gen.DiscoverIdpRequestObject) (gen.DiscoverIdpResponseObject, error) {
	issuer := ""
	if request.Params.Issuer != "" {
		issuer = request.Params.Issuer
	}
	if issuer == "" {
		return nil, apierr.BadRequest("issuer query param required")
	}
	issuerOut, jwksURL, err := h.config.DiscoverIDP(ctx, issuer)
	if err != nil {
		return nil, apierr.BadGateway(err.Error())
	}
	return gen.DiscoverIdp200JSONResponse(gen.DiscoverOutputBody{Issuer: issuerOut, JwksURL: jwksURL}), nil
}
