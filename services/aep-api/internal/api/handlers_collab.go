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
	"strings"

	"github.com/wso2/aep/aep-api/internal/feature/requirements"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/auth"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// Collaboration feature on the strict interface. GetSpecCollabSession is the
// org-scoped session descriptor; ValidateCollabAccess is the server-to-server
// room authorization the collab-server calls with the forwarded user Bearer
// (+ X-Room-Id). Both read the Authorization header only for the best-effort
// display identity — the JWT signature is verified by the outer middleware.
// The project-ownership oracle (§6.6g) is deps.CollabRepo (gitrepo.RepoService).

func (s *legacyHandlers) GetSpecCollabSession(ctx context.Context, request gen.GetSpecCollabSessionRequestObject) (gen.GetSpecCollabSessionResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	// The tenant gate already bound the caller's org. Confirm the project
	// exists under it via the repo oracle — 404 otherwise.
	if repo, err := s.deps.CollabRepo.GetRepo(ctx, org, request.ProjectName); err != nil || repo == nil {
		return nil, errNotFound("project not found")
	}
	name, email := requirements.ParseDisplayIdentity(request.Params.Authorization)
	return gen.GetSpecCollabSession200JSONResponse(gen.CollabSessionOutputBody{
		RoomID:   "spec-" + org + "-" + request.ProjectName,
		WsURL:    "/collab",
		UserName: name,
		Email:    email,
	}), nil
}

// ValidateCollabAccess keeps the Huma-era S2S semantics exactly: the acting
// org is recovered from the VERIFIED token claims (never the room ID), the
// room must carry the caller-org prefix, and the room's project must resolve
// through the ownership oracle — 403 on any mismatch, never a hint of whether
// the room exists. The claims check stays even though the tenant gate 401s
// claimless requests in ENFORCE: in LOG mode the gate passes them through, and
// this handler must still refuse (the collab server relies on it).
func (s *legacyHandlers) ValidateCollabAccess(ctx context.Context, request gen.ValidateCollabAccessRequestObject) (gen.ValidateCollabAccessResponseObject, error) {
	claims := auth.ClaimsFromContext(ctx)
	org := auth.ResolveOuHandle(claims)
	if claims == nil || org == "" {
		return nil, errUnauthorized("invalid or missing token")
	}

	roomID := strings.TrimSpace(request.Params.XRoomID)
	if roomID == "" {
		return nil, errBadRequest("missing X-Room-Id")
	}
	// The room must belong to the verified caller's org.
	prefix := "spec-" + org + "-"
	if !strings.HasPrefix(roomID, prefix) {
		return nil, errForbidden("forbidden")
	}
	project := strings.TrimPrefix(roomID, prefix)
	if project == "" {
		return nil, errForbidden("forbidden")
	}
	// Project-ownership oracle: the project's repo row must exist for this org.
	if repo, err := s.deps.CollabRepo.GetRepo(ctx, org, project); err != nil || repo == nil {
		return nil, errForbidden("forbidden")
	}

	name, email := requirements.ParseDisplayIdentity(request.Params.Authorization)
	return gen.ValidateCollabAccess200JSONResponse(gen.CollabValidateOutputBody{
		Name:        name,
		Email:       email,
		ProjectName: project,
	}), nil
}
