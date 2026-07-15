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

package gitrepo

import (
	"strings"
	"testing"
)

// Issues mirroring the badbackend192 repro: two "implement" issues plus a
// timeout-bug issue that a same-incident query must surface.
var searchFixtures = []IssueInfo{
	{Number: 1, Title: "Implement Service2 (slow backend simulator)", Body: "backend that sleeps"},
	{Number: 2, Title: "Implement Service1 (front-facing service with timeout handling)", Body: "calls service2"},
	{Number: 5, Title: "Make service1 HTTP timeout configurable via env var and add retry with backoff for service2 calls", Body: "service2 delay exceeds the 5s timeout"},
}

func numbers(issues []IssueInfo) []int {
	out := make([]int, len(issues))
	for i, iss := range issues {
		out[i] = iss.Number
	}
	return out
}

func contains(nums []int, n int) bool {
	for _, x := range nums {
		if x == n {
			return true
		}
	}
	return false
}

func TestDedupeLabelFor(t *testing.T) {
	// Deterministic: same key → same label (dedup relies on this).
	if a, b := dedupeLabelFor("sre-rca/badbackend-service1"), dedupeLabelFor("sre-rca/badbackend-service1"); a != b {
		t.Fatalf("non-deterministic label: %q vs %q", a, b)
	}
	if got := dedupeLabelFor("sre-rca/svc1"); got != "dedupe:sre-rca/svc1" {
		t.Errorf("unexpected label: %q", got)
	}
	// Whitespace collapses to '-'.
	if got := dedupeLabelFor("sre rca  key"); got != "dedupe:sre-rca-key" {
		t.Errorf("whitespace not collapsed: %q", got)
	}
	// Capped at GitHub's 50-char label limit.
	if got := dedupeLabelFor(strings.Repeat("x", 200)); len(got) != 50 {
		t.Errorf("expected 50-char cap, got %d", len(got))
	}
}

func TestRankIssuesByQuery(t *testing.T) {
	// The exact query shape that returned empty under the old substring
	// filter — a multi-word natural-language query. It must now surface #5.
	got := RankIssuesByQuery(searchFixtures, "service1 timeout retry backoff for service2")
	nums := numbers(got)
	if !contains(nums, 5) {
		t.Fatalf("expected multi-word query to surface issue #5, got %v", nums)
	}
	// #5 mentions the most query terms (service1, timeout, retry, backoff,
	// service2) so it must rank ahead of the weaker #2 (timeout, service2).
	if len(nums) == 0 || nums[0] != 5 {
		t.Errorf("expected #5 ranked first, got order %v", nums)
	}

	// Empty query is a "list all" call — unchanged contract.
	if len(RankIssuesByQuery(searchFixtures, "")) != len(searchFixtures) {
		t.Error("empty query should return all issues")
	}

	// A query with no lexical overlap returns nothing (same as before).
	if got := RankIssuesByQuery(searchFixtures, "oomkilled memory pressure"); len(got) != 0 {
		t.Errorf("expected no matches for unrelated query, got %v", numbers(got))
	}

	// Stopwords/short tokens alone must not match everything.
	if got := RankIssuesByQuery(searchFixtures, "the a for with error"); len(got) != len(searchFixtures) {
		// all-stopword query tokenises to empty -> "list all"
		t.Errorf("all-stopword query should behave as list-all, got %d", len(got))
	}
}
