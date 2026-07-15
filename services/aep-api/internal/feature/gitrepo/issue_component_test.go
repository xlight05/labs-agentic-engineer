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

// COMPONENT tier: the issue create/search surface behind the real handler
// chain. This surface backs the SRE/RCA alert handoff via the deployed
// aep-mcp-server (AE-HANDOFF-DESIGN.md) — it was accidentally dropped at the
// contract-first cutover and restored; these tests pin it to the contract so
// it cannot silently vanish again. The wire quirk matters: list items use
// CAPITALIZED keys (Number/Title/…), create's result lowercase — exactly what
// the MCP client parses.
//
// External test package: the harness imports api, which imports gitrepo.
package gitrepo_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wso2/aep/aep-api/internal/api"
	"github.com/wso2/aep/aep-api/internal/feature/gitrepo"
	"github.com/wso2/aep/aep-api/internal/platform/componenttest"
)

// fakeIssueService is a minimal gitrepo.IssueService: create echoes, list
// returns a fixed set for the ranker to filter. The embedded interface panics
// on any other method — these routes must never reach them.
type fakeIssueService struct {
	gitrepo.IssueService
	created []gitrepo.CreateIssueRequest
	gotOrg  string
	issues  []gitrepo.IssueInfo
}

func (f *fakeIssueService) CreateIssue(_ context.Context, org, _ string, req gitrepo.CreateIssueRequest) (*gitrepo.IssueResult, error) {
	f.gotOrg = org
	f.created = append(f.created, req)
	return &gitrepo.IssueResult{Number: 7, URL: "https://github.com/acme/repo/issues/7", NodeID: "n7"}, nil
}

func (f *fakeIssueService) ListIssues(_ context.Context, org, _ string, _ []string) ([]gitrepo.IssueInfo, error) {
	f.gotOrg = org
	return f.issues, nil
}

func TestIssueComponent_CreateAndList(t *testing.T) {
	t.Parallel()
	svc := &fakeIssueService{issues: []gitrepo.IssueInfo{
		{Number: 1, Title: "service1 timeout on checkout", Body: "…", URL: "u1", State: "open", Labels: []string{"sre"}},
		{Number: 2, Title: "docs typo", Body: "…", URL: "u2", State: "open"},
	}}
	h := componenttest.New(t, componenttest.Options{Deps: api.Deps{IssueSvc: svc}})

	// Create: org from the verified token, result keys lowercase.
	resp := h.AsOrg("acme").Post("/api/v1/projects/web/issues",
		`{"title":"pod oomkilled","body":"details","labels":["sre"],"dedupeKey":"sre-rca/web"}`)
	if resp.Code != 200 {
		t.Fatalf("create: want 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	if svc.gotOrg != "acme" || len(svc.created) != 1 || svc.created[0].DedupeKey != "sre-rca/web" {
		t.Fatalf("service saw org=%q created=%+v", svc.gotOrg, svc.created)
	}
	var created map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("create body: %v", err)
	}
	for _, k := range []string{"number", "url", "nodeId"} {
		if _, ok := created[k]; !ok {
			t.Fatalf("create body missing %q (lowercase keys are the MCP-consumed wire): %s", k, resp.Body.String())
		}
	}

	// List with a ranked query: CAPITALIZED item keys (the MCP-consumed wire).
	resp = h.AsOrg("acme").Get("/api/v1/projects/web/issues?q=service1%20timeout")
	if resp.Code != 200 {
		t.Fatalf("list: want 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body.Bytes(), &items); err != nil {
		t.Fatalf("list body: %v", err)
	}
	if len(items) != 1 || !strings.Contains(string(items[0]["Title"]), "service1") {
		t.Fatalf("ranked list: got %s", resp.Body.String())
	}
	for _, k := range []string{"Number", "Title", "Body", "URL", "State", "Labels"} {
		if _, ok := items[0][k]; !ok {
			t.Fatalf("list item missing capitalized %q: %s", k, resp.Body.String())
		}
	}

	// Validation: missing required body fields → contract validator's 400.
	resp = h.AsOrg("acme").Post("/api/v1/projects/web/issues", `{}`)
	if resp.Code != 400 {
		t.Fatalf("empty create: want 400, got %d", resp.Code)
	}
	if e := componenttest.DecodeEnvelope(t, resp.Body.String()); e.Code != "validation_failed" {
		t.Fatalf("validation envelope: %s", resp.Body.String())
	}

	// Deny-by-default gate: claimless request is 401.
	if resp := h.NoAuth().Get("/api/v1/projects/web/issues"); resp.Code != 401 {
		t.Fatalf("claimless list: want 401, got %d", resp.Code)
	}

	// Nil service → 503 service_unavailable, not a panic.
	h2 := componenttest.New(t, componenttest.Options{Deps: api.Deps{}})
	if resp := h2.AsOrg("acme").Get("/api/v1/projects/web/issues"); resp.Code != 503 {
		t.Fatalf("nil svc: want 503, got %d body=%s", resp.Code, resp.Body.String())
	}
}
