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

import (
	"context"
	"errors"
	"testing"
	"time"
)

// UNIT tier for GitServiceSecretProvider — the TTL/cache/fallback logic in
// front of the git-service webhook-secrets fetch. The out-of-process seam
// (git-service HTTP → CredentialService.GetWebhookSecrets) is doubled by a
// counting fake SecretFetcher so cache hits vs. refetches are observable.

// countingFetcher is a fake SecretFetcher. Each Get is counted; secrets and
// err are programmable, and err can be armed only after the first successful
// fetch (to exercise the stale-cache fallback).
type countingFetcher struct {
	calls   int
	secrets [][]byte
	err     error
	// failAfter, when > 0, makes GetWebhookSecrets start returning err once
	// the call count reaches it — used to populate the cache first, then fail.
	failAfter int
}

func (f *countingFetcher) GetWebhookSecrets(_ context.Context, _ string) ([][]byte, error) {
	f.calls++
	if f.err != nil && (f.failAfter == 0 || f.calls >= f.failAfter) {
		return nil, f.err
	}
	return f.secrets, nil
}

func TestGitServiceSecretProvider_CacheHitWithinTTL(t *testing.T) {
	t.Parallel()
	f := &countingFetcher{secrets: [][]byte{[]byte("k1")}}
	// Large TTL so the two back-to-back calls are safely inside the window.
	p := NewGitServiceSecretProvider(f, time.Minute)
	ctx := context.Background()

	if _, err := p.Secrets(ctx, "org-1", SecretOpts{}); err != nil {
		t.Fatalf("first Secrets: %v", err)
	}
	got, err := p.Secrets(ctx, "org-1", SecretOpts{})
	if err != nil {
		t.Fatalf("second Secrets: %v", err)
	}
	if len(got) != 1 || string(got[0]) != "k1" {
		t.Errorf("cached secrets drifted: %q", got)
	}
	if f.calls != 1 {
		t.Errorf("second call within TTL must hit cache; fetcher calls = %d, want 1", f.calls)
	}
}

func TestGitServiceSecretProvider_RefetchAfterExpiry(t *testing.T) {
	t.Parallel()
	f := &countingFetcher{secrets: [][]byte{[]byte("k1")}}
	p := NewGitServiceSecretProvider(f, 20*time.Millisecond)
	base := time.Unix(1_700_000_000, 0)
	clock := base
	p.now = func() time.Time { return clock }
	ctx := context.Background()

	if _, err := p.Secrets(ctx, "org-1", SecretOpts{}); err != nil {
		t.Fatalf("first Secrets: %v", err)
	}
	// Advance the clock past the TTL so lookup misses and the provider refetches.
	clock = base.Add(40 * time.Millisecond)
	if _, err := p.Secrets(ctx, "org-1", SecretOpts{}); err != nil {
		t.Fatalf("post-expiry Secrets: %v", err)
	}
	if f.calls != 2 {
		t.Errorf("expired entry must refetch; fetcher calls = %d, want 2", f.calls)
	}
}

func TestGitServiceSecretProvider_ForceBypassesCache(t *testing.T) {
	t.Parallel()
	f := &countingFetcher{secrets: [][]byte{[]byte("k1")}}
	p := NewGitServiceSecretProvider(f, time.Minute)
	ctx := context.Background()

	if _, err := p.Secrets(ctx, "org-1", SecretOpts{}); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	// Force skips the (still-valid) cache and refreshes from source — the
	// path the receiver uses after an HMAC mismatch to catch in-flight rotation.
	if _, err := p.Secrets(ctx, "org-1", SecretOpts{Force: true}); err != nil {
		t.Fatalf("forced Secrets: %v", err)
	}
	if f.calls != 2 {
		t.Errorf("Force=true must bypass the cache; fetcher calls = %d, want 2", f.calls)
	}
}

func TestGitServiceSecretProvider_StaleCacheFallbackOnError(t *testing.T) {
	t.Parallel()
	// First fetch succeeds and populates the cache; the second (forced, so it
	// bypasses the cache lookup) fails — a transient git-service blip must not
	// fail the verifier, so the provider falls back to the cached secrets.
	f := &countingFetcher{
		secrets:   [][]byte{[]byte("k1")},
		err:       errors.New("git-service: 503"),
		failAfter: 2,
	}
	p := NewGitServiceSecretProvider(f, time.Minute)
	ctx := context.Background()

	if _, err := p.Secrets(ctx, "org-1", SecretOpts{}); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	got, err := p.Secrets(ctx, "org-1", SecretOpts{Force: true})
	if err != nil {
		t.Fatalf("fetch error with a warm cache must fall back, not error; got %v", err)
	}
	if len(got) != 1 || string(got[0]) != "k1" {
		t.Errorf("fallback should return the cached secrets; got %q", got)
	}
}

func TestGitServiceSecretProvider_ErrorWithNoCachePropagates(t *testing.T) {
	t.Parallel()
	// No prior successful fetch, so there's nothing to fall back to — the
	// error surfaces to the caller.
	f := &countingFetcher{err: errors.New("git-service: down")}
	p := NewGitServiceSecretProvider(f, time.Minute)
	if _, err := p.Secrets(context.Background(), "org-1", SecretOpts{}); err == nil {
		t.Fatalf("cold-cache fetch error must propagate")
	}
}

func TestGitServiceSecretProvider_InvalidateForcesRefetch(t *testing.T) {
	t.Parallel()
	f := &countingFetcher{secrets: [][]byte{[]byte("k1")}}
	p := NewGitServiceSecretProvider(f, time.Minute)
	ctx := context.Background()

	if _, err := p.Secrets(ctx, "org-1", SecretOpts{}); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	p.Invalidate("org-1")
	if _, err := p.Secrets(ctx, "org-1", SecretOpts{}); err != nil {
		t.Fatalf("post-invalidate Secrets: %v", err)
	}
	if f.calls != 2 {
		t.Errorf("Invalidate must drop the entry and force a refetch; fetcher calls = %d, want 2", f.calls)
	}
}

func TestNewGitServiceSecretProvider_DefaultsTTL(t *testing.T) {
	t.Parallel()
	// ttl==0 falls back to the 30s default (phase2.md §8.3), so a freshly
	// primed entry is a cache hit on the next call.
	f := &countingFetcher{secrets: [][]byte{[]byte("k1")}}
	p := NewGitServiceSecretProvider(f, 0)
	ctx := context.Background()
	if _, err := p.Secrets(ctx, "org-1", SecretOpts{}); err != nil {
		t.Fatalf("first Secrets: %v", err)
	}
	if _, err := p.Secrets(ctx, "org-1", SecretOpts{}); err != nil {
		t.Fatalf("second Secrets: %v", err)
	}
	if f.calls != 1 {
		t.Errorf("ttl==0 must default (not expire immediately); fetcher calls = %d, want 1", f.calls)
	}
}
