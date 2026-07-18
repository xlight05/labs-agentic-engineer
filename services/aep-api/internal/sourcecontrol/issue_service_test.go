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
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/wso2/aep/aep-api/internal/platform/gittest"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	githubclient "github.com/wso2/aep/aep-api/internal/sourcecontrol/githubhost"
)

// newIssueSvcOnStub wires a REAL issueService (and REAL REST client) at the
// stub. The repo resolves (org1, proj1) → github.com/acme/widgets, so every
// GitHub call lands under /repos/acme/widgets on the stub. Tasks are plain
// GitHub issues now (Projects v2 dropped) — no board ops on creation.
func newIssueSvcOnStub(t *testing.T, stub *gittest.Stub) sourcecontrol.IssueService {
	t.Helper()
	repo := newFakeRepoRepo()
	repo.preload(&sourcecontrol.GitRepository{
		OrgID: "org1", ProjectID: "proj1",
		RepoURL: "https://github.com/acme/widgets",
	})
	return sourcecontrol.NewIssueService(repo, githubclient.NewClient(githubclient.WithAPIBase(stub.URL)), fakeResolver{})
}

func TestCreateIssue_SendsTitleBodyLabelsAndParsesResult(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	stub.On(http.MethodPost, "/repos/acme/widgets/labels", http.StatusCreated, `{}`)
	stub.On(http.MethodPost, "/repos/acme/widgets/issues", http.StatusCreated,
		`{"number":7,"html_url":"https://github.com/acme/widgets/issues/7","node_id":"NODE7"}`)
	svc := newIssueSvcOnStub(t, stub)

	res, err := svc.CreateIssue(testContext(), "org1", "proj1", sourcecontrol.CreateIssueRequest{
		Title:  "Implement auth",
		Body:   "do the thing",
		Labels: []string{"aep", "phase-1"},
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if res.Number != 7 || res.URL != "https://github.com/acme/widgets/issues/7" || res.NodeID != "NODE7" {
		t.Fatalf("result = %+v, want {7, .../issues/7, NODE7}", res)
	}

	reqs := stub.Requests()

	// Issue create request carries title/body/labels verbatim.
	issueReq := onlyRequest(t, reqs, http.MethodPost, "/repos/acme/widgets/issues")
	var body struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels"`
	}
	decodeBody(t, issueReq.Body, &body)
	if body.Title != "Implement auth" || body.Body != "do the thing" {
		t.Fatalf("issue body title/body = %q/%q", body.Title, body.Body)
	}
	if strings.Join(body.Labels, ",") != "aep,phase-1" {
		t.Fatalf("issue labels = %v, want [aep phase-1]", body.Labels)
	}

	// Both labels are ensured up-front, in order, with their mapped colors.
	labelReqs := requestsMatching(reqs, http.MethodPost, "/repos/acme/widgets/labels")
	if len(labelReqs) != 2 {
		t.Fatalf("label ensure requests = %d, want 2", len(labelReqs))
	}
	wantColors := map[string]string{"aep": "0075ca", "phase-1": "ededed"}
	for _, lr := range labelReqs {
		var l struct{ Name, Color string }
		decodeBody(t, lr.Body, &l)
		if wantColors[l.Name] != l.Color {
			t.Fatalf("label %q color = %q, want %q", l.Name, l.Color, wantColors[l.Name])
		}
		delete(wantColors, l.Name)
	}
	if len(wantColors) != 0 {
		t.Fatalf("labels not all ensured; missing %v", wantColors)
	}
}

func TestCreateIssue_TitleRequired(t *testing.T) {
	t.Parallel()
	svc := newIssueSvcOnStub(t, gittest.NewStub(t))
	if _, err := svc.CreateIssue(testContext(), "org1", "proj1", sourcecontrol.CreateIssueRequest{Title: "  "}); err == nil {
		t.Fatal("want error for blank title, got nil")
	}
}

func TestCreateIssue_RepoNotFound(t *testing.T) {
	t.Parallel()
	// A repo with no row → resolveRepoAndCredential surfaces ErrRepoNotFound.
	svc := sourcecontrol.NewIssueService(newFakeRepoRepo(), githubclient.NewClient(githubclient.WithAPIBase(gittest.NewStub(t).URL)), fakeResolver{})
	_, err := svc.CreateIssue(testContext(), "org1", "proj1", sourcecontrol.CreateIssueRequest{Title: "x"})
	if !errors.Is(err, sourcecontrol.ErrRepoNotFound) {
		t.Fatalf("err = %v, want ErrRepoNotFound", err)
	}
}

func TestCloseIssue_CommentsThenCloses(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	stub.On(http.MethodPost, "/repos/acme/widgets/issues/7/comments", http.StatusCreated, `{}`)
	stub.On(http.MethodPatch, "/repos/acme/widgets/issues/7", http.StatusOK, `{}`)
	svc := newIssueSvcOnStub(t, stub)

	if err := svc.CloseIssue(testContext(), "org1", "proj1", 7, "closing this out"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	reqs := stub.Requests()

	comment := onlyRequest(t, reqs, http.MethodPost, "/repos/acme/widgets/issues/7/comments")
	var cb struct{ Body string }
	decodeBody(t, comment.Body, &cb)
	if cb.Body != "closing this out" {
		t.Fatalf("comment body = %q", cb.Body)
	}

	patch := onlyRequest(t, reqs, http.MethodPatch, "/repos/acme/widgets/issues/7")
	var pb struct {
		State       string `json:"state"`
		StateReason string `json:"state_reason"`
	}
	decodeBody(t, patch.Body, &pb)
	if pb.State != "closed" || pb.StateReason != "completed" {
		t.Fatalf("close patch = %+v, want {closed completed}", pb)
	}
}

func TestCloseIssue_NoCommentWhenBlank(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	stub.On(http.MethodPatch, "/repos/acme/widgets/issues/7", http.StatusOK, `{}`)
	svc := newIssueSvcOnStub(t, stub)

	if err := svc.CloseIssue(testContext(), "org1", "proj1", 7, "   "); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	if got := requestsMatching(stub.Requests(), http.MethodPost, "/repos/acme/widgets/issues/7/comments"); len(got) != 0 {
		t.Fatalf("comment posted despite blank comment: %+v", got)
	}
}

func TestCommentIssue_PostsBody(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	stub.On(http.MethodPost, "/repos/acme/widgets/issues/7/comments", http.StatusCreated, `{}`)
	svc := newIssueSvcOnStub(t, stub)

	if err := svc.CommentIssue(testContext(), "org1", "proj1", 7, "a comment"); err != nil {
		t.Fatalf("CommentIssue: %v", err)
	}
	req := onlyRequest(t, stub.Requests(), http.MethodPost, "/repos/acme/widgets/issues/7/comments")
	var b struct{ Body string }
	decodeBody(t, req.Body, &b)
	if b.Body != "a comment" {
		t.Fatalf("comment body = %q", b.Body)
	}
}

func TestCommentIssue_BodyRequired(t *testing.T) {
	t.Parallel()
	svc := newIssueSvcOnStub(t, gittest.NewStub(t))
	if err := svc.CommentIssue(testContext(), "org1", "proj1", 7, "  "); err == nil {
		t.Fatal("want error for blank comment body, got nil")
	}
}

func TestListIssues_ParsesResponseAndFiltersByLabel(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	stub.On(http.MethodGet, "/repos/acme/widgets/issues", http.StatusOK,
		`[{"number":1,"title":"T1","body":"B1","html_url":"U1","state":"open","labels":[{"name":"aep"},{"name":"phase-1"}]}]`)
	svc := newIssueSvcOnStub(t, stub)

	issues, err := svc.ListIssues(testContext(), "org1", "proj1", []string{"aep"})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	got := issues[0]
	if got.Number != 1 || got.Title != "T1" || got.Body != "B1" || got.URL != "U1" || got.State != "open" {
		t.Fatalf("issue = %+v", got)
	}
	if strings.Join(got.Labels, ",") != "aep,phase-1" {
		t.Fatalf("labels = %v, want [aep phase-1]", got.Labels)
	}
	// The requested label was passed through as a query filter.
	req := onlyRequest(t, stub.Requests(), http.MethodGet, "/repos/acme/widgets/issues")
	if !strings.Contains(req.Query, "labels=aep") {
		t.Fatalf("query = %q, want it to contain labels=aep", req.Query)
	}
}

func TestEditIssueBody_PatchesBody(t *testing.T) {
	t.Parallel()
	stub := gittest.NewStub(t)
	stub.On(http.MethodPatch, "/repos/acme/widgets/issues/7", http.StatusOK, `{}`)
	svc := newIssueSvcOnStub(t, stub)

	if err := svc.EditIssueBody(testContext(), "org1", "proj1", 7, "replacement body"); err != nil {
		t.Fatalf("EditIssueBody: %v", err)
	}
	req := onlyRequest(t, stub.Requests(), http.MethodPatch, "/repos/acme/widgets/issues/7")
	var b struct{ Body string }
	decodeBody(t, req.Body, &b)
	if b.Body != "replacement body" {
		t.Fatalf("edit body = %q", b.Body)
	}
}

func TestParseOwnerRepo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in          string
		owner, repo string
		wantErr     bool
	}{
		{"https://github.com/acme/widgets", "acme", "widgets", false},
		{"https://github.com/acme/widgets.git", "acme", "widgets", false},
		{"git@github.com:acme/widgets.git", "acme", "widgets", false},
		{"https://github.com/acme/widgets/", "acme", "widgets", false},
		{"not-a-url", "", "", true},
		{"https://github.com/onlyowner", "", "", true},
	}
	for _, c := range cases {
		owner, repo, err := sourcecontrol.ParseOwnerRepo(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseOwnerRepo(%q): want error, got %s/%s", c.in, owner, repo)
			}
			continue
		}
		if err != nil || owner != c.owner || repo != c.repo {
			t.Errorf("ParseOwnerRepo(%q) = %s/%s (err %v), want %s/%s", c.in, owner, repo, err, c.owner, c.repo)
		}
	}
}
