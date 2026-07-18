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

package mcpdiscovery

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/platform/secrets"
)

// ---- test doubles for the credential resolver ------------------------------

// fakeCred is a minimal secrets.Credential: a fixed token + repo owner.
type fakeCred struct {
	token string
	owner string
}

func (c fakeCred) Token(context.Context) (string, time.Time, error) { return c.token, time.Time{}, nil }
func (c fakeCred) Identity() secrets.Identity                       { return secrets.Identity{} }
func (c fakeCred) RepoOwner() string                                { return c.owner }
func (c fakeCred) WebhookStrategy() secrets.WebhookStrategy         { return secrets.WebhookPerRepo }

// fakeResolver returns cred for orgID, or err.
type fakeResolver struct {
	cred    secrets.Credential
	err     error
	lastOrg string
}

func (r *fakeResolver) Resolve(_ context.Context, ocOrgID string) (secrets.Credential, error) {
	r.lastOrg = ocOrgID
	if r.err != nil {
		return nil, r.err
	}
	return r.cred, nil
}

// newTestClient wires a RemoteGitClient at the stub's base URL with a resolver
// that maps every org to owner "acme" (token "t0k3n").
func newTestClient(t *testing.T, base string) (*RemoteGitClient, *fakeResolver) {
	t.Helper()
	res := &fakeResolver{cred: fakeCred{token: "t0k3n", owner: "acme"}}
	c := NewRemoteGitClient(res, WithRemoteGitAPIBase(base))
	return c, res
}

// ---- (a) file read → decoded content ---------------------------------------

func TestRemoteGit_GetFileContents_File(t *testing.T) {
	const raw = "openapi: 3.0.0\ninfo:\n  title: invoice\n"
	var gotAuth, gotAccept, gotPath, gotRef string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotPath = r.URL.Path
		gotRef = r.URL.Query().Get("ref")
		fmt.Fprintf(w, `{"type":"file","name":"openapi.yaml","path":"specs/openapi.yaml","sha":"abc123","content":%q,"encoding":"base64"}`,
			base64.StdEncoding.EncodeToString([]byte(raw)))
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv.URL)
	got, err := c.GetFileContents(context.Background(), "org-1", "acme", "billing-svc", "specs/openapi.yaml", "main")
	if err != nil {
		t.Fatalf("GetFileContents: %v", err)
	}
	if got.IsDirectory {
		t.Errorf("IsDirectory = true, want false")
	}
	if got.Content != raw {
		t.Errorf("Content = %q, want %q", got.Content, raw)
	}
	if got.SHA != "abc123" {
		t.Errorf("SHA = %q, want abc123", got.SHA)
	}
	if gotAuth != "Bearer t0k3n" {
		t.Errorf("Authorization = %q, want Bearer t0k3n", gotAuth)
	}
	if !strings.Contains(gotAccept, "vnd.github") {
		t.Errorf("Accept = %q, want github media type", gotAccept)
	}
	if gotPath != "/repos/acme/billing-svc/contents/specs/openapi.yaml" {
		t.Errorf("path = %q", gotPath)
	}
	if gotRef != "main" {
		t.Errorf("ref = %q, want main", gotRef)
	}
}

// ---- (b) directory path → entries ------------------------------------------

func TestRemoteGit_GetFileContents_Directory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `[
			{"type":"file","name":"openapi.yaml","path":"specs/openapi.yaml","sha":"aaa"},
			{"type":"dir","name":"schemas","path":"specs/schemas","sha":"bbb"}
		]`)
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv.URL)
	got, err := c.GetFileContents(context.Background(), "org-1", "acme", "billing-svc", "specs", "")
	if err != nil {
		t.Fatalf("GetFileContents: %v", err)
	}
	if !got.IsDirectory {
		t.Fatalf("IsDirectory = false, want true")
	}
	if got.Content != "" {
		t.Errorf("Content = %q, want empty for a directory", got.Content)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(got.Entries))
	}
	if got.Entries[0].Path != "specs/openapi.yaml" || got.Entries[0].Type != "file" {
		t.Errorf("entry[0] = %+v", got.Entries[0])
	}
	if got.Entries[1].Type != "dir" {
		t.Errorf("entry[1].Type = %q, want dir", got.Entries[1].Type)
	}
}

// ---- (c) search → matching paths -------------------------------------------

func TestRemoteGit_SearchCode(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		fmt.Fprint(w, `{"total_count":2,"items":[
			{"path":"specs/openapi.yaml","sha":"aaa"},
			{"path":"api/openapi.yaml","sha":"bbb"}
		]}`)
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv.URL)
	hits, err := c.SearchCode(context.Background(), "org-1", "acme", "billing-svc", "openapi filename:openapi.yaml")
	if err != nil {
		t.Fatalf("SearchCode: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	if hits[0].Path != "specs/openapi.yaml" || hits[0].SHA != "aaa" {
		t.Errorf("hit[0] = %+v", hits[0])
	}
	if !strings.Contains(gotQuery, "repo:acme/billing-svc") {
		t.Errorf("q = %q, want it to scope repo:acme/billing-svc", gotQuery)
	}
	if !strings.Contains(gotQuery, "openapi") {
		t.Errorf("q = %q, want it to include the caller query", gotQuery)
	}
}

