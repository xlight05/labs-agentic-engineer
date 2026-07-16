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

package orgcreds

// UNIT tier: the HS256 connect-state bearer —
// issue→verify round-trip and every rejection branch (expiry, tampered
// signature, wrong signing key, malformed, wrong kind, missing ocOrgId, no
// key). Pure crypto/JWT, no HTTP or DB. Pinned ahead of the file split.

import (
	"strings"
	"testing"
	"time"
)

func TestBearerService_RoundTrip(t *testing.T) {
	t.Parallel()
	b := NewBearerService("state-signing-key", time.Minute)
	tok, err := b.IssueConnectState("acme", "user@x.io", 4242, 15*time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := b.VerifyConnectState(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Kind != "connect-state" {
		t.Fatalf("kind: %q", claims.Kind)
	}
	if claims.OcOrgID != "acme" || claims.Actor != "user@x.io" || claims.InstallationID != 4242 {
		t.Fatalf("claims: %+v", claims)
	}
	if claims.ExpiresAt <= claims.IssuedAt {
		t.Fatalf("exp %d must be after iat %d", claims.ExpiresAt, claims.IssuedAt)
	}
}

func TestBearerService_RoundTrip_UnpinnedInstall(t *testing.T) {
	t.Parallel()
	// installationID 0 is the initial OAuth roundtrip (not yet pinned) and must
	// verify cleanly — the code treats 0 as valid.
	b := NewBearerService("k", time.Minute)
	tok, _ := b.IssueConnectState("acme", "actor", 0, 15*time.Minute)
	claims, err := b.VerifyConnectState(tok)
	if err != nil || claims.InstallationID != 0 {
		t.Fatalf("unpinned roundtrip: err=%v install=%d", err, claims.InstallationID)
	}
}

func TestBearerService_Expired(t *testing.T) {
	t.Parallel()
	b := NewBearerService("k", time.Minute)
	// Negative TTL puts exp in the past.
	tok, err := b.IssueConnectState("acme", "actor", 0, -time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := b.VerifyConnectState(tok); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("want expired error, got %v", err)
	}
}

func TestBearerService_TamperedSignature(t *testing.T) {
	t.Parallel()
	b := NewBearerService("k", time.Minute)
	tok, _ := b.IssueConnectState("acme", "actor", 0, 15*time.Minute)
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(parts))
	}
	// Flip the final signature byte so the HMAC no longer matches.
	sig := []byte(parts[2])
	if sig[len(sig)-1] == 'A' {
		sig[len(sig)-1] = 'B'
	} else {
		sig[len(sig)-1] = 'A'
	}
	tampered := parts[0] + "." + parts[1] + "." + string(sig)
	if _, err := b.VerifyConnectState(tampered); err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Fatalf("want signature mismatch, got %v", err)
	}
}

func TestBearerService_WrongKey(t *testing.T) {
	t.Parallel()
	tok, _ := NewBearerService("key-A", time.Minute).IssueConnectState("acme", "actor", 0, 15*time.Minute)
	// A verifier with a different key must reject the HMAC.
	if _, err := NewBearerService("key-B", time.Minute).VerifyConnectState(tok); err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Fatalf("want signature mismatch across keys, got %v", err)
	}
}

func TestBearerService_Malformed(t *testing.T) {
	t.Parallel()
	b := NewBearerService("k", time.Minute)
	if _, err := b.VerifyConnectState("not-a-jwt"); err == nil || !strings.Contains(err.Error(), "malformed JWT") {
		t.Fatalf("want malformed JWT, got %v", err)
	}
}

func TestBearerService_WrongKind(t *testing.T) {
	t.Parallel()
	b := NewBearerService("k", time.Minute)
	// Hand-sign a well-formed token whose kind is NOT connect-state (e.g. a
	// leaked task bearer shape) — the kind guard must reject the substitution.
	tok, err := b.signClaims(ConnectStateClaims{
		Kind:      "task-bearer",
		OcOrgID:   "acme",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("signClaims: %v", err)
	}
	if _, err := b.VerifyConnectState(tok); err == nil || !strings.Contains(err.Error(), "wrong kind") {
		t.Fatalf("want wrong kind, got %v", err)
	}
}

func TestBearerService_MissingOcOrgID(t *testing.T) {
	t.Parallel()
	b := NewBearerService("k", time.Minute)
	tok, _ := b.signClaims(ConnectStateClaims{
		Kind:      "connect-state",
		OcOrgID:   "",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if _, err := b.VerifyConnectState(tok); err == nil || !strings.Contains(err.Error(), "missing ocOrgId") {
		t.Fatalf("want missing ocOrgId, got %v", err)
	}
}

func TestBearerService_NoSigningKey(t *testing.T) {
	t.Parallel()
	b := NewBearerService("", time.Minute)
	if _, err := b.IssueConnectState("acme", "actor", 0, time.Minute); err == nil {
		t.Fatal("issue with no key must error")
	}
	if _, err := b.VerifyConnectState("a.b.c"); err == nil {
		t.Fatal("verify with no key must error")
	}
}
