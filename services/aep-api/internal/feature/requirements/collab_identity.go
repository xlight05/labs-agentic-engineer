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

package requirements

import (
	"context"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wso2/aep/aep-api/models"
)

// repoOracle is the project-ownership port the collab handlers consult: it
// resolves the GitRepository for (orgID, projectID), so a collab request can be
// confirmed to belong to the caller's org. RegisterCollab is wired with the
// concrete repo at the composition root.
type repoOracle interface {
	GetRepo(ctx context.Context, orgID, projectID string) (*models.GitRepository, error)
}

// collabUserClaims projects the display fields off a verified user JWT. The
// signature is verified upstream (the gated route's jwt middleware / the
// validate route's jwt wrap); this only reads display fields.
type collabUserClaims struct {
	Name       string `json:"name"`
	Email      string `json:"email"`
	GivenName  string `json:"given_name"`
	FamilyName string `json:"family_name"`
	jwt.RegisteredClaims
}

// ParseDisplayIdentity is the exported entry point for the strict handlers
// (api/handlers_collab.go); it is parseDisplayIdentity verbatim. The
// unexported name stays for the retained Huma registration until its central
// deletion (issue 003).
func ParseDisplayIdentity(authHeader string) (name, email string) {
	return parseDisplayIdentity(authHeader)
}

// parseDisplayIdentity extracts a display name + email from a Bearer token for
// collab presence. Signature verification happens upstream; this is best-effort
// projection and returns empty strings on any parse failure.
func parseDisplayIdentity(authHeader string) (name, email string) {
	if authHeader == "" {
		return "", ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", ""
	}
	claims := &collabUserClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(parts[1], claims); err != nil {
		return "", ""
	}
	name = claims.Name
	if name == "" {
		given := strings.TrimSpace(claims.GivenName)
		family := strings.TrimSpace(claims.FamilyName)
		if strings.EqualFold(family, "user") {
			family = ""
		}
		name = strings.TrimSpace(given + " " + family)
	}
	if name == "" {
		name, _ = claims.GetSubject()
	}
	return name, claims.Email
}
