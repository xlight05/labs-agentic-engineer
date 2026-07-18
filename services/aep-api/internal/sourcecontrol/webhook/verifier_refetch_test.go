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

package webhook

// UNIT tier: the verifier's cache-miss-then-refetch path and its rate-limiter
// interplay. The four core HMAC cases (accept/mismatch/malformed/empty) live in
// verifier_test.go; this file covers only the refetch behavior that closes the
// in-flight-rotation hole (§6.4) and the limiter that bounds its amplification
// (§8.4). Reuses sign() from verifier_test.go.

import (
	"context"
	"errors"
	"testing"
)

// rotatingProvider returns `cached` on a normal (Force=false) fetch and `fresh`
// on a forced (Force=true) refetch, recording each. It models the rotation
// window: the sender already signs with the new secret while the cache still
// holds the old one.
type rotatingProvider struct {
	cached, fresh []byte
	err           error
	softCount     int // Force=false calls
	forceCount    int // Force=true calls
}

func (p *rotatingProvider) Secrets(_ context.Context, _ string, opts SecretOpts) ([][]byte, error) {
	if p.err != nil {
		return nil, p.err
	}
	if opts.Force {
		p.forceCount++
		return [][]byte{p.fresh}, nil
	}
	p.softCount++
	return [][]byte{p.cached}, nil
}

func TestVerifier_RefetchOnMismatch_AcceptsRotatedSecret(t *testing.T) {
	t.Parallel()
	body := []byte(`{"action":"opened"}`)
	// Sender signs with the NEW secret; the cache still holds the OLD one.
	prov := &rotatingProvider{cached: []byte("old-secret"), fresh: []byte("new-secret")}
	v := NewVerifier(prov)

	if err := v.Verify(context.Background(), "org-acme", sign("new-secret", body), body); err != nil {
		t.Fatalf("a rotated secret must verify after the forced refetch, got %v", err)
	}
	if prov.softCount != 1 || prov.forceCount != 1 {
		t.Fatalf("expected exactly one soft then one forced fetch, got soft=%d force=%d", prov.softCount, prov.forceCount)
	}
}

func TestVerifier_RefetchOnMismatch_StillMismatchAfterRefetch(t *testing.T) {
	t.Parallel()
	body := []byte(`{}`)
	// Neither cached nor fresh matches the forged signature → mismatch even after
	// the forced refetch is spent.
	prov := &rotatingProvider{cached: []byte("old"), fresh: []byte("newer")}
	v := NewVerifier(prov)

	err := v.Verify(context.Background(), "org-acme", sign("forged", body), body)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("want ErrSignatureMismatch after a spent refetch, got %v", err)
	}
	if prov.forceCount != 1 {
		t.Fatalf("a mismatch must trigger exactly one forced refetch, got %d", prov.forceCount)
	}
}

func TestVerifier_Limiter_DeniesForcedRefetch(t *testing.T) {
	t.Parallel()
	body := []byte(`{}`)
	prov := &rotatingProvider{cached: []byte("old"), fresh: []byte("new-secret")}
	// Burst 1: the single token is consumed by the first VerifyWithKey's forced
	// refetch; the second is denied BEFORE any force fetch, so git-service is spared.
	limiter := NewRefetchLimiter(0.0001, 1)
	v := NewVerifier(prov).WithRefetchLimiter(limiter)

	// First mismatch: refetch allowed (token spent), still succeeds via `fresh`.
	if err := v.VerifyWithKey(context.Background(), "org-acme", "org-acme|ip", sign("new-secret", body), body); err != nil {
		t.Fatalf("first refetch should be allowed + succeed, got %v", err)
	}
	forceAfterFirst := prov.forceCount

	// Second mismatch on the SAME key: the bucket is empty, so the verifier must
	// short-circuit to ErrSignatureMismatch WITHOUT a further forced refetch.
	err := v.VerifyWithKey(context.Background(), "org-acme", "org-acme|ip", sign("also-forged", body), body)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("rate-limited refetch must return ErrSignatureMismatch, got %v", err)
	}
	if prov.forceCount != forceAfterFirst {
		t.Fatalf("a denied refetch must NOT call the provider again: forceCount %d → %d", forceAfterFirst, prov.forceCount)
	}
}

func TestVerifier_SecretFetchErrorSurfacesNotAsMismatch(t *testing.T) {
	t.Parallel()
	// A provider error on the first fetch is a verify error (→ 500), not a
	// signature mismatch (→ 401): the two drive different acks.
	prov := &rotatingProvider{err: errors.New("git-service down")}
	v := NewVerifier(prov)
	err := v.Verify(context.Background(), "org-acme", sign("x", []byte(`{}`)), []byte(`{}`))
	if err == nil || errors.Is(err, ErrSignatureMismatch) || errors.Is(err, ErrSignatureMalformed) {
		t.Fatalf("provider error must surface as a non-signature error, got %v", err)
	}
}
