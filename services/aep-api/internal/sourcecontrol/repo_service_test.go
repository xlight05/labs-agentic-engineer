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

package sourcecontrol_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/wso2/aep/aep-api/internal/platform/gittest"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	githubclient "github.com/wso2/aep/aep-api/internal/sourcecontrol/githubhost"
	"github.com/wso2/aep/aep-api/models"
)

func TestCreateRepo_HappyPathMarksReady(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	stub.On(http.MethodPost, "/orgs/test-org/repos", http.StatusCreated,
		`{"clone_url":"https://github.com/test-org/my-project.git"}`)

	repo := newFakeRepoRepo()
	svc := sourcecontrol.NewRepoService(repo, githubclient.NewClient(githubclient.WithAPIBase(stub.URL)), fakeResolver{}, "private")

	got, err := svc.CreateRepo(testContext(), "org1", "proj1", "My Project", "")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	// The repo is ready the moment GitHub has it — no clone, no "cloning" state.
	if got.Status != "ready" || got.DefaultBranch != "main" {
		t.Fatalf("row = {status:%q branch:%q}, want {ready main}", got.Status, got.DefaultBranch)
	}
	if got.RepoURL != "https://github.com/test-org/my-project.git" {
		t.Fatalf("row url = %q, want the GitHub clone_url", got.RepoURL)
	}

	// The create request carried the visibility + auto_init + the EXACT
	// slug-derived name — never a random suffix; the name the user saw in the
	// create form is the name the repo gets.
	req := onlyRequest(t, stub.Requests(), http.MethodPost, "/orgs/test-org/repos")
	var body struct {
		Name        string `json:"name"`
		Private     bool   `json:"private"`
		AutoInit    bool   `json:"auto_init"`
		Description string `json:"description"`
	}
	decodeBody(t, req.Body, &body)
	if !body.Private || !body.AutoInit {
		t.Fatalf("create request = %+v, want private+auto_init", body)
	}
	if body.Name != "my-project" {
		t.Fatalf("repo name = %q, want my-project exactly (no suffix)", body.Name)
	}
	if !strings.Contains(body.Description, "My Project") {
		t.Fatalf("description = %q, want it to mention the project name", body.Description)
	}
}

// Conflicts are NEVER suffixed away — for the derived name too, the create
// fails with the sentinel so the user is asked for a different name.
func TestCreateRepo_DerivedNameConflictFailsWithoutRetry(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	stub.On(http.MethodPost, "/orgs/test-org/repos", http.StatusUnprocessableEntity,
		`{"message":"name already exists on this account"}`)

	repo := newFakeRepoRepo()
	svc := sourcecontrol.NewRepoService(repo, githubclient.NewClient(githubclient.WithAPIBase(stub.URL)), fakeResolver{}, "private")

	_, err := svc.CreateRepo(testContext(), "org1", "proj1", "My Project", "")
	if !sourcecontrol.IsRepoNameConflict(err) {
		t.Fatalf("err = %v, want the ErrRepoNameConflict sentinel to survive", err)
	}
	if n := len(stub.Requests()); n != 1 {
		t.Fatalf("GitHub called %d times, want exactly 1 (no suffix retries)", n)
	}
}

func TestCreateRepo_IsIdempotentOnExistingRow(t *testing.T) {
	t.Parallel()
	repo := newFakeRepoRepo()
	existing := &models.GitRepository{OrgID: "org1", ProjectID: "proj1", RepoURL: "https://github.com/test-org/pre", Status: "ready"}
	repo.preload(existing)
	stub := gittest.NewStub(t) // no create route registered — must NOT be hit
	svc := sourcecontrol.NewRepoService(repo, githubclient.NewClient(githubclient.WithAPIBase(stub.URL)), fakeResolver{}, "private")

	got, err := svc.CreateRepo(testContext(), "org1", "proj1", "My Project", "")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if got.RepoURL != existing.RepoURL {
		t.Fatalf("returned row url = %q, want existing %q", got.RepoURL, existing.RepoURL)
	}
	if n := len(stub.Requests()); n != 0 {
		t.Fatalf("GitHub was called %d times on an idempotent re-create, want 0", n)
	}
}

