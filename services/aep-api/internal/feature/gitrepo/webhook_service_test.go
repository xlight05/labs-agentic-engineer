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

package gitrepo_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	githubclient "github.com/wso2/aep/aep-api/internal/clients/github"
	"github.com/wso2/aep/aep-api/internal/feature/gitrepo"
	"github.com/wso2/aep/aep-api/internal/platform/gittest"
	"github.com/wso2/aep/aep-api/internal/platform/secrets"
	"github.com/wso2/aep/aep-api/models"
)

// newWebhookSvcOnStub wires a REAL webhookService with a REAL issueService (for
// the resolve helper), a REAL repoService (so SetWebhookID's lookup+persist body
// actually executes — review finding: a recording double left it uncovered), and
// the REAL REST client at the stub. Everything shares ONE fake repo store, whose
// record is how tests assert persistence. strategy sets the resolved
// credential's webhook strategy.
func newWebhookSvcOnStub(t *testing.T, stub *gittest.Stub, strategy secrets.WebhookStrategy) (gitrepo.WebhookService, *fakeRepoRepo) {
	t.Helper()
	repo := newFakeRepoRepo()
	repo.preload(&models.GitRepository{OrgID: "org1", ProjectID: "proj1", RepoURL: "https://github.com/acme/widgets"})
	resolver := fakeResolver{cred: fakeCred{strategy: strategy}}
	gh := githubclient.NewClient(githubclient.WithAPIBase(stub.URL))
	issueSvc := gitrepo.NewIssueService(repo, nil, resolver)
	repoSvc := gitrepo.NewRepoService(repo, gh, resolver, "public")

	wh := gitrepo.NewWebhookService(
		repo,
		gh,
		repoSvc,
		issueSvc,
		"https://webhook.example/deliver",
		"s3cr3t",
	)
	return wh, repo
}

// storedWebhookID reads the persisted WebhookID off the shared fake repo record.
func storedWebhookID(t *testing.T, repo *fakeRepoRepo, org, proj string) *int64 {
	t.Helper()
	rec, err := repo.GetByOrgAndProjectID(context.Background(), org, proj)
	if err != nil || rec == nil {
		t.Fatalf("repo record %s/%s: rec=%v err=%v", org, proj, rec, err)
	}
	return rec.WebhookID
}

func TestWebhookRegister_HappyPathSendsHookPayloadAndPersistsID(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	stub.On(http.MethodPost, "/repos/acme/widgets/hooks", http.StatusCreated, `{"id":12345}`)
	wh, repo := newWebhookSvcOnStub(t, stub, secrets.WebhookPerRepo)

	hookID, err := wh.Register(testContext(), "org1", "proj1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if hookID == nil || *hookID != 12345 {
		t.Fatalf("hookID = %v, want 12345", hookID)
	}

	req := onlyRequest(t, stub.Requests(), http.MethodPost, "/repos/acme/widgets/hooks")
	var body struct {
		Name   string            `json:"name"`
		Active bool              `json:"active"`
		Events []string          `json:"events"`
		Config map[string]string `json:"config"`
	}
	decodeBody(t, req.Body, &body)
	if body.Name != "web" || !body.Active {
		t.Fatalf("hook = {name:%q active:%v}, want {web true}", body.Name, body.Active)
	}
	if strings.Join(body.Events, ",") != "pull_request,push,issue_comment,issues" {
		t.Fatalf("events = %v, want [pull_request push issue_comment issues]", body.Events)
	}
	if body.Config["url"] != "https://webhook.example/deliver" || body.Config["secret"] != "s3cr3t" {
		t.Fatalf("config url/secret = %q/%q", body.Config["url"], body.Config["secret"])
	}
	if body.Config["content_type"] != "json" || body.Config["insecure_ssl"] != "0" {
		t.Fatalf("config content_type/insecure_ssl = %q/%q", body.Config["content_type"], body.Config["insecure_ssl"])
	}

	// Persistence asserted through the REAL repoService.SetWebhookID body:
	// lookup + pointer-set + repo.Update all executed against the shared store.
	if got := storedWebhookID(t, repo, "org1", "proj1"); got == nil || *got != 12345 {
		t.Fatalf("persisted WebhookID = %v, want 12345", got)
	}
}

