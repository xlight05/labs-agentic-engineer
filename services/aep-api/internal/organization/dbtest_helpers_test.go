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

package organization_test

// Helpers duplicated here from the still-white-box (package organization)
// unit-test files that originally defined them (credential_service_test.go,
// anthropic_service_test.go). Those files stay package organization — they
// test unexported logic and don't import dbtest, so they're not part of the
// import cycle — but the DBTEST-tier files in this package (organization_test)
// need the same fakes. Each helper below references only EXPORTED production
// symbols, so duplication (rather than exporting production internals) is the
// honest fix; see AGENTS.md task notes on the organization dbtest conversion.

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/wso2/aep/aep-api/internal/organization"
)

// --- fake GitHub (mirrors credential_service_test.go's stubGitHub) ----------

// stubGitHub is a configurable stand-in for api.github.com. Register a status +
// body per "METHOD /path" with on(); an unregistered path 404s, so a probe's
// own not-found handling is exercised. Query strings are ignored (routing is on
// r.URL.Path).
type stubGitHub struct {
	*httptest.Server
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
}

func newStubGitHub(t testing.TB) *stubGitHub {
	t.Helper()
	s := &stubGitHub{routes: map[string]http.HandlerFunc{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		h := s.routes[r.Method+" "+r.URL.Path]
		s.mu.Unlock()
		if h == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		h(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// on registers a fixed status+body for an exact method+path.
func (s *stubGitHub) on(method, path string, code int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[method+" "+path] = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}
}

// assertValidationCode asserts err is an *organization.ValidationError
// carrying wantCode (mirrors credential_service_test.go's assertValidationCode).
func assertValidationCode(t testing.TB, err error, wantCode string) {
	t.Helper()
	var ve *organization.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *organization.ValidationError(%s), got %#v", wantCode, err)
	}
	if ve.Code != wantCode {
		t.Fatalf("ValidationError.Code: got %q want %q", ve.Code, wantCode)
	}
}

// --- fake Anthropic API (mirrors anthropic_service_test.go's anthropicFakeAPI) --

// anthropicUnitKey is shaped like a real key ("sk-ant-api03-" + suffix), long
// enough for looksLikeAnthropicKey. preview: prefix [:15], last4 "1234".
const anthropicUnitKey = "sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz1234"

// anthropicProbeCapture records what the validation probe actually sent.
type anthropicProbeCapture struct {
	calls   int
	method  string
	path    string
	apiKey  string
	version string
}

// anthropicFakeAPI serves the validation probe with a fixed status and
// captures the last request's shape.
func anthropicFakeAPI(t testing.TB, status int) (string, *anthropicProbeCapture) {
	t.Helper()
	rec := &anthropicProbeCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.calls++
		rec.method, rec.path = r.Method, r.URL.Path
		rec.apiKey = r.Header.Get("x-api-key")
		rec.version = r.Header.Get("anthropic-version")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"id":"msg_fake"}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, rec
}

// anthropicValidationCode unwraps the *organization.ValidationError code or
// fails the test.
func anthropicValidationCode(t *testing.T, err error) string {
	t.Helper()
	var ve *organization.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *organization.ValidationError, got %#v", err)
	}
	return ve.Code
}
