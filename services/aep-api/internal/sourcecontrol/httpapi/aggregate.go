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

package httpapi

import (
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol/issues"
)

// Every slice names its type Handler, so embedding them directly would be
// "Handler redeclared". Local aliases give distinct field names (§6).
type issuesHandler = issues.Handler

// Handlers is the sourcecontrol domain's slice handlers, embedded so Go promotes
// each operation exactly once into the edge's composite. It declares nothing.
type Handlers struct {
	*issuesHandler
}

// New assembles the domain: pure wiring, constructor injection only.
//
// Unlike ops, this domain's ports are nil-TOLERANT: the component harness wires
// only what the feature under test needs, and the slices degrade to 503 rather
// than panic. So there is no Deps.Validate here — a nil IssueService is a
// supported configuration, not a broken one.
func New(d sourcecontrol.Deps) (*Handlers, error) {
	return &Handlers{issuesHandler: issues.New(d.Issues)}, nil
}
