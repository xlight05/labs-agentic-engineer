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
	"errors"
	"testing"
)

// fakeCredProvider records the project it was asked for and returns a canned
// account, so the service test can assert the execution→project fence without
// the real mock provider.
type fakeCredProvider struct {
	gotProject string
	gotReq     CredentialRequest
	cred       TestCredential
}

func (f *fakeCredProvider) RequestCredentials(_ context.Context, _, projectID string, req CredentialRequest) (TestCredential, error) {
	f.gotProject = projectID
	f.gotReq = req
	return f.cred, nil
}

func TestRequestCredentials_ResolvesProjectAndReturnsAccount(t *testing.T) {
	prov := &fakeCredProvider{cred: TestCredential{Username: "admin", Password: "admin", Mock: true, Note: "mock"}}
	svc := NewCredentialService(fakeExecLocator{projectID: "proj", found: true}, prov)

	got, err := svc.RequestCredentials(context.Background(), "exec-1", "org", CredentialRequest{Role: "admin"})
	if err != nil {
		t.Fatalf("RequestCredentials: %v", err)
	}
	if got.Username != "admin" || got.Password != "admin" || !got.Mock {
		t.Errorf("credential = %+v; want mock admin/admin", got)
	}
	// The provider must be fenced to the resolved project, not handed the raw
	// execution id or org — this is the tenant fence real provisioning relies on.
	if prov.gotProject != "proj" {
		t.Errorf("provider project = %q; want %q", prov.gotProject, "proj")
	}
	if prov.gotReq.Role != "admin" {
		t.Errorf("provider role hint = %q; want %q", prov.gotReq.Role, "admin")
	}
}

func TestRequestCredentials_UnknownExecutionIs404(t *testing.T) {
	prov := &fakeCredProvider{}
	svc := NewCredentialService(fakeExecLocator{found: false}, prov) // execution not in caller's org

	_, err := svc.RequestCredentials(context.Background(), "exec-x", "org", CredentialRequest{})
	if !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("want ErrExecutionNotFound (→ 404), got %v", err)
	}
	if prov.gotProject != "" {
		t.Errorf("provider must not be called on unknown execution; got project %q", prov.gotProject)
	}
}
