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

package listorgs

import (
	"context"
	"net/http"

	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/organization"
	"github.com/wso2/aep/aep-api/internal/platform/apierr"
	"github.com/wso2/aep/aep-api/internal/platform/ocerr"
)

// Handler serves list-organizations, the enumerated tenant-gate carve-out: it
// carries no org context — the console renders the org switcher from it before
// an org claim exists — and the service scopes itself. It still requires a user
// JWT at the outer middleware.
type Handler struct{ orgs organization.OrganizationService }

// New returns the slice's handler.
func New(orgs organization.OrganizationService) *Handler { return &Handler{orgs: orgs} }

func (h *Handler) ListOrganizations(ctx context.Context, _ gen.ListOrganizationsRequestObject) (gen.ListOrganizationsResponseObject, error) {
	list, err := h.orgs.List(ctx)
	if err != nil {
		return nil, mapOrganizationError(err)
	}
	return gen.ListOrganizations200JSONResponse(*list), nil
}

// mapOrganizationError maps the List error to its envelope. The BFF is
// read-only over OC namespaces, so the only distinction the contract makes is
// an OC 401 (surfaced as 401 so an upstream auth failure isn't masked); every
// other OC sentinel and any opaque error collapse to a fixed-message 500 that
// never echoes the internal cause.
func mapOrganizationError(err error) error {
	if status, ok := ocerr.Status(err); ok && status == http.StatusUnauthorized {
		return apierr.Unauthorized("invalid or expired token")
	}
	return apierr.Internal("failed to list organizations")
}
