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

package app

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	authn "github.com/wso2/aep/aep-api/internal/platform/auth"
	"github.com/wso2/aep/aep-api/models"
)

// impersonationResolver maps an OC namespace (the org handle the BFF puts in the
// request URL) to the org's UUID for the X-Impersonate-Org header on M2M OC
// calls. It is a named, tested type rather than a Build closure so the
// JWT-vs-side-car decision is unit-testable in isolation:
//
//   - JWT-first: a user-initiated request carries the caller's Thunder org UUID
//     (ouId) in the JWT. When the JWT's handle matches the namespace we're about
//     to impersonate, use ouId directly — no DB dependency, and it's the same
//     value Thunder embeds.
//   - Async paths (webhooks, watchers) have no JWT and fall through to the
//     organizations side-car, which orgensure backfills with the Thunder UUID on
//     the org's first authed request.
type impersonationResolver struct {
	sidecar orgUUIDByHandle
}

// orgUUIDByHandle is the side-car lookup port: the organizations row keyed by the
// org handle (the same value the BFF puts in OC URLs), preferring the Thunder
// UUID. orgSideCar is the raw-GORM impl; tests substitute a fake.
type orgUUIDByHandle interface {
	OrgUUIDByHandle(ctx context.Context, handle string) (string, error)
}

// Resolve is the openchoreo.Config.ImpersonateOrgResolver function.
func (r impersonationResolver) Resolve(ctx context.Context, namespace string) (string, error) {
	if claims := authn.ClaimsFromContext(ctx); claims != nil && claims.OuId != "" && authn.ResolveOuHandle(claims) == namespace {
		return claims.OuId, nil
	}
	return r.sidecar.OrgUUIDByHandle(ctx, namespace)
}

// orgSideCar reads the organizations side-car by handle. It is the raw-GORM half
// of the resolver, kept behind the orgUUIDByHandle port so the decision logic
// above tests without a database.
type orgSideCar struct{ db *gorm.DB }

func (s orgSideCar) OrgUUIDByHandle(ctx context.Context, handle string) (string, error) {
	var org models.Organization
	if err := s.db.WithContext(ctx).Where("name = ?", handle).First(&org).Error; err != nil {
		return "", fmt.Errorf("resolve impersonation org for namespace %q: %w", handle, err)
	}
	if org.ThunderOrgUUID != nil {
		return org.ThunderOrgUUID.String(), nil
	}
	return org.UUID.String(), nil
}