func TestWebhookRegister_PlatformStrategyShortCircuits(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	wh, repo := newWebhookSvcOnStub(t, stub, secrets.WebhookPlatform)

	hookID, err := wh.Register(testContext(), "org1", "proj1")
	if err != nil || hookID != nil {
		t.Fatalf("Register = (%v, %v), want (nil, nil) for platform strategy", hookID, err)
	}
	if n := len(stub.Requests()); n != 0 {
		t.Fatalf("GitHub called %d times for platform strategy, want 0", n)
	}
	if got := storedWebhookID(t, repo, "org1", "proj1"); got != nil {
		t.Fatalf("WebhookID = %v for platform strategy, want unset", *got)
	}
}

func TestWebhookRegister_MissingConfigErrors(t *testing.T) {
	t.Parallel()
	issRepo := newFakeRepoRepo()
	issRepo.preload(&models.GitRepository{OrgID: "org1", ProjectID: "proj1", RepoURL: "https://github.com/acme/widgets"})
	issueSvc := gitrepo.NewIssueService(issRepo, nil, fakeResolver{})
	repoSvc := gitrepo.NewRepoService(issRepo, githubclient.NewClient(), fakeResolver{}, "public")
	// Empty delivery URL + secret.
	wh := gitrepo.NewWebhookService(issRepo, githubclient.NewClient(), repoSvc, issueSvc, "", "")

	if _, err := wh.Register(testContext(), "org1", "proj1"); err == nil {
		t.Fatal("want error for unconfigured webhook delivery URL/secret, got nil")
	}
}

func TestWebhookRegister_DedupOnHookAlreadyExists(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	// GitHub 422s a duplicate hook; the client then lists hooks and matches by URL.
	stub.On(http.MethodPost, "/repos/acme/widgets/hooks", http.StatusUnprocessableEntity, `{"message":"Hook already exists"}`)
	stub.On(http.MethodGet, "/repos/acme/widgets/hooks", http.StatusOK,
		`[{"id":99,"config":{"url":"https://webhook.example/deliver"}}]`)
	wh, repo := newWebhookSvcOnStub(t, stub, secrets.WebhookPerRepo)

	hookID, err := wh.Register(testContext(), "org1", "proj1")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if hookID == nil || *hookID != 99 {
		t.Fatalf("hookID = %v, want existing 99", hookID)
	}
	if got := storedWebhookID(t, repo, "org1", "proj1"); got == nil || *got != 99 {
		t.Fatalf("persisted WebhookID = %v, want 99", got)
	}
}

// nonResolvingIssueSvc implements IssueService (via the embedded interface) but
// NOT the private resolveRepoAndCredential helper — i.e. a test double or an
// alternate provider impl. Before the fix, webhookService type-asserted to the
// concrete *issueService and stored nil, so Register nil-dereferenced. After the
// fix it stores the interface and surfaces a clean error.
type nonResolvingIssueSvc struct{ gitrepo.IssueService }

func TestWebhookRegister_NonResolvingIssueServiceErrorsNotPanics(t *testing.T) {
	t.Parallel()
	repo := newFakeRepoRepo()
	wh := gitrepo.NewWebhookService(
		repo,
		githubclient.NewClient(),
		gitrepo.NewRepoService(repo, githubclient.NewClient(), fakeResolver{}, "public"),
		nonResolvingIssueSvc{},
		"https://webhook.example/deliver",
		"s3cr3t",
	)
	_, err := wh.Register(testContext(), "org1", "proj1")
	if err == nil || !strings.Contains(err.Error(), "cannot resolve repo credentials") {
		t.Fatalf("err = %v, want a clean 'cannot resolve repo credentials' error (not a panic)", err)
	}
}
