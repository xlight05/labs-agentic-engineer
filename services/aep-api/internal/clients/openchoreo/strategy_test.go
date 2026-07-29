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

package openchoreo

import (
	"context"
	"testing"

	authn "github.com/wso2/aep/aep-api/internal/platform/auth"
	"github.com/wso2/aep/aep-api/ocauth"
)

// fixedAuthStrategy returns a constant AuthMode — test seam only.
type fixedAuthStrategy struct{ mode ocauth.AuthMode }

func (f fixedAuthStrategy) Decide(context.Context) ocauth.AuthMode { return f.mode }

// dualModeAuthStrategy mirrors today's inlined dual-mode decision for tests
// that still need PAS-style user-JWT pass-through vs service-identity M2M.
// ImpersonateOrgResolver-nil (direct-OC) is no longer part of this decision:
// that off-switch is Config.RequestAuthStrategy == nil → always M2M.
type dualModeAuthStrategy struct{}

func (dualModeAuthStrategy) Decide(ctx context.Context) ocauth.AuthMode {
	if authn.IsServiceIdentity(ctx) || authn.GetAuthToken(ctx) == "" {
		return ocauth.AuthModeServiceM2M
	}
	return ocauth.AuthModeUserJWT
}

func TestNilStrategy_IsAllM2M(t *testing.T) {
	var impersonate, auth string
	srv := captureServer(t, &impersonate, &auth)
	defer srv.Close()

	c, err := newGenClient(Config{
		BaseURL:             srv.URL,
		AuthProvider:        fakeAuthProvider{tok: "m2m-token"},
		RequestAuthStrategy: nil, // direct-OC off-switch: never pass-through
		ImpersonateOrgResolver: func(_ context.Context, ns string) (string, error) {
			if ns == "wc-abc" {
				return "org-uuid-123", nil
			}
			return "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// User JWT present + resolver set, but nil strategy must still use M2M
	// (never forward the user JWT).
	ctx := authn.WithAuthToken(context.Background(), "user-jwt")
	if _, err := c.GetNamespaceWithResponse(ctx, "wc-abc"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer m2m-token" {
		t.Errorf("nil strategy must use M2M, Authorization = %q, want Bearer m2m-token", auth)
	}
	if impersonate != "org-uuid-123" {
		t.Errorf("nil strategy M2M path must set X-Impersonate-Org, got %q", impersonate)
	}
}

func TestInjectedUserJWTStrategy_PassThrough(t *testing.T) {
	var impersonate, auth string
	srv := captureServer(t, &impersonate, &auth)
	defer srv.Close()

	resolverCalled := false
	c, err := newGenClient(Config{
		BaseURL:             srv.URL,
		AuthProvider:        fakeAuthProvider{tok: "m2m-token"},
		RequestAuthStrategy: fixedAuthStrategy{mode: ocauth.AuthModeUserJWT},
		ImpersonateOrgResolver: func(_ context.Context, _ string) (string, error) {
			resolverCalled = true
			return "org-uuid-123", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := authn.WithAuthToken(context.Background(), "user-jwt")
	if _, err := c.GetNamespaceWithResponse(ctx, "wc-abc"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer user-jwt" {
		t.Errorf("Authorization = %q, want Bearer user-jwt", auth)
	}
	if impersonate != "" {
		t.Errorf("user-JWT path must not set X-Impersonate-Org, got %q", impersonate)
	}
	if resolverCalled {
		t.Error("resolver must not be called on the user-JWT path")
	}
}

func TestInjectedM2MStrategy_UsesProviderAndResolver(t *testing.T) {
	var impersonate, auth string
	srv := captureServer(t, &impersonate, &auth)
	defer srv.Close()

	c, err := newGenClient(Config{
		BaseURL:             srv.URL,
		AuthProvider:        fakeAuthProvider{tok: "m2m-token"},
		RequestAuthStrategy: fixedAuthStrategy{mode: ocauth.AuthModeServiceM2M},
		ImpersonateOrgResolver: func(_ context.Context, ns string) (string, error) {
			if ns == "wc-abc" {
				return "org-uuid-123", nil
			}
			return "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Even with a user JWT in ctx, an injected M2M strategy must use the
	// provider token and impersonation header.
	ctx := authn.WithAuthToken(context.Background(), "user-jwt")
	if _, err := c.GetNamespaceWithResponse(ctx, "wc-abc"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer m2m-token" {
		t.Errorf("Authorization = %q, want Bearer m2m-token", auth)
	}
	if impersonate != "org-uuid-123" {
		t.Errorf("X-Impersonate-Org = %q, want org-uuid-123", impersonate)
	}
}
