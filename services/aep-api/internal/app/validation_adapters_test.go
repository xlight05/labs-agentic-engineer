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

package app

import (
	"context"
	"testing"

	"github.com/wso2/aep/aep-api/internal/delivery/validation"
)

// The v1 mock provider returns a shared admin/admin account marked Mock for any
// request, ignoring the (future-only) role/purpose/username hints.
func TestMockValidationCredentials_ReturnsMockAdmin(t *testing.T) {
	got, err := mockValidationCredentials{}.RequestCredentials(
		context.Background(), "org", "proj",
		validation.CredentialRequest{Role: "some-role", Username: "someone"},
	)
	if err != nil {
		t.Fatalf("RequestCredentials: %v", err)
	}
	if got.Username != "admin" || got.Password != "admin" {
		t.Errorf("credential = %q/%q; want admin/admin", got.Username, got.Password)
	}
	if !got.Mock {
		t.Error("credential must be marked Mock so the runner reports it as a stand-in")
	}
	if got.Note == "" {
		t.Error("mock credential should carry an explanatory Note")
	}
}
