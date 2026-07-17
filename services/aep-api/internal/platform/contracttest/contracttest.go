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

// Package contracttest gives arch-guard tests the committed OpenAPI contract's
// raw bytes. The runtime validator loads the contract from gen.GetSpec()
// (baked into the binary); tests assert the SOURCE of truth directly instead,
// so a contract edit is caught against the file developers actually edit.
package contracttest

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// SourceYAML returns the bytes of packages/contracts/api/v1/openapi.yaml,
// located relative to this file so the read is independent of the working
// directory (five levels up: contracttest → platform → internal → aep-api →
// services → repo root).
func SourceYAML(t *testing.T) []byte {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("contracttest: runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(self), "..", "..", "..", "..", "..")
	p := filepath.Join(root, "packages", "contracts", "api", "v1", "openapi.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read source contract %s: %v", p, err)
	}
	return b
}
