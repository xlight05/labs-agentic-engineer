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

package validation

import (
	"context"
	"fmt"
)

// CredentialRequest is what the validation runner asks for when a criterion
// needs a login. Every field is an optional hint: the runner names the kind of
// account it wants (a role, a free-text purpose, a preferred username) and the
// platform answers with a usable account. The hints are advisory today — the
// mock provider returns the same account regardless — but they are the contract
// real per-project user provisioning will honor later (provision a user for the
// requested role) without the agent changing how it asks.
type CredentialRequest struct {
	Role     string
	Purpose  string
	Username string
}

// TestCredential is a usable test account the runner exports in-session
// (AEP_E2E_USERNAME / AEP_E2E_PASSWORD) to drive auth-gated e2e criteria. Mock
// is true while user provisioning is unimplemented (the shared stand-in
// account); Note carries the human explanation so it can surface in the report.
type TestCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Mock     bool   `json:"mock"`
	Note     string `json:"note,omitempty"`
}

// CredentialProvider vends a test account for a project's validation run. The
// mock provider (composition root) returns a shared stand-in; a real provider
// will provision/allocate a per-project user scoped to req.Role. It is fenced
// by project (never org-wide) so the least-privilege posture holds once the
// account is real, not mock.
type CredentialProvider interface {
	RequestCredentials(ctx context.Context, orgHandle, projectID string, req CredentialRequest) (TestCredential, error)
}

// CredentialService answers the runner's test-credential request. It resolves
// the runner's execution id to its project (org-fenced, reusing the
// validation-context ExecutionLocator) so the provider gets a verified project,
// then delegates to the provider. Unknown execution → ErrExecutionNotFound
// (surfaced as 404), exactly like the context fetch.
type CredentialService struct {
	execs ExecutionLocator
	creds CredentialProvider
}

// NewCredentialService wires the credential service.
func NewCredentialService(execs ExecutionLocator, creds CredentialProvider) *CredentialService {
	return &CredentialService{execs: execs, creds: creds}
}

// RequestCredentials resolves the runner's execution to its project (org-fenced)
// and returns the provider's test account. orgHandle is the verified caller org
// (the auth layer fences it against the execution).
func (s *CredentialService) RequestCredentials(ctx context.Context, executionID, orgHandle string, req CredentialRequest) (*TestCredential, error) {
	projectID, found, err := s.execs.LookupExecutionProject(ctx, orgHandle, executionID)
	if err != nil {
		return nil, fmt.Errorf("request credentials: resolve execution: %w", err)
	}
	if !found {
		return nil, ErrExecutionNotFound
	}
	cred, err := s.creds.RequestCredentials(ctx, orgHandle, projectID, req)
	if err != nil {
		return nil, fmt.Errorf("request credentials: provider: %w", err)
	}
	return &cred, nil
}
