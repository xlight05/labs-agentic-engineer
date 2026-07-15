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

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/config"
	"github.com/wso2/aep/aep-api/internal/platform/auth"
	"github.com/wso2/aep/aep-api/models"
)

// Component test for the mounted MCP discovery surface: the real outer mux
// (NewHandler → mountSurfaces), the real AgentsScopedVerifier over a real
// TaskTokenManager, and the real MCP handler over a fake external-resource
// port. Proves the full caller flow: mint a token with the concrete signer
// (IssueMCPToken) → initialize → tools/list → tools/call, plus the two edge
// negatives (no token; org bound from the claim, not the request).

// mcpTestReader is a fake dependencies.ExternalResourceReader recording the
// org each call was scoped to.
type mcpTestReader struct {
	items   []models.ExternalResource
	lastOrg string
}

func (f *mcpTestReader) List(_ context.Context, orgID string) ([]models.ExternalResource, error) {
	f.lastOrg = orgID
	return f.items, nil
}

func (f *mcpTestReader) Get(_ context.Context, orgID, name string) (*models.ExternalResource, error) {
	f.lastOrg = orgID
	for i := range f.items {
		if f.items[i].Name == name {
			return &f.items[i], nil
		}
	}
	return nil, nil
}

// newMCPTestServer builds the full handler with the MCP surface wired and
// returns the server, the token manager (the concrete signer), and the fake
// reader.
func newMCPTestServer(t *testing.T) (*httptest.Server, *auth.TaskTokenManager, *mcpTestReader) {
	t.Helper()
	priv := mustGenerateRSAKey(t)
	mgr, err := auth.NewTaskTokenManager(auth.TaskTokenConfig{
		PrivateKey: string(encodePKCS1(t, priv)),
		Issuer:     "aep-bff",
		Audience:   "git-service",
		TTL:        time.Hour,
	})
	if err != nil {
		t.Fatalf("NewTaskTokenManager: %v", err)
	}
	reader := &mcpTestReader{items: []models.ExternalResource{{
		Name:        "salesforce",
		Description: "CRM",
		ConfigKeys:  models.ConfigKeySlice{{Key: "SALESFORCE_TOKEN", Secret: true}},
	}}}
	handler := NewHandler(AppParams{
		Config:               config.Config{},
		Deps:                 Deps{TaskTokens: mgr},
		MCPExternalResources: reader,
		// MCPOrgEndpoints / MCPResourceTypes deliberately nil — those tools
		// degrade to empty results; the round-trip below uses the resource tools.
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, mgr, reader
}

// postMCP POSTs a JSON-RPC body to the mounted path with the given bearer.
func postMCP(t *testing.T, srv *httptest.Server, bearer, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/internal/v1/mcp", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST mcp: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// rpcResult decodes a 200 JSON-RPC envelope and returns its result object.
func rpcResult(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var envelope struct {
		Result map[string]any  `json:"result"`
		Error  *map[string]any `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", *envelope.Error)
	}
	return envelope.Result
}

// TestMCPSurface_FullRoundTrip drives the complete caller flow through the
// mounted mux with a token minted by the concrete signer.
func TestMCPSurface_FullRoundTrip(t *testing.T) {
	srv, mgr, reader := newMCPTestServer(t)

	tok, err := mgr.IssueMCPToken("org-round-trip")
	if err != nil {
		t.Fatalf("IssueMCPToken: %v", err)
	}

	// initialize
	result := rpcResult(t, postMCP(t, srv, tok, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if result["protocolVersion"] != "2024-11-05" {
		t.Fatalf("protocolVersion = %v, want 2024-11-05", result["protocolVersion"])
	}

	// notifications/initialized → 202 (per Streamable-HTTP)
	notif := postMCP(t, srv, tok, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if notif.StatusCode != http.StatusAccepted {
		t.Fatalf("notification status = %d, want 202", notif.StatusCode)
	}

	// tools/list
	result = rpcResult(t, postMCP(t, srv, tok, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	tools, _ := result["tools"].([]any)
	names := map[string]bool{}
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		name, _ := tool["name"].(string)
		names[name] = true
	}
	for _, want := range []string{
		"list_external_resources", "get_external_resource_schema",
		"list_org_endpoints", "list_platform_resource_types",
	} {
		if !names[want] {
			t.Errorf("tools/list missing %q (got %v)", want, names)
		}
	}

	// tools/call — the fake reader must be queried with the org from the CLAIM.
	body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_external_resources","arguments":{}}}`
	result = rpcResult(t, postMCP(t, srv, tok, body))
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("tools/call returned no content: %+v", result)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"salesforce"`) || !strings.Contains(text, `"SALESFORCE_TOKEN"`) {
		t.Errorf("tool payload = %q, want the registered resource + key", text)
	}
	if reader.lastOrg != "org-round-trip" {
		t.Errorf("port org = %q, want org-round-trip (from the token claim)", reader.lastOrg)
	}
}

// TestMCPSurface_NoToken401 proves the mount is behind the verifier: an
// unauthenticated POST never reaches the JSON-RPC handler.
func TestMCPSurface_NoToken401(t *testing.T) {
	srv, _, _ := newMCPTestServer(t)
	resp := postMCP(t, srv, "", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestMCPSurface_WrongAudience401 proves a validly-signed BFF token for another
// service cannot be replayed against the MCP mount.
func TestMCPSurface_WrongAudience401(t *testing.T) {
	srv, mgr, _ := newMCPTestServer(t)
	tok, err := mgr.IssueServiceToken("agents-service", "org-x", 5*time.Minute)
	if err != nil {
		t.Fatalf("IssueServiceToken: %v", err)
	}
	resp := postMCP(t, srv, tok, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestMCPSurface_OrgFromClaimNotRequest plants a different org in every
// request-controlled slot; the port must still be scoped by the claim org.
func TestMCPSurface_OrgFromClaimNotRequest(t *testing.T) {
	srv, mgr, reader := newMCPTestServer(t)
	tok, err := mgr.IssueMCPToken("claim-org")
	if err != nil {
		t.Fatalf("IssueMCPToken: %v", err)
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_external_resources","arguments":{"orgHandle":"attacker-org"}}}`
	req, err := http.NewRequest(http.MethodPost,
		srv.URL+"/internal/v1/mcp?orgHandle=attacker-org", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Oc-Org-Id", "attacker-org")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if reader.lastOrg != "claim-org" {
		t.Fatalf("port org = %q, want claim-org (the signed claim must win over every request-supplied org)", reader.lastOrg)
	}
}

// TestMCPSurface_NoTokenManager404 proves the conditional mount: without a
// token manager nothing can verify a caller, so the path is not mounted at all.
func TestMCPSurface_NoTokenManager404(t *testing.T) {
	handler := NewHandler(AppParams{Config: config.Config{}})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/internal/v1/mcp", "application/json",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)))
	if err != nil {
		t.Fatalf("POST mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (surface unmounted without a token manager)", resp.StatusCode)
	}
}