// ---- (d) owner outside the caller's org is REFUSED -------------------------

func TestRemoteGit_GetFileContents_OwnerMismatchRefused(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		fmt.Fprint(w, `{"type":"file","content":"","encoding":"base64"}`)
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv.URL) // resolver owner = "acme"
	_, err := c.GetFileContents(context.Background(), "org-1", "evilcorp", "secret-repo", "x", "")
	if !errors.Is(err, ErrOwnerNotInOrg) {
		t.Fatalf("err = %v, want ErrOwnerNotInOrg", err)
	}
	if called {
		t.Fatalf("GitHub was called for a cross-org owner — the guard must refuse BEFORE any network read")
	}
}

func TestRemoteGit_SearchCode_OwnerMismatchRefused(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv.URL)
	_, err := c.SearchCode(context.Background(), "org-1", "evilcorp", "secret-repo", "openapi")
	if !errors.Is(err, ErrOwnerNotInOrg) {
		t.Fatalf("err = %v, want ErrOwnerNotInOrg", err)
	}
	if called {
		t.Fatalf("GitHub search was called for a cross-org owner — the guard must refuse first")
	}
}

// ---- content size cap ------------------------------------------------------

func TestRemoteGit_GetFileContents_ContentTooLarge(t *testing.T) {
	big := strings.Repeat("a", 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"type":"file","sha":"x","content":%q,"encoding":"base64"}`,
			base64.StdEncoding.EncodeToString([]byte(big)))
	}))
	defer srv.Close()

	res := &fakeResolver{cred: fakeCred{token: "t", owner: "acme"}}
	c := NewRemoteGitClient(res, WithRemoteGitAPIBase(srv.URL), WithRemoteGitMaxContentBytes(1024))
	_, err := c.GetFileContents(context.Background(), "org-1", "acme", "billing-svc", "big.yaml", "")
	if err == nil {
		t.Fatalf("expected an error for content over the cap")
	}
}

// ---- GitHub "too large to inline" (encoding=none) is refused, not silent --

func TestRemoteGit_GetFileContents_EncodingNoneRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// GitHub's Contents API returns this shape for a file it cannot inline
		// (roughly 1-100 MiB): content is empty and encoding is "none".
		fmt.Fprint(w, `{"type":"file","sha":"toolarge","content":"","encoding":"none"}`)
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv.URL)
	_, err := c.GetFileContents(context.Background(), "org-1", "acme", "billing-svc", "huge.bin", "")
	if !errors.Is(err, ErrFileTooLargeToInline) {
		t.Fatalf("err = %v, want ErrFileTooLargeToInline", err)
	}
}

func TestRemoteGit_GetFileContents_GenuinelyEmptyFileStillOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A real empty file: encoding is still "base64", content is "".
		fmt.Fprint(w, `{"type":"file","sha":"empty","content":"","encoding":"base64"}`)
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv.URL)
	got, err := c.GetFileContents(context.Background(), "org-1", "acme", "billing-svc", "empty.txt", "")
	if err != nil {
		t.Fatalf("GetFileContents: %v", err)
	}
	if got.Content != "" {
		t.Errorf("Content = %q, want empty string for a genuinely empty file", got.Content)
	}
}

// ---- SearchCode rejects an agent-embedded scope qualifier ------------------

func TestRemoteGit_SearchCode_EmbeddedRepoQualifierRefused(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv.URL)
	_, err := c.SearchCode(context.Background(), "org-1", "acme", "billing-svc", "secret repo:acme/other-private")
	if !errors.Is(err, ErrQueryScopeQualifier) {
		t.Fatalf("err = %v, want ErrQueryScopeQualifier", err)
	}
	if called {
		t.Fatalf("GitHub search was called for a query embedding its own repo: qualifier — the sanitizer must refuse before any network read")
	}
}

func TestRemoteGit_SearchCode_EmbeddedOrgQualifierRefused(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()

	c, _ := newTestClient(t, srv.URL)
	_, err := c.SearchCode(context.Background(), "org-1", "acme", "billing-svc", "secret org:evilcorp")
	if !errors.Is(err, ErrQueryScopeQualifier) {
		t.Fatalf("err = %v, want ErrQueryScopeQualifier", err)
	}
	if called {
		t.Fatalf("GitHub search was called for a query embedding its own org: qualifier — the sanitizer must refuse before any network read")
	}
}
