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

package spec

// UNIT tier (pure): parseDisplayIdentity is best-effort projection of a Bearer
// token's display fields for collab presence. Signature is verified upstream, so
// the token here is unsigned-then-parsed-unverified — the table drives every
// name-derivation branch (explicit name, given+family assembly, the "User"
// surname scrub, subject fallback) and the reject-on-garbage paths.

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func mkBearer(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte("irrelevant-test-key"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return "Bearer " + s
}

func TestParseDisplayIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		header    func(t *testing.T) string
		wantName  string
		wantEmail string
	}{
		{
			name:   "empty header",
			header: func(*testing.T) string { return "" },
		},
		{
			name:   "non-bearer scheme is ignored",
			header: func(t *testing.T) string { return "Basic dXNlcjpwYXNz" },
		},
		{
			name:   "malformed token yields empty identity",
			header: func(*testing.T) string { return "Bearer not-a-jwt" },
		},
		{
			name: "explicit name claim wins",
			header: func(t *testing.T) string {
				return mkBearer(t, jwt.MapClaims{"name": "Ada Lovelace", "email": "ada@x.io"})
			},
			wantName:  "Ada Lovelace",
			wantEmail: "ada@x.io",
		},
		{
			name: "given + family assembled when no name",
			header: func(t *testing.T) string {
				return mkBearer(t, jwt.MapClaims{"given_name": "Ada", "family_name": "Lovelace"})
			},
			wantName: "Ada Lovelace",
		},
		{
			name: "placeholder 'User' surname is scrubbed",
			header: func(t *testing.T) string {
				return mkBearer(t, jwt.MapClaims{"given_name": "Ada", "family_name": "User"})
			},
			wantName: "Ada",
		},
		{
			name:     "falls back to subject when no display fields",
			header:   func(t *testing.T) string { return mkBearer(t, jwt.MapClaims{"sub": "user-123"}) },
			wantName: "user-123",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			name, email := parseDisplayIdentity(tc.header(t))
			if name != tc.wantName || email != tc.wantEmail {
				t.Fatalf("parseDisplayIdentity: got (%q,%q), want (%q,%q)", name, email, tc.wantName, tc.wantEmail)
			}
		})
	}
}
