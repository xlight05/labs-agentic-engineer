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

// Component-tier coverage for the contract-first internal S2S surface: the
// runner credentials-refresh exchange through the REAL handler graph
// (mountSurfaces → runnerAuthGate → strict handler), with a real RS256
// Task-JWT minted by a test TaskTokenManager. Pins the RUNNER-LOCKSTEP wire
// shape: exact top-level body keys and the capitalized Identity keys — the
// runner must work unchanged against this surface.

package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/credentials"
	"github.com/wso2/aep/aep-api/internal/feature/orgcreds"
	"github.com/wso2/aep/aep-api/internal/platform/auth"
)

type fakeCredsRefresh struct {
	gotExecution, gotOrg string
}

func (f *fakeCredsRefresh) Refresh(_ context.Context, executionID, orgHandle string) (*orgcreds.RefreshResponse, error) {
	f.gotExecution, f.gotOrg = executionID, orgHandle
	return &orgcreds.RefreshResponse{
		Token:     "ghs_fresh",
		ExpiresAt: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC),
		Identity:  credentials.Identity{Name: "AEP Bot", Email: "bot@aep.dev", Login: "aep-bot"},
		TaskID:    executionID,
	}, nil
}

func newInternalTestStack(t *testing.T) (http.Handler, *auth.TaskTokenManager, *fakeCredsRefresh) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	mgr, err := auth.NewTaskTokenManager(auth.TaskTokenConfig{
		PrivateKey: string(pemKey), Issuer: "aep-bff", Audience: "git-service", TTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewTaskTokenManager: %v", err)
	}
	svc := &fakeCredsRefresh{}
	h := NewHandler(AppParams{
		InternalDeps: InternalDeps{
			CredsRefresh: svc,
			RunnerAuth:   auth.NewRunnerAuthorizer(mgr, nil, nil),
		},
	})
	return h, mgr, svc
}

func TestInternalSurface_RunnerRefresh_Lockstep(t *testing.T) {
	t.Parallel()
	h, mgr, svc := newInternalTestStack(t)

	tok, err := mgr.Issue("exec-42", "org-acme", "proj-1")
	if err != nil {
		t.Fatalf("issue task token: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/executions/exec-42/credentials/refresh", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("refresh: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if svc.gotExecution != "exec-42" || svc.gotOrg != "org-acme" {
		t.Fatalf("service saw execution=%q org=%q — org must come from the verified token", svc.gotExecution, svc.gotOrg)
	}

	// RUNNER LOCKSTEP: exact field sets, including the capitalized Identity keys.
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v\n%s", err, rec.Body.String())
	}
	if got, want := slices.Sorted(maps.Keys(body)), []string{"expiresAt", "identity", "taskId", "token"}; !slices.Equal(got, want) {
		t.Fatalf("top-level keys drifted: got %v want %v", got, want)
	}
	var identity map[string]json.RawMessage
	if err := json.Unmarshal(body["identity"], &identity); err != nil {
		t.Fatalf("identity: %v", err)
	}
	if got, want := slices.Sorted(maps.Keys(identity)), []string{"Email", "Login", "Name"}; !slices.Equal(got, want) {
		t.Fatalf("identity keys drifted (capitalized, runner lockstep): got %v want %v", got, want)
	}
}

func TestInternalSurface_AuthPosture(t *testing.T) {
	t.Parallel()
	h, mgr, _ := newInternalTestStack(t)

	// No bearer → 401 envelope.
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/executions/exec-42/credentials/refresh", strings.NewReader(""))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"code"`) {
		t.Fatalf("no bearer: want 401 envelope, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Bearer bound to a DIFFERENT execution → 403 (the INT-6 fence).
	tok, _ := mgr.Issue("exec-OTHER", "org-acme", "proj-1")
	req = httptest.NewRequest(http.MethodPost, "/internal/v1/executions/exec-42/credentials/refresh", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("mismatched execution: want 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}
