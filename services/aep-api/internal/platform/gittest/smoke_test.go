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

package gittest

// Smoke tests proving the harness end to end: clone/seed/tag over the file://
// remote, and the Stub route registry. (The Git-Data HTTP fake and its wire
// round-trip tests were retired with the REST git-object path — the gitfs
// Workspace engine is exercised by internal/platform/gitfs's own suite.)

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// --- Remote: clone / seed / tag ---------------------------------------------

func TestRemote_CloneReflectsSeed(t *testing.T) {
	t.Parallel()
	r := NewRemote(t, WithSeed(map[string]string{
		"specs/requirements/main.md": "# hello",
	}, "seed"))

	clone := r.Clone(t)
	got, err := os.ReadFile(filepath.Join(clone, "specs", "requirements", "main.md"))
	if err != nil {
		t.Fatalf("read cloned file: %v", err)
	}
	if string(got) != "# hello" {
		t.Fatalf("cloned content = %q, want %q", got, "# hello")
	}
}

func TestRemote_SeedAndTagVisibleAfterFetch(t *testing.T) {
	t.Parallel()
	r := NewRemote(t, WithSeed(map[string]string{"README.md": "root"}, "seed"))

	newSHA := r.Seed(t, map[string]string{"specs/requirements/main.md": "reqs"}, "add reqs")
	r.Tag(t, "v1", "Requirements v1")

	if head := r.HeadSHA(t); head != newSHA {
		t.Fatalf("HeadSHA = %s, want seeded %s", head, newSHA)
	}
	if tags := r.Tags(t); len(tags) != 1 || tags[0] != "v1" {
		t.Fatalf("Tags = %v, want [v1]", tags)
	}
	if content := r.FileAt(t, "main", "specs/requirements/main.md"); content != "reqs" {
		t.Fatalf("FileAt(main) = %q, want %q", content, "reqs")
	}

	// A fresh clone sees the seeded commit and tag — i.e. they really landed in
	// the bare repo and propagate over the file:// protocol (as `git fetch`
	// would).
	clone := r.Clone(t)
	if _, err := os.Stat(filepath.Join(clone, "specs", "requirements", "main.md")); err != nil {
		t.Fatalf("seeded file missing in clone: %v", err)
	}
	tagsOut, err := runGit(clone, nil, nil, "tag", "-l")
	if err != nil {
		t.Fatalf("git tag -l in clone: %v", err)
	}
	if got := bytes.TrimSpace([]byte(tagsOut)); string(got) != "v1" {
		t.Fatalf("clone tags = %q, want v1", got)
	}
}

func TestRemote_RemoveDeletesPaths(t *testing.T) {
	t.Parallel()
	r := NewRemote(t, WithSeed(map[string]string{
		"specs/requirements/main.md": "keep",
		"specs/requirements/gone.md": "delete me",
	}, "seed"))

	r.Remove(t, "rm gone.md", "specs/requirements/gone.md")

	if _, err := r.exec(nil, nil, "cat-file", "blob", "main:specs/requirements/gone.md"); err == nil {
		t.Fatal("gone.md still present after Remove")
	}
	if got := r.FileAt(t, "main", "specs/requirements/main.md"); got != "keep" {
		t.Fatalf("survivor main.md = %q, want %q", got, "keep")
	}
}

// --- Stub: route registry ---------------------------------------------------

func TestStub_RegistersAndRecords(t *testing.T) {
	t.Parallel()
	s := NewStub(t)
	s.On(http.MethodPost, "/repos/acme/widgets/issues", http.StatusCreated, `{"number":7}`)

	// Registered route returns the configured status+body.
	resp, err := http.Post(s.URL+"/repos/acme/widgets/issues", "application/json", bytes.NewReader([]byte(`{"title":"bug"}`)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || string(body) != `{"number":7}` {
		t.Fatalf("registered route = %d %q, want 201 {\"number\":7}", resp.StatusCode, body)
	}

	// Unregistered route 404s.
	resp2, err := http.Get(s.URL + "/repos/acme/widgets/hooks")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("unregistered route = %d, want 404", resp2.StatusCode)
	}

	// Both requests were recorded, in order, with method/path/body.
	reqs := s.Requests()
	if len(reqs) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(reqs))
	}
	if reqs[0].Method != http.MethodPost || reqs[0].Path != "/repos/acme/widgets/issues" || reqs[0].Body != `{"title":"bug"}` {
		t.Fatalf("request[0] = %+v", reqs[0])
	}
	if reqs[1].Path != "/repos/acme/widgets/hooks" {
		t.Fatalf("request[1] path = %q, want /repos/acme/widgets/hooks", reqs[1].Path)
	}
}

// TestStub_OnSequenceScriptsSuccessiveReplies pins the create-then-recover
// shape: one route answers differently before and after an intervening side
// effect, and calls past the end of the script repeat the last reply.
func TestStub_OnSequenceScriptsSuccessiveReplies(t *testing.T) {
	t.Parallel()
	s := NewStub(t)
	s.OnSequence(http.MethodGet, "/repos/acme/widgets/milestones",
		Response{Status: http.StatusOK, Body: `[]`},
		Response{Status: http.StatusOK, Body: `[{"number":4,"title":"v1"}]`},
	)

	want := []struct {
		status int
		body   string
	}{
		{http.StatusOK, `[]`},
		{http.StatusOK, `[{"number":4,"title":"v1"}]`},
		{http.StatusOK, `[{"number":4,"title":"v1"}]`}, // past the end: last reply repeats
	}
	for i, w := range want {
		status, body := get(t, s.URL+"/repos/acme/widgets/milestones")
		if status != w.status || body != w.body {
			t.Fatalf("call %d = %d %q, want %d %q", i+1, status, body, w.status, w.body)
		}
	}
	if got := len(s.Requests()); got != 3 {
		t.Fatalf("recorded %d requests, want 3", got)
	}
}

// TestStub_OnFuncSeesTheRequest proves a dynamic route can key its reply off
// the request — the pagination and single-/graphql-route cases.
func TestStub_OnFuncSeesTheRequest(t *testing.T) {
	t.Parallel()
	s := NewStub(t)
	s.OnFunc(http.MethodGet, "/repos/acme/widgets/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Query().Get("page") == "2" {
			_, _ = io.WriteString(w, `[{"number":2}]`)
			return
		}
		_, _ = io.WriteString(w, `[{"number":1}]`)
	})

	if _, body := get(t, s.URL+"/repos/acme/widgets/issues?page=1"); body != `[{"number":1}]` {
		t.Fatalf("page 1 body = %q", body)
	}
	if _, body := get(t, s.URL+"/repos/acme/widgets/issues?page=2"); body != `[{"number":2}]` {
		t.Fatalf("page 2 body = %q", body)
	}
	// Query strings are captured on the record even though routing ignores them.
	if q := s.Requests()[1].Query; q != "page=2" {
		t.Fatalf("recorded query = %q, want page=2", q)
	}
}

// TestStub_OnReplacesAnEarlierRegistration pins the documented last-one-wins
// rule across registration forms.
func TestStub_OnReplacesAnEarlierRegistration(t *testing.T) {
	t.Parallel()
	s := NewStub(t)
	s.OnSequence(http.MethodGet, "/x", Response{Status: http.StatusOK, Body: `first`})
	s.On(http.MethodGet, "/x", http.StatusTeapot, `second`)

	if status, body := get(t, s.URL+"/x"); status != http.StatusTeapot || body != "second" {
		t.Fatalf("after replacement = %d %q, want 418 \"second\"", status, body)
	}
}

// get performs a GET and returns the status and body as a string.
func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(body)
}
