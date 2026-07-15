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

package gen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestVendoredContractFresh fails `go test` (so root `make test`) whenever the
// committed contract moved without `make gen-api`: the vendored copy this
// package embeds (and the validator enforces at runtime) is refreshed ONLY by
// that target, so any byte drift against packages/contracts/api/v1 means the
// generated router/models/validator are stale too. Same posture as
// designspec's schema_vendor_test.go. The console's TS types regenerate from
// the contract on every root `make gen` — without this gate a contract edit
// skews FE (fresh) against BE (stale) with every build green.
func TestVendoredContractFresh(t *testing.T) {
	t.Parallel()
	pairs := []struct{ vendored, source string }{
		{"contract/openapi.yaml", "../../../../../packages/contracts/api/v1/openapi.yaml"},
		{"contract/components.yaml", "../../../../../packages/contracts/api/v1/components.yaml"},
	}
	for _, p := range pairs {
		vendored, err := os.ReadFile(p.vendored)
		if err != nil {
			t.Fatalf("read vendored %s: %v", p.vendored, err)
		}
		source, err := os.ReadFile(filepath.Clean(p.source))
		if err != nil {
			t.Fatalf("read source %s: %v", p.source, err)
		}
		if !bytes.Equal(vendored, source) {
			t.Errorf("%s drifted from %s — the contract was edited without regenerating.\nRun `make gen-api` in services/aep-api and commit the result.", p.vendored, p.source)
		}
	}
}
