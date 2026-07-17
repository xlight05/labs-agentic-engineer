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

package organization

import (
	"context"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/platform/secrets"
)

// (fakeResolver and fakeCred re-used from build_credentials_service_test.go)

func TestRefresh_Happy(t *testing.T) {
	expiry := time.Now().Add(time.Hour)
	res := &fakeResolver{cred: &fakeCred{token: "ghs_abc", exp: expiry}}

	svc := NewCredentialsRefreshService(res)
	resp, err := svc.Refresh(context.Background(), "task-1", "default")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if resp.Token != "ghs_abc" {
		t.Errorf("token = %q; want ghs_abc", resp.Token)
	}
	if resp.TaskID != "task-1" {
		t.Errorf("taskId echo = %q; want task-1", resp.TaskID)
	}
}

// Ensure fakeCred matches the secrets.Credential interface.
var _ secrets.Credential = (*fakeCred)(nil)
