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

// UNIT tier: routing-key extraction (which event class maps to which
// identifier) + ResolveOcOrgID (the cache-fronted lookup that turns a routing
// key into an ocOrgId) + the RoutingCache TTL. All pure/in-process — the
// OcOrgIDLookup is a hand fake, no DB.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeLookup is a hand fake for OcOrgIDLookup that records call counts (so a
// test can prove the cache saved a lookup) and returns configured results.
type fakeLookup struct {
	byInstall  map[int64]string
	byRepo     map[string]string
	err        error
	installHit int
	repoHit    int
}

func (f *fakeLookup) OrgIDByInstallationID(_ context.Context, id int64) (string, error) {
	f.installHit++
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.byInstall[id]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (f *fakeLookup) OrgIDByRepoFullName(_ context.Context, name string) (string, error) {
	f.repoHit++
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.byRepo[name]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func TestExtractRoutingKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		event   string
		payload string
		want    RoutingKey
	}{
		{
			name:    "installation → installation.id",
			event:   "installation",
			payload: `{"installation":{"id":42}}`,
			want:    RoutingKey{Kind: "installation", InstallationID: 42},
		},
		{
			name:    "installation_repositories → installation.id",
			event:   "installation_repositories",
			payload: `{"installation":{"id":99}}`,
			want:    RoutingKey{Kind: "installation", InstallationID: 99},
		},
		{
			name:    "pull_request → repository.full_name",
			event:   "pull_request",
			payload: `{"repository":{"full_name":"acme/web"}}`,
			want:    RoutingKey{Kind: "repository", RepoFullName: "acme/web"},
		},
		{
			name:    "push → repository.full_name",
			event:   "push",
			payload: `{"repository":{"full_name":"acme/api"}}`,
			want:    RoutingKey{Kind: "repository", RepoFullName: "acme/api"},
		},
		{
			name:    "issue_comment → repository.full_name",
			event:   "issue_comment",
			payload: `{"repository":{"full_name":"acme/docs"}}`,
			want:    RoutingKey{Kind: "repository", RepoFullName: "acme/docs"},
		},
		{
			name:    "issues → repository.full_name",
			event:   "issues",
			payload: `{"repository":{"full_name":"acme/tasks"}}`,
			want:    RoutingKey{Kind: "repository", RepoFullName: "acme/tasks"},
		},
		{
			name:    "unknown event → platform (audit-only)",
			event:   "ping",
			payload: `{"zen":"…"}`,
			want:    RoutingKey{Kind: "platform"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractRoutingKey(tc.event, []byte(tc.payload))
			if got != tc.want {
				t.Fatalf("extractRoutingKey(%q) = %+v; want %+v", tc.event, got, tc.want)
			}
		})
	}
}

func TestResolveOcOrgID_Installation_LooksUpThenCaches(t *testing.T) {
	t.Parallel()
	lookup := &fakeLookup{byInstall: map[int64]string{42: "org-acme"}}
	cache := NewRoutingCache(time.Minute)
	body := []byte(`{"installation":{"id":42}}`)

	// Miss → lookup + put.
	oc, err := ResolveOcOrgID(context.Background(), lookup, cache, "installation", body)
	if err != nil || oc != "org-acme" {
		t.Fatalf("first resolve = (%q, %v); want (org-acme, nil)", oc, err)
	}
	// Hit → served from cache, lookup NOT consulted again.
	oc, err = ResolveOcOrgID(context.Background(), lookup, cache, "installation", body)
	if err != nil || oc != "org-acme" {
		t.Fatalf("second resolve = (%q, %v); want (org-acme, nil)", oc, err)
	}
	if lookup.installHit != 1 {
		t.Fatalf("cache hit must avoid a second lookup; installHit = %d, want 1", lookup.installHit)
	}
}

func TestResolveOcOrgID_Repository_LooksUpThenCaches(t *testing.T) {
	t.Parallel()
	lookup := &fakeLookup{byRepo: map[string]string{"acme/web": "org-acme"}}
	cache := NewRoutingCache(time.Minute)
	body := []byte(`{"repository":{"full_name":"acme/web"}}`)

	for i := 0; i < 3; i++ {
		oc, err := ResolveOcOrgID(context.Background(), lookup, cache, "push", body)
		if err != nil || oc != "org-acme" {
			t.Fatalf("resolve #%d = (%q, %v); want (org-acme, nil)", i, oc, err)
		}
	}
	if lookup.repoHit != 1 {
		t.Fatalf("three repo resolves behind a warm cache must hit git-service once; repoHit = %d", lookup.repoHit)
	}
}

func TestResolveOcOrgID_NoRoutingKey(t *testing.T) {
	t.Parallel()
	cache := NewRoutingCache(time.Minute)
	lookup := &fakeLookup{}
	cases := []struct {
		name    string
		event   string
		payload string
	}{
		{"installation id zero", "installation", `{"installation":{"id":0}}`},
		{"repo full_name empty", "push", `{"repository":{"full_name":""}}`},
		{"platform event", "ping", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ResolveOcOrgID(context.Background(), lookup, cache, tc.event, []byte(tc.payload))
			if !errors.Is(err, ErrNoRoutingKey) {
				t.Fatalf("want ErrNoRoutingKey, got %v", err)
			}
		})
	}
}

func TestResolveOcOrgID_LookupErrorNotCached(t *testing.T) {
	t.Parallel()
	// A lookup error (e.g. transient git-service failure) must surface AND must
	// NOT populate the cache — a later successful lookup has to be able to fill it.
	lookup := &fakeLookup{err: errors.New("git-service 503")}
	cache := NewRoutingCache(time.Minute)
	body := []byte(`{"repository":{"full_name":"acme/web"}}`)

	if _, err := ResolveOcOrgID(context.Background(), lookup, cache, "push", body); err == nil {
		t.Fatal("lookup error must surface")
	}
	if _, ok := cache.getRepo("acme/web"); ok {
		t.Fatal("a failed lookup must not populate the cache")
	}
}

func TestRoutingCache_TTLExpiry(t *testing.T) {
	t.Parallel()
	// Injected fixed clock: advance past the TTL to prove expiry — no wall-clock
	// sleep. Get returns !ok once the clock passes expireAt.
	base := time.Unix(1_700_000_000, 0)
	clock := base
	cache := NewRoutingCache(20 * time.Millisecond)
	cache.now = func() time.Time { return clock }

	cache.putInstallation(7, "org-x")
	if v, ok := cache.getInstallation(7); !ok || v != "org-x" {
		t.Fatalf("fresh entry must be a hit, got (%q, %v)", v, ok)
	}
	cache.putRepo("Acme/Web", "org-y")
	// Repo keys are case-normalised: a differently-cased read hits the same entry.
	if v, ok := cache.getRepo("acme/web"); !ok || v != "org-y" {
		t.Fatalf("repo cache must be case-insensitive, got (%q, %v)", v, ok)
	}

	clock = base.Add(40 * time.Millisecond) // advance past the 20ms TTL

	if _, ok := cache.getInstallation(7); ok {
		t.Fatal("installation entry must expire past its TTL")
	}
	if _, ok := cache.getRepo("acme/web"); ok {
		t.Fatal("repo entry must expire past its TTL")
	}
}

func TestNewRoutingCache_DefaultTTL(t *testing.T) {
	t.Parallel()
	// ttl==0 defaults to 60s; a just-put entry is a hit well inside that window.
	cache := NewRoutingCache(0)
	cache.putInstallation(1, "org-z")
	if _, ok := cache.getInstallation(1); !ok {
		t.Fatal("entry must be fresh under the default 60s TTL")
	}
}
