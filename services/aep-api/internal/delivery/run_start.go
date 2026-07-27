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

// StartRunRequest asks the run supervisor for a run over one milestone.
//
// It lives at the domain ROOT because two sub-packages ask for the same thing
// and may not import each other (`slice ⊥ sibling`): the plan path in `build`
// starts the spec run it just planned, and the event plane starts an incident
// run on adoption or from the reconcile sweep. Each declares its own narrow
// `RunStarter` port over THIS type, so one composition-root adapter satisfies
// both without either package naming the other.
type StartRunRequest struct {
	OrgID     string
	ProjectID string
	// MilestoneNumber is the platform key of the milestone to work. Titles are
	// renamable on GitHub; the number never changes.
	MilestoneNumber int
	// MilestoneTitle is the title at creation (== the `v<N>` spec tag), carried
	// for display and for the runner's `gh issue list --milestone "<title>"`
	// discovery call.
	MilestoneTitle string
	// Origin is RunOriginSpecBuild for the plan path and
	// RunOriginIncidentAdoption for everything the event plane starts.
	Origin string
	// RunID is the admitted run row this request supervises, when the caller
	// already admitted one (the plan path admits the row itself, so that the
	// spec-run mutex is armed before the slow planning turn begins). Empty means
	// "admit one yourself" — the adoption and sweep paths, where admission and
	// supervision must happen together or a row exists that nobody drives.
	RunID string
}
