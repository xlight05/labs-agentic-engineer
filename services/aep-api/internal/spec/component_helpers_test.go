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

package spec_test

import (
	"testing"

	"github.com/wso2/aep/aep-api/internal/spec"
	spechttpapi "github.com/wso2/aep/aep-api/internal/spec/httpapi"
)

// mustSpecHandlers assembles the spec domain for a component test, filling only
// the field(s) the op under test needs (the rest stay nil and fail loud only if
// invoked, exactly as the edge does). Mirrors how the composition root builds
// edge.Deps.Spec — the component test now wires the domain, not the loose
// per-service fields the edge no longer carries.
func mustSpecHandlers(t *testing.T, d spec.Deps) *spechttpapi.Handlers {
	t.Helper()
	h, err := spechttpapi.New(d)
	if err != nil {
		t.Fatalf("assemble spec domain: %v", err)
	}
	return h
}