func TestCreateRepo_ErrorPropagatesAndCreatesNoRow(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	stub.On(http.MethodPost, "/orgs/test-org/repos", http.StatusForbidden, `{"message":"Forbidden"}`)
	repo := newFakeRepoRepo()
	svc := sourcecontrol.NewRepoService(repo, githubclient.NewClient(githubclient.WithAPIBase(stub.URL)), fakeResolver{}, "private")

	_, err := svc.CreateRepo(testContext(), "org1", "proj1", "My Project", "")
	if err == nil || !strings.Contains(err.Error(), "create github repo") || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want a create-github-repo 403 error", err)
	}
	if r, _ := repo.GetByOrgAndProjectID(testContext(), "org1", "proj1"); r != nil {
		t.Fatalf("a repo row was created despite the GitHub failure: %+v", r)
	}
}

func TestCreateRepo_ExplicitRepoNameUsedVerbatim(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	stub.On(http.MethodPost, "/orgs/test-org/repos", http.StatusCreated,
		`{"clone_url":"https://github.com/test-org/exact-repo.git"}`)

	repo := newFakeRepoRepo()
	svc := sourcecontrol.NewRepoService(repo, githubclient.NewClient(githubclient.WithAPIBase(stub.URL)), fakeResolver{}, "private")

	got, err := svc.CreateRepo(testContext(), "org1", "proj1", "My Project", "exact-repo")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if got.RepoURL != "https://github.com/test-org/exact-repo.git" {
		t.Fatalf("row url = %q, want the exact-name clone_url", got.RepoURL)
	}

	// A user-chosen repo name is used VERBATIM — no random suffix.
	req := onlyRequest(t, stub.Requests(), http.MethodPost, "/orgs/test-org/repos")
	var body struct {
		Name string `json:"name"`
	}
	decodeBody(t, req.Body, &body)
	if body.Name != "exact-repo" {
		t.Fatalf("repo name = %q, want exact-repo verbatim", body.Name)
	}
}

func TestCreateRepo_ExplicitRepoNameConflictFailsWithoutRetry(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	stub.On(http.MethodPost, "/orgs/test-org/repos", http.StatusUnprocessableEntity,
		`{"message":"name already exists on this account"}`)

	repo := newFakeRepoRepo()
	svc := sourcecontrol.NewRepoService(repo, githubclient.NewClient(githubclient.WithAPIBase(stub.URL)), fakeResolver{}, "private")

	_, err := svc.CreateRepo(testContext(), "org1", "proj1", "My Project", "taken-repo")
	if !sourcecontrol.IsRepoNameConflict(err) {
		t.Fatalf("err = %v, want the ErrRepoNameConflict sentinel to survive", err)
	}
	// The name was chosen by the user — retrying with suffixes would betray it.
	if n := len(stub.Requests()); n != 1 {
		t.Fatalf("GitHub called %d times, want exactly 1 (no suffix retries)", n)
	}
	if r, _ := repo.GetByOrgAndProjectID(testContext(), "org1", "proj1"); r != nil {
		t.Fatalf("a repo row was created despite the conflict: %+v", r)
	}
}

func TestEnsureBareRepo_AdoptsOnNameConflict(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	stub.On(http.MethodPost, "/orgs/test-org/repos", http.StatusUnprocessableEntity,
		`{"message":"name already exists on this account"}`)
	repo := newFakeRepoRepo()
	svc := sourcecontrol.NewRepoService(repo, githubclient.NewClient(githubclient.WithAPIBase(stub.URL)), fakeResolver{}, "private")

	got, err := svc.EnsureBareRepo(testContext(), "org1", "_skills", "org-skills")
	if err != nil {
		t.Fatalf("EnsureBareRepo: %v", err)
	}
	// On conflict the URL is derived from cred.RepoOwner() + the requested name.
	if got.RepoURL != "https://github.com/test-org/org-skills" {
		t.Fatalf("adopted url = %q, want https://github.com/test-org/org-skills", got.RepoURL)
	}
	if got.Status != "ready" || got.DefaultBranch != "main" {
		t.Fatalf("adopted row = {status:%q branch:%q}, want {ready main}", got.Status, got.DefaultBranch)
	}
	if r, _ := repo.GetByOrgAndProjectID(testContext(), "org1", "_skills"); r == nil {
		t.Fatal("adopted repo row was not persisted")
	}
}
