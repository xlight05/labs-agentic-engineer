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

package sourcecontrol_test

import (
	"encoding/json"
	"testing"

	"github.com/wso2/aep/aep-api/internal/platform/gittest"
)

// requestsMatching returns the recorded requests whose method+path match, in
// arrival order.
func requestsMatching(reqs []gittest.RecordedRequest, method, path string) []gittest.RecordedRequest {
	var out []gittest.RecordedRequest
	for _, r := range reqs {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// onlyRequest asserts exactly one recorded request matches method+path and
// returns it.
func onlyRequest(t *testing.T, reqs []gittest.RecordedRequest, method, path string) gittest.RecordedRequest {
	t.Helper()
	m := requestsMatching(reqs, method, path)
	if len(m) != 1 {
		t.Fatalf("want exactly 1 %s %s request, got %d (all: %+v)", method, path, len(m), reqs)
	}
	return m[0]
}

// decodeBody unmarshals a recorded request body into dst.
func decodeBody(t *testing.T, raw string, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		t.Fatalf("decode request body %q: %v", raw, err)
	}
}
