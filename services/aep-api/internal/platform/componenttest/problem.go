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

package componenttest

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// Envelope is the flat error body the contract-first edge serves for every
// non-2xx response: {code, message, details?} (contract schema Error).
type Envelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details []struct {
		Field   string `json:"field"`
		Message string `json:"message"`
	} `json:"details"`
}

// DecodeEnvelope parses a flat error envelope, failing the test on anything
// that isn't one.
func DecodeEnvelope(t testing.TB, body string) Envelope {
	t.Helper()
	var e Envelope
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("not a flat error envelope: %v\n%s", err, body)
	}
	if e.Code == "" {
		t.Fatalf("error envelope missing code:\n%s", body)
	}
	return e
}

// GoldenFieldSet reads a harvested golden JSON object (testdata/harvest/golden)
// and returns its sorted top-level keys — the low-maintenance on-wire contract
// assertion: pin the FIELD SET, not volatile values.
func GoldenFieldSet(t testing.TB, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("golden %s is not a JSON object: %v", path, err)
	}
	return sortedKeys(m)
}

// FieldSet returns the sorted top-level keys of a JSON object body.
func FieldSet(t testing.TB, body string) []string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("body is not a JSON object: %v\n%s", err, body)
	}
	return sortedKeys(m)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
