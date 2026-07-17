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

// Package httpapi aggregates the ops domain's slice handlers into the one type
// the edge embeds, and composes the domain from its Deps.
//
// # Why the composition lives here and not in the domain root
//
// §2 puts module.go in the domain root and §8 has it return *httpapi.Handlers.
// That cannot compile: the root would import httpapi, httpapi imports the
// slices, and the slices import the root — a cycle. This package is the only
// place that can see both the root and the slices, so it is where the domain is
// assembled. The root keeps the Deps TYPE (it names only ports, so no cycle).
//
// # Why it declares no methods
//
// It only embeds. A method declared here would sit at depth-1 from the edge and
// silently beat a legacy method at depth-2 — a green build serving the wrong
// body. See docs/design/domain-oriented-architecture.md §19.1.
package httpapi

import (
	"fmt"

	"github.com/wso2/aep/aep-api/internal/ops"
	"github.com/wso2/aep/aep-api/internal/ops/createreport"
	"github.com/wso2/aep/aep-api/internal/ops/getreport"
	"github.com/wso2/aep/aep-api/internal/ops/listreports"
)

// An embedded field is named by its UNQUALIFIED type name, so embedding
// *createreport.Handler beside *getreport.Handler is "Handler redeclared" —
// every slice calls its type Handler. Local aliases give distinct field names
// while each slice keeps the unstuttering name. (The same trick the edge needs
// for the domains themselves; §6.)
type (
	createReportHandler = createreport.Handler
	getReportHandler    = getreport.Handler
	listReportsHandler  = listreports.Handler
)

// Handlers is the ops domain's slice handlers, embedded so Go promotes each
// operation exactly once into the edge's composite. It declares nothing itself.
type Handlers struct {
	*createReportHandler
	*getReportHandler
	*listReportsHandler
}

// New assembles the domain: pure wiring, constructor injection only, no setters.
// Being pure is what lets the assembly test build the real graph in
// microseconds with a faked repository.
func New(d ops.Deps) (*Handlers, error) {
	if err := d.Validate(); err != nil {
		return nil, fmt.Errorf("ops httpapi: %w", err)
	}
	return &Handlers{
		createReportHandler: createreport.New(d.Reports),
		getReportHandler:    getreport.New(d.Reports, d.Execs),
		listReportsHandler:  listreports.New(d.Reports),
	}, nil
}
