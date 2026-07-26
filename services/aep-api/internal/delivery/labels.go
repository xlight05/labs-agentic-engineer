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

package delivery

import "strings"

// The milestone-model label vocabulary. A milestone is one spec version's
// delivery increment AND its ledger, so the labels are what separate the three
// populations living in it: agent work, dispatch gates, and human-filed issues
// that are ledger-only until adopted.
//
// It lives at the domain root because both halves of the loop read it — the
// event plane matching issues to a run, and the supervisor deciding a cycle
// boundary — and neither may import the other.
//
// Nothing here is parsed out of an issue BODY: bodies are prose. The labels
// plus milestone membership are the whole platform-side structure.
const (
	// LabelAgentWork ("aep") marks an issue as agent work. The working set of a
	// run is the open LabelAgentWork issues in its milestone, minus the gates.
	// An issue WITHOUT it is ledger-only: never worked, never stalling settle.
	LabelAgentWork = "aep"
	// LabelProvisionGate ("aep:provision") marks a dispatch gate — a dependency
	// the platform (not the agent) must resolve. An open gate in the milestone
	// holds the next dispatch; it never blocks settle.
	LabelProvisionGate = "aep:provision"
	// LabelValidationWork ("aep:validation") marks the validation issue, worked
	// by the run's validation cycle rather than an ordinary coding cycle.
	LabelValidationWork = "aep:validation"
	// LabelAdopt ("aep:codingagent") is the GitHub-side adoption trigger: a
	// human stamps it on any issue to hand it to the agent.
	LabelAdopt = "aep:codingagent"
)

// HasLabel reports whether labels contains name, case-insensitively (GitHub
// label matching is case-insensitive, and a hand-typed `AEP` must not read as
// a different population).
func HasLabel(labels []string, name string) bool {
	for _, l := range labels {
		if strings.EqualFold(l, name) {
			return true
		}
	}
	return false
}
