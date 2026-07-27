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
	"reflect"
	"testing"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// UNIT TIER — the four decisions, as pure functions.

func TestParseResolvesRefs(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []int
	}{
		{"one per line, every keyword", "Resolves #12\nCloses #13\nFixes #14", []int{12, 13, 14}},
		{"case and colon variants", "resolved: #7\nCLOSED #8\nfix #9", []int{7, 8, 9}},
		{"deduplicated, first-seen order", "Resolves #5\nCloses #5\nFixes #2", []int{5, 2}},
		{"a bare mention is not a claim", "See #12 for context", nil},
		{"cross-repo references are not milestone members", "Closes other/repo#12", nil},
		{"prose around the reference", "This resolves #12 in full.", []int{12}},
		{"nothing at all", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseResolvesRefs(c.body); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("parseResolvesRefs(%q) = %v, want %v", c.body, got, c.want)
			}
		})
	}
}

func TestMilestoneFromBranch(t *testing.T) {
	cases := []struct {
		ref  string
		want int
		ok   bool
	}{
		{"aep/m7-c1", 7, true},
		{"aep/m12-c3", 12, true},
		{"aep/m7", 7, true},
		{"feature/whatever", 0, false},
		{"aep/mx-c1", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := milestoneFromBranch(c.ref)
		if got != c.want || ok != c.ok {
			t.Errorf("milestoneFromBranch(%q) = (%d, %v), want (%d, %v)", c.ref, got, ok, c.want, c.ok)
		}
	}
}

func TestDecideAutoMerge(t *testing.T) {
	work := []sourcecontrol.IssueInfo{
		{Number: 12, State: "open", Labels: []string{delivery.LabelAgentWork}},
		{Number: 13, State: "open", Labels: []string{delivery.LabelAgentWork, delivery.LabelProvisionGate}},
		// A ledger issue: in the milestone, but not agent work.
		{Number: 14, State: "open", Labels: []string{"question"}},
	}
	cases := []struct {
		name     string
		resolves []int
		want     bool
		matched  []int
	}{
		{"one agent-work issue is enough", []int{12}, true, []int{12}},
		{"several", []int{12, 13}, true, []int{12, 13}},
		{"a claim outside the milestone decides nothing", []int{99}, false, nil},
		{"a ledger issue is not agent work", []int{14}, false, nil},
		{"partial match still merges", []int{99, 12}, true, []int{12}},
		{"claiming nothing never merges", nil, false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decideAutoMerge(c.resolves, work)
			if got.Merge != c.want {
				t.Fatalf("Merge = %v (%s), want %v", got.Merge, got.Reason, c.want)
			}
			if !reflect.DeepEqual(got.Matched, c.matched) {
				t.Fatalf("Matched = %v, want %v", got.Matched, c.matched)
			}
		})
	}
}

