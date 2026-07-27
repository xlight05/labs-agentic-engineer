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

package eventcore

import (
	"strings"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// This file holds the event plane's three DECISIONS as pure functions over
// facts: may this pull request merge, which components does this diff touch,
// and may this run dispatch. They are separated from the handlers that fetch
// the facts so each can be read, argued with and tested on its own — the merge
// policy in particular, because it is the seam review logic arrives behind.

// mergeDecision is the auto-merge policy seam's verdict. Reason is written for
// a log line and for whoever later replaces this policy; Matched lists the
// milestone issues the pull request claims, which is the evidence the verdict
// rests on.
type mergeDecision struct {
	Merge   bool
	Reason  string
	Matched []int
}

// decideAutoMerge IS the merge policy: a pull request whose Resolves list
// references at least one agent-work issue in the run's milestone squash-merges.
//
// There is deliberately no verification BEFORE the merge. The verification is
// the POST-MERGE build: the merge is what triggers it, so gating the merge on a
// build would gate it on the thing it exists to cause. A red build is not a
// dead end either — it mints a fix issue into the same milestone, the run works
// it in the next cycle, and the loop converges. The agent's own compile-level
// checks (go build / tsc, lockfile resolution) run before it opens the pull
// request and catch the cheap failures; the cluster catches the rest.
//
// What the predicate DOES buy is scope: a pull request that claims nothing in
// this milestone is not this run's work and is left alone for a human. Review
// logic (approvals, checks, a human gate) arrives later BEHIND this function —
// it is one named decision over facts precisely so that swapping it is a local
// change with a local test, not a rewrite of the handler.
func decideAutoMerge(resolves []int, milestoneIssues []sourcecontrol.IssueInfo) mergeDecision {
	if len(resolves) == 0 {
		return mergeDecision{Reason: "pull request resolves no issue"}
	}
	work := make(map[int]bool, len(milestoneIssues))
	for _, iss := range milestoneIssues {
		if delivery.HasLabel(iss.Labels, delivery.LabelAgentWork) {
			work[iss.Number] = true
		}
	}
	var matched []int
	for _, n := range resolves {
		if work[n] {
			matched = append(matched, n)
		}
	}
	if len(matched) == 0 {
		return mergeDecision{Reason: "no resolved issue is agent work in this milestone"}
	}
	return mergeDecision{Merge: true, Reason: "resolves agent work in the run's milestone", Matched: matched}
}

// The path diff itself is delivery.DiffComponents in the domain root: the run
// supervisor has to compute the SAME expected component set to know when a
// cycle's builds have all reported, and two copies of a prefix-matching rule
// would eventually disagree about what a merge rebuilds.

// dispatchable is the dispatch predicate: no gate is open in the milestone and
// its working set is non-empty.
//
// It reads the ONE GraphQL call behind MilestoneIssueCounts rather than a
// milestone's open_issues field, which counts pull requests and would keep a
// finished run "workable" for as long as one of its PRs stayed open.
//
// The second clause is the WORKING SET, not "some issue is open": a milestone
// holding only ledger issues (human-filed, unadopted, no "aep" label) has
// nothing to work, and declaring it workable would wake a run whose first act
// is to find an empty working set. The exclusions live on the counts type so
// this predicate and any settle check agree on what work is.
func dispatchable(counts *sourcecontrol.MilestoneIssueCounts) bool {
	if counts == nil {
		return false
	}
	return counts.OpenProvision == 0 && counts.OpenNonGateWork() > 0
}

// attemptsFor counts how many of a component's WorkflowRuns belong to one
// (component, commit) pair — the automatic re-trigger budget, derived from
// OpenChoreo instead of stored.
//
// Deriving it is what keeps the budget honest under every restart, replay and
// redelivery: the runs ARE the attempts. A counter column would have to be
// incremented by exactly the code paths that create runs, and a redelivered
// webhook or a crashed handler would desynchronise it from the cluster.
func attemptsFor(runs []BuildRun, prefix string) int {
	n := 0
	for _, r := range runs {
		if strings.HasPrefix(strings.ToLower(r.Name), prefix) {
			n++
		}
	}
	return n
}
