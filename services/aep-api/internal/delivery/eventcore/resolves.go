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
	"regexp"
	"strconv"
	"strings"
)

// resolvesRefRE matches GitHub's closing keywords followed by a same-repo
// issue reference. The runner writes one line per completed issue
// ("Resolves #12"), so this is the platform's read of what a cycle claims to
// have finished — and, through the auto-merge predicate, the only thing the
// platform checks before merging.
//
// GitHub's own keyword set is matched (close/closes/closed, fix/fixes/fixed,
// resolve/resolves/resolved) so that what the platform counts and what GitHub
// closes at merge can never diverge. Cross-repo references (`owner/repo#12`)
// are deliberately not matched: a milestone lives in one repository, and a
// reference into another one cannot be a member of it.
var resolvesRefRE = regexp.MustCompile(`(?i)(^|[^/\w])(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s*:?\s*#(\d+)\b`)

// parseResolvesRefs returns every issue number a pull-request body claims to
// resolve, in first-seen order, deduplicated.
//
// It is a reimplementation rather than a shared helper on purpose: the
// execution slice's single-ref parser dies with that package, and a slice may
// not import a sibling. The difference that matters is arity — the milestone
// model lands ONE pull request per cycle carrying a reference per completed
// issue, so the list, not the first match, is the fact.
func parseResolvesRefs(body string) []int {
	matches := resolvesRefRE.FindAllStringSubmatch(body, -1)
	if matches == nil {
		return nil
	}
	seen := make(map[int]bool, len(matches))
	out := make([]int, 0, len(matches))
	for _, m := range matches {
		n, err := strconv.Atoi(m[2])
		if err != nil || n <= 0 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// branchMilestoneRE extracts the milestone number from an agent branch. The
// runner derives its own branch identity as `aep/m<milestone#>-c<k>` (or reuses
// an unmerged one on crash resume), so the branch is the cheapest true key from
// a pull request back to its milestone — present in every pull_request payload,
// costing no API call.
var branchMilestoneRE = regexp.MustCompile(`^aep/m(\d+)(?:-|$)`)

// milestoneFromBranch returns the milestone number an agent branch names, or
// (0, false) for any other branch — a human's `feature/x` included, which is
// why the pull-request handlers fall back to the project's live spec run
// rather than treating an unparseable branch as "not ours".
func milestoneFromBranch(ref string) (int, bool) {
	m := branchMilestoneRE.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// The commit-shortening and build-run naming helpers live in the domain ROOT
// (delivery.ShortSHA / BuildRunNamePrefix / BuildRunName): the run supervisor
// reads back the very runs this package triggers, and a name the two halves
// disagreed about would silently break both the re-trigger budget and the
// supervisor's "did this cycle land green?" read.