// TestDispatchable pins the predicate against the populations a milestone can
// actually hold. The cases that matter are the ones where "some issue is open"
// and "there is work to do" part company: a ledger-only milestone, a milestone
// whose only open work is the validation issue, and a gate that also carries
// the agent-work label.
func TestDispatchable(t *testing.T) {
	cases := []struct {
		name   string
		counts *sourcecontrol.MilestoneIssueCounts
		want   bool
	}{
		{
			"one open task, no gate",
			&sourcecontrol.MilestoneIssueCounts{OpenProvision: 0, OpenWork: 1, OpenTotal: 1},
			true,
		},
		{
			// The regression this predicate exists to prevent: human-filed issues
			// with no "aep" label are the milestone's LEDGER. They are never worked,
			// so a milestone holding nothing else has an empty working set.
			"only ledger issues are open",
			&sourcecontrol.MilestoneIssueCounts{OpenProvision: 0, OpenWork: 0, OpenTotal: 4},
			false,
		},
		{
			// The validation issue belongs to the run's validation cycle, not to a
			// coding cycle, so it is not work a dispatch can pick up.
			"only the validation issue is open",
			&sourcecontrol.MilestoneIssueCounts{OpenProvision: 0, OpenWork: 1, OpenWorkValidation: 1, OpenTotal: 1},
			false,
		},
		{
			"an open gate holds dispatch even with work waiting",
			&sourcecontrol.MilestoneIssueCounts{OpenProvision: 1, OpenWork: 3, OpenTotal: 4},
			false,
		},
		{
			// A gate carrying "aep" too: the gate clause already holds dispatch, and
			// the gate must not be double-counted into the working set either.
			"a gate that also carries the work label",
			&sourcecontrol.MilestoneIssueCounts{OpenProvision: 1, OpenWork: 1, OpenWorkGate: 1, OpenTotal: 1},
			false,
		},
		{
			"everything closed",
			&sourcecontrol.MilestoneIssueCounts{},
			false,
		},
		{"unknown milestone", nil, false},
	}
	for _, c := range cases {
		if got := dispatchable(c.counts); got != c.want {
			t.Errorf("dispatchable(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestAttemptsFor pins the budget's counting rule, including the two ways it
// could over-count: another component's runs, and the same component at a
// different commit.
func TestAttemptsFor(t *testing.T) {
	runs := []BuildRun{
		{Name: delivery.BuildRunName("proj1", "order-service", "abc123def456789", 1)},
		{Name: delivery.BuildRunName("proj1", "order-service", "abc123def456789", 2)},
		{Name: delivery.BuildRunName("proj1", "order-service", "999999999999999", 1)},
		{Name: delivery.BuildRunName("proj1", "web", "abc123def456789", 1)},
	}
	if got := attemptsFor(runs, delivery.BuildRunNamePrefix("proj1", "order-service", "abc123def456789")); got != 2 {
		t.Fatalf("attempts for (order-service, abc123def456) = %d, want 2", got)
	}
	if got := attemptsFor(runs, delivery.BuildRunNamePrefix("proj1", "web", "abc123def456789")); got != 1 {
		t.Fatalf("attempts for (web, abc123def456) = %d, want 1", got)
	}
	if got := attemptsFor(nil, delivery.BuildRunNamePrefix("proj1", "web", "abc")); got != 0 {
		t.Fatalf("attempts with no runs = %d, want 0", got)
	}
}

// TestBudgetIsOnePerComponentPerSHA drives the rule through the function that
// enforces it, which is also the function that makes the fan-out idempotent —
// they are the same counting, so they cannot drift apart.
func TestBudgetIsOnePerComponentPerSHA(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	sha := "abc123def456789"

	// The merge fan-out: one run, and a redelivery adds none.
	for i := 0; i < 3; i++ {
		if _, err := h.events.ensureBuildRun(ctx, testOrg, testProject, "order-service", sha, mergeBuildLimit); err != nil {
			t.Fatalf("fan-out %d: %v", i, err)
		}
	}
	if got := h.builds.triggeredFor("order-service"); len(got) != 1 {
		t.Fatalf("the merge fan-out allows exactly one run per (component, SHA), got %v", got)
	}

	// The red path allows one more, and never a third.
	attempt, err := h.events.ensureBuildRun(ctx, testOrg, testProject, "order-service", sha, redBuildLimit)
	if err != nil || attempt != 2 {
		t.Fatalf("the first red must re-trigger attempt 2, got (%d, %v)", attempt, err)
	}
	attempt, err = h.events.ensureBuildRun(ctx, testOrg, testProject, "order-service", sha, redBuildLimit)
	if err != nil || attempt != 0 {
		t.Fatalf("the budget is spent after one re-trigger, got (%d, %v)", attempt, err)
	}
	if got := h.builds.triggeredFor("order-service"); len(got) != 2 {
		t.Fatalf("exactly two runs total for one (component, SHA), got %v", got)
	}

	// A NEW commit starts a fresh allowance — the budget is per SHA.
	attempt, err = h.events.ensureBuildRun(ctx, testOrg, testProject, "order-service", "feedfacefeed0", mergeBuildLimit)
	if err != nil || attempt != 1 {
		t.Fatalf("a new merge SHA gets its own attempt 1, got (%d, %v)", attempt, err)
	}
}
