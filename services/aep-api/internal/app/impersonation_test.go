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
	"errors"
	"testing"

	authn "github.com/wso2/aep/aep-api/internal/platform/auth"
)

type fakeSideCar struct {
	uuid   string
	err    error
	called int
}

func (f *fakeSideCar) OrgUUIDByHandle(_ context.Context, _ string) (string, error) {
	f.called++
	return f.uuid, f.err
}

// TestImpersonationResolver_JWTFastPath: when the caller's JWT carries an ouId
// whose handle matches the namespace, the resolver returns it WITHOUT touching
// the side-car (no DB dependency on the hot user path).
func TestImpersonationResolver_JWTFastPath(t *testing.T) {
	sc := &fakeSideCar{uuid: "sidecar-uuid"}
	r := impersonationResolver{sidecar: sc}

	ctx := authn.WithClaims(context.Background(), &authn.Claims{OuHandle: "acme", OuId: "jwt-uuid"})
	got, err := r.Resolve(ctx, "acme")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "jwt-uuid" {
		t.Fatalf("Resolve = %q, want the JWT ouId %q", got, "jwt-uuid")
	}
	if sc.called != 0 {
		t.Fatalf("side-car consulted %d times on the JWT fast path, want 0", sc.called)
	}
}

// TestImpersonationResolver_FallsBackToSideCar: no JWT (async path) and a
// handle-mismatched JWT both fall through to the side-car lookup.
func TestImpersonationResolver_FallsBackToSideCar(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
	}{
		{"no claims (webhook/watcher path)", context.Background()},
		{"claims present but no ouId", authn.WithClaims(context.Background(), &authn.Claims{OuHandle: "acme"})},
		{"claims for a different org", authn.WithClaims(context.Background(), &authn.Claims{OuHandle: "other", OuId: "other-uuid"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := &fakeSideCar{uuid: "sidecar-uuid"}
			r := impersonationResolver{sidecar: sc}

			got, err := r.Resolve(tc.ctx, "acme")
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != "sidecar-uuid" {
				t.Fatalf("Resolve = %q, want the side-car uuid", got)
			}
			if sc.called != 1 {
				t.Fatalf("side-car consulted %d times, want 1", sc.called)
			}
		})
	}
}

// TestImpersonationResolver_SideCarErrorPropagates: a side-car lookup failure
// surfaces to the caller (OC call then fails rather than impersonating wrong).
func TestImpersonationResolver_SideCarErrorPropagates(t *testing.T) {
	sc := &fakeSideCar{err: errors.New("no such org")}
	r := impersonationResolver{sidecar: sc}

	if _, err := r.Resolve(context.Background(), "ghost"); err == nil {
		t.Fatal("Resolve = nil error, want the side-car error propagated")
	}
}
