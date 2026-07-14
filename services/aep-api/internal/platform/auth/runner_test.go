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

package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wso2/aep/aep-api/internal/platform/auth/jwtassertion"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// newRunnerTaskManager builds a TaskTokenManager backed by a freshly generated
// RSA key so tests can mint real BFF Task-JWTs.
func newRunnerTaskManager(t *testing.T) *TaskTokenManager {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	mgr, err := NewTaskTokenManager(TaskTokenConfig{
		PrivateKey: string(pemKey),
		Issuer:     "aep-bff",
		Audience:   "git-service",
		TTL:        time.Hour,
	})
	if err != nil {
		t.Fatalf("NewTaskTokenManager: %v", err)
	}
	return mgr
}

// statusOf extracts the HTTP status an *HTTPError carries (0 if it is not
// one).
func statusOf(err error) int {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status
	}
	return 0
}

func TestRunnerAuthorizer_TaskJWT(t *testing.T) {
	mgr := newRunnerTaskManager(t)
	// taskOrg lookup should never be hit on the Task-JWT path; fail loudly if it is.
	taskOrg := func(context.Context, string) (string, error) {
		t.Fatal("task-org lookup must not run for Task-JWT")
		return "", nil
	}
	a := NewRunnerAuthorizer(mgr, nil, taskOrg)

	tok, err := mgr.Issue("task-1", "org-a", "proj-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	t.Run("valid + matching task → ok, org bound", func(t *testing.T) {
		caller, err := a.Authorize(context.Background(), "Bearer "+tok, "task-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if caller.Org != tenant.OrgHandle("org-a") {
			t.Errorf("org = %q, want org-a", caller.Org)
		}
		if caller.Source != tenant.SourceTaskJWT {
			t.Errorf("source = %v, want SourceTaskJWT", caller.Source)
		}
		if caller.Subject != "task-1" {
			t.Errorf("subject = %q, want task-1", caller.Subject)
		}
	})

	t.Run("task mismatch → 403 (INT-6 fence)", func(t *testing.T) {
		_, err := a.Authorize(context.Background(), "Bearer "+tok, "task-2")
		if got := statusOf(err); got != 403 {
			t.Fatalf("status = %d, want 403 (err=%v)", got, err)
		}
	})
}

func TestRunnerAuthorizer_BadBearer(t *testing.T) {
	a := NewRunnerAuthorizer(newRunnerTaskManager(t), nil, func(context.Context, string) (string, error) {
		return "", nil
	})

	cases := map[string]string{
		"absent":       "",
		"wrong scheme": "Token abc.def.ghi",
		"too short":    "Bearer",
		"garbage jwt":  "Bearer not-a-real-token",
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := a.Authorize(context.Background(), header, "task-1")
			if got := statusOf(err); got != 401 {
				t.Fatalf("status = %d, want 401 (err=%v)", got, err)
			}
		})
	}
}

// newPublisherVerifier stands up an httptest JWKS server backed by a fresh RSA
// key and returns a verifier plus a mint func that signs publisher-cc tokens
// (aud = "aep-publisher-<org>", ouHandle = <ouHandle>) the verifier accepts.
const pubIssuer, pubAudPrefix = "platform-idp", "aep-publisher-"

func newPublisherVerifier(t *testing.T) (*PublisherTokenVerifier, func(org, ouHandle string) string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	const kid = "test-kid"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwtassertion.JWKS{Keys: []jwtassertion.JSONWebKey{{
			Kty: "RSA", Kid: kid, Use: "sig", Alg: "RS256",
			N: base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
			E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes()),
		}}})
	}))
	t.Cleanup(srv.Close)

	v := NewPublisherTokenVerifier(jwtassertion.NewJWKSCache(srv.URL), pubIssuer, pubAudPrefix)
	if v == nil {
		t.Fatal("NewPublisherTokenVerifier returned nil")
	}
	mint := func(org, ouHandle string) string {
		claims := PublisherClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    pubIssuer,
				Audience:  jwt.ClaimStrings{pubAudPrefix + org},
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
			OuHandle: ouHandle,
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = kid
		signed, err := tok.SignedString(priv)
		if err != nil {
			t.Fatalf("sign publisher token: %v", err)
		}
		return signed
	}
	return v, mint
}

// Publisher-cc fallback: no BFF Task-JWT, so the org-bound publisher token is
// tried second. The token's org MUST match the task's owning org (runner.go's
// cross-org fence) — org-A's token cannot refresh an org-B task it names.
func TestRunnerAuthorizer_PublisherCC(t *testing.T) {
	verifier, mint := newPublisherVerifier(t)

	t.Run("valid + task-org matches → ok, publisher source", func(t *testing.T) {
		a := NewRunnerAuthorizer(newRunnerTaskManager(t), verifier,
			func(context.Context, string) (string, error) { return "org-a", nil })
		caller, err := a.Authorize(context.Background(), "Bearer "+mint("org-a", "org-a"), "task-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if caller.Org != tenant.OrgHandle("org-a") {
			t.Errorf("org = %q, want org-a", caller.Org)
		}
		if caller.Source != tenant.SourcePublisherCC {
			t.Errorf("source = %v, want SourcePublisherCC", caller.Source)
		}
	})

	t.Run("token org ≠ task org → 403 (cross-org fence)", func(t *testing.T) {
		a := NewRunnerAuthorizer(newRunnerTaskManager(t), verifier,
			func(context.Context, string) (string, error) { return "org-b", nil })
		_, err := a.Authorize(context.Background(), "Bearer "+mint("org-a", "org-a"), "task-1")
		if got := statusOf(err); got != 403 {
			t.Fatalf("status = %d, want 403 (err=%v)", got, err)
		}
	})

	t.Run("task lookup fails → 403", func(t *testing.T) {
		a := NewRunnerAuthorizer(newRunnerTaskManager(t), verifier,
			func(context.Context, string) (string, error) { return "", errors.New("not found") })
		_, err := a.Authorize(context.Background(), "Bearer "+mint("org-a", "org-a"), "task-1")
		if got := statusOf(err); got != 403 {
			t.Fatalf("status = %d, want 403 (err=%v)", got, err)
		}
	})
}

// A Task-JWT minted for a different org still authenticates (signature is
// valid) but the INT-6 fence binds the path task to the claim, so the org is
// taken from the verified claim — never from anything the caller could spoof.
func TestRunnerAuthorizer_OrgFromClaimNotInput(t *testing.T) {
	mgr := newRunnerTaskManager(t)
	a := NewRunnerAuthorizer(mgr, nil, func(context.Context, string) (string, error) { return "", nil })
	tok, _ := mgr.Issue("task-9", "org-zed", "")
	caller, err := a.Authorize(context.Background(), "Bearer "+tok, "task-9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caller.Org != tenant.OrgHandle("org-zed") {
		t.Errorf("org = %q, want org-zed (from the signed claim)", caller.Org)
	}
}
