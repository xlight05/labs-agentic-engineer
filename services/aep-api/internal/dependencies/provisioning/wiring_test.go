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

package provisioning

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/internal/spec"
)

// codingIssue builds a working-set issue as the plan tap writes one: prose plus
// the `aep` label, and nothing else.
func codingIssue(number int, title, state string, extra ...string) sourcecontrol.IssueInfo {
	return sourcecontrol.IssueInfo{
		Number: number,
		Title:  title,
		Body:   "Build it.",
		State:  state,
		Labels: append([]string{delivery.LabelAgentWork}, extra...),
	}
}

// wiringDesign is a two-component design: `orders` consumes the platform
// resource whose gate resolves in these tests, `web` consumes nothing that
// resolves. It is what makes "the comment is keyed by component" testable.
func wiringDesign() []spec.DesignComponent {
	return []spec.DesignComponent{
		{Name: "orders", Dependencies: []spec.Dependency{
			{Kind: spec.DependencyKindPlatformResource, Name: "orders-db", ResourceType: "postgres-cnpg",
				Description: "Managed PostgreSQL via CloudNativePG"},
		}},
		{Name: "web"},
	}
}

// readyOrdersDB drives the readiness watcher to the point where the gate for
// `orders-db` resolves, and returns the fakes so a test can assert what landed.
// It mirrors the live sequence exactly: Provision admits + starts the run, the
// binding goes Ready, the sweep completes the row.
func readyOrdersDB(t *testing.T, seed []sourcecontrol.IssueInfo, design []spec.DesignComponent) (*fakeIssues, *ResourceWatcher) {
	t.Helper()
	issues := newFakeIssues(append([]sourcecontrol.IssueInfo{provisionGateIssue(11, "orders-db")}, seed...))
	bindings := &fakeBindings{byName: map[string]*openchoreo.ResourceReleaseBinding{}}
	svc := newTestService(issues, &fakeExecStore{}, &fakeReeval{}, fakeDesign{comps: design},
		&fakeExtProv{}, &fakePlatProv{}, bindings)

	if err := svc.Provision(context.Background(), "org", "proj", "orders-db", nil, nil); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	w := NewResourceWatcher(svc, nil, time.Second)
	w.now = func() time.Time { return time.Unix(1000, 0).Add(time.Minute) }
	// Two keys for one binding: the watcher reads it back by the run name the
	// provisioner reported (fakePlatProv's "o-<dep>-<env>"), while the wiring
	// resolver addresses it by the canonical ExternalResourceBindingName
	// ("<project>-<dep>-<env>") the real provisioner mints. Registering both keeps
	// the fake from hiding either lookup.
	ready := readyBinding("host", "port")
	bindings.byName["o-orders-db-development"] = ready
	bindings.byName["proj-orders-db-development"] = ready
	if err := w.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	return issues, w
}

// ADR-0004's trigger: the resolved dependencies reach the coding agent at GATE
// RESOLUTION — the first moment the address exists — not at plan time and not
// after the merge. The comment carries the block for the component that consumes
// the dependency, named, so the agent knows which workload.yaml it belongs in.
func TestWiring_PostedOnGateResolution(t *testing.T) {
	issues, _ := readyOrdersDB(t, []sourcecontrol.IssueInfo{codingIssue(21, "Implement orders", "open")}, wiringDesign())

	posted := issues.comments[21]
	if len(posted) != 1 {
		t.Fatalf("want exactly one wiring comment on the working-set issue, got %d: %v", len(posted), posted)
	}
	body := posted[0]
	for _, want := range []string{
		"**Platform-resolved dependencies**",
		"## Component `orders`",
		"```yaml\ndependencies:\n",
		"ref: proj-orders-db",
		"ORDERS_DB_HOST",
		"ORDERS_DB_PORT",
		"### Platform resource — orders-db",
		"postgres-cnpg",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("wiring comment missing %q:\n%s", want, body)
		}
	}
	// `web` consumes nothing — it must not get a block, or the agent would write
	// a dependencies: into a workload.yaml that has no dependencies.
	if strings.Contains(body, "## Component `web`") {
		t.Errorf("a component that does not consume the dependency must have no block:\n%s", body)
	}
	// The gate closes as it always did — the comment is additive to that path.
	if _, closed := issues.closed[11]; !closed {
		t.Errorf("the gate must still close on readiness")
	}
}

// Targeting: the run's WORKING SET and nothing else. Gates are platform holds,
// the validation issue is worked by its own cycle, a closed issue is done, and
// an issue without `aep` is ledger-only — none of them is agent work.
func TestWiring_TargetsTheWorkingSetOnly(t *testing.T) {
	seed := []sourcecontrol.IssueInfo{
		codingIssue(21, "Implement orders", "open"),
		codingIssue(22, "Implement web", "open"),
		codingIssue(23, "Already merged", "closed"),
		codingIssue(24, "Validate the increment", "open", delivery.LabelValidationWork),
		{Number: 25, Title: "A human bug report", State: "open"}, // ledger-only: no `aep`
	}
	issues, _ := readyOrdersDB(t, seed, wiringDesign())

	for _, n := range []int{21, 22} {
		if len(issues.comments[n]) != 1 {
			t.Errorf("working-set issue #%d: want 1 wiring comment, got %d", n, len(issues.comments[n]))
		}
	}
	for _, n := range []int{11, 23, 24, 25} {
		for _, c := range issues.comments[n] {
			if strings.Contains(c, "Platform-resolved dependencies") {
				t.Errorf("issue #%d is not agent work and must not get the wiring comment", n)
			}
		}
	}
}

// Idempotency: the resolve path re-runs (a re-build re-mints and re-settles a
// gate for a dependency that is already Ready), and the same comment must not
// pile up. The aep:wired/<slug> marker stamped on the issue is the guard.
func TestWiring_IdempotentAcrossReRun(t *testing.T) {
	issues := newFakeIssues([]sourcecontrol.IssueInfo{
		provisionGateIssue(11, "orders-db"),
		codingIssue(21, "Implement orders", "open"),
	})
	bindings := &fakeBindings{byName: map[string]*openchoreo.ResourceReleaseBinding{
		"proj-orders-db-development": readyBinding("host", "port"),
	}}
	svc := newTestService(issues, &fakeExecStore{}, &fakeReeval{}, fakeDesign{comps: wiringDesign()},
		&fakeExtProv{}, &fakePlatProv{}, bindings)

	// Two builds in a row over an already-Ready dependency: settleReadyGates
	// admits + completes a provision run each time it finds an open gate.
	for i := 0; i < 2; i++ {
		if fails := svc.settleReadyGates(context.Background(), "org", "proj", nil); len(fails) != 0 {
			t.Fatalf("settleReadyGates pass %d: %+v", i, fails)
		}
		// Re-open the gate so the second pass genuinely re-runs the resolve path
		// (a re-build re-mints one) — otherwise the test proves only that a closed
		// gate is skipped.
		for j := range issues.list {
			if issues.list[j].Number == 11 {
				issues.list[j].State = "open"
			}
		}
	}

	if got := len(issues.comments[21]); got != 1 {
		t.Fatalf("a re-run must not repeat the wiring comment: got %d comments\n%v", got, issues.comments[21])
	}
	if !delivery.HasLabel(issueByNumber(t, issues, 21).Labels, wiredDepLabel("orders-db")) {
		t.Errorf("the posted issue must carry the aep:wired/<slug> marker")
	}
}

// A gate resolving with nothing open to work posts nothing: there is no agent to
// tell, and the next version's plan will resolve the wiring afresh.
func TestWiring_NoOpenWorkPostsNothing(t *testing.T) {
	issues, _ := readyOrdersDB(t, []sourcecontrol.IssueInfo{codingIssue(21, "Implement orders", "closed")}, wiringDesign())

	for n, cs := range issues.comments {
		for _, c := range cs {
			if strings.Contains(c, "Platform-resolved dependencies") {
				t.Errorf("no open work: nothing should be posted, but issue #%d got:\n%s", n, c)
			}
		}
	}
}

// The block itself, across all four consumer-side dependency kinds. This is the
// contract the coding agent copies verbatim, so the env-var names and visibility
// values are asserted literally.
func TestWiring_ResolvesEveryDependencyKind(t *testing.T) {
	design := []spec.DesignComponent{{
		Name: "web",
		Dependencies: []spec.Dependency{
			{Kind: spec.DependencyKindOrgService, Name: "employee-api"},
			{Kind: spec.DependencyKindComponent, Name: "orders"},
			{Kind: spec.DependencyKindExternal, Name: "stripe"},
			{Kind: spec.DependencyKindPlatformResource, Name: "orders-db", ResourceType: "postgres-cnpg",
				Description: "Managed PostgreSQL via CloudNativePG"},
		},
	}}
	issues := newFakeIssues([]sourcecontrol.IssueInfo{
		provisionGateIssue(11, "orders-db"),
		codingIssue(21, "Implement web", "open"),
	})
	bindings := &fakeBindings{byName: map[string]*openchoreo.ResourceReleaseBinding{
		"proj-stripe-development":    readyBinding("STRIPE_API_KEY"),
		"proj-orders-db-development": readyBinding("host", "port"),
	}}
	svc := NewService(Deps{
		Issues: issues, Execs: &fakeExecStore{}, Reeval: &fakeReeval{},
		Design: fakeDesign{comps: design}, Repos: fakeRepos{},
		ExtProv: &fakeExtProv{}, PlatProv: &fakePlatProv{}, Bindings: bindings,
		Providers: fakeProviders{
			nsVisible: map[string]openchoreo.WorkloadEndpointInfo{
				"employee-api": {Project: "hr", Component: "hr-employee-api", Name: "http"},
			},
			projectEP: map[string]openchoreo.WorkloadEndpointInfo{
				"proj-orders": {Component: "proj-orders", Name: "http"},
			},
		},
	})

	svc.postResolvedWiring(context.Background(), "org", "proj", "orders-db")

	posted := issues.comments[21]
	if len(posted) != 1 {
		t.Fatalf("want one wiring comment, got %d", len(posted))
	}
	body := posted[0]
	for _, want := range []string{
		"visibility: namespace", "address: EMPLOYEE_API_URL",
		"visibility: project", "address: ORDERS_URL",
		"ref: proj-stripe", "STRIPE_API_KEY: STRIPE_API_KEY",
		"ref: proj-orders-db", "ORDERS_DB_HOST", "ORDERS_DB_PORT",
		"### Consumed API contract — employee-api",
		"project `hr`, component `hr-employee-api`, endpoint `http`",
		"list_org_component_endpoints",
		"### Consumed API contract — orders (local)",
		"### Platform resource — orders-db",
		"Managed PostgreSQL via CloudNativePG",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("wiring comment missing %q:\n%s", want, body)
		}
	}
	// Secret values never leave the SecretWriter port: outputs ride as NAMES.
	if strings.Contains(strings.ToLower(body), "password") {
		t.Errorf("no secret material may appear in the wiring comment:\n%s", body)
	}
}

func TestOrgServiceURLEnvAndEnvVarName(t *testing.T) {
	// Byte parity with endpoints.OrgServiceURLEnv / the upstream envVarName.
	cases := map[string]string{"employee-api": "EMPLOYEE_API_URL", "todo": "TODO_URL", "order-svc-2": "ORDER_SVC_2_URL"}
	for in, want := range cases {
		if got := orgServiceURLEnv(in); got != want {
			t.Errorf("orgServiceURLEnv(%q) = %q, want %q", in, got, want)
		}
	}
	if got := envVarName("orders-db", "host"); got != "ORDERS_DB_HOST" {
		t.Errorf("envVarName(orders-db, host) = %q, want ORDERS_DB_HOST", got)
	}
}

// The gate index and the wiring marker are DIFFERENT labels: overloading
// aep:dep/<slug> onto a coding issue would make gateDepFromLabels answer for
// issues that are not gates.
func TestWiredDepLabel_IsNotTheGateIndex(t *testing.T) {
	if got := wiredDepLabel("Orders DB"); got != "aep:wired/orders-db" {
		t.Errorf("wiredDepLabel = %q, want aep:wired/orders-db", got)
	}
	if wiredDepLabel("orders-db") == gateDepLabel("orders-db") {
		t.Errorf("the wiring marker must not collide with the gate index")
	}
	if got := gateDepFromLabels([]string{wiredDepLabel("orders-db")}); got != "" {
		t.Errorf("the wiring marker must not read back as a gate's dependency, got %q", got)
	}
	if wiredDepLabel("  ") != "" {
		t.Errorf("an unslugifiable name must yield no label")
	}
}

func issueByNumber(t *testing.T, f *fakeIssues, number int) sourcecontrol.IssueInfo {
	t.Helper()
	for _, i := range f.list {
		if i.Number == number {
			return i
		}
	}
	t.Fatalf("issue #%d not found", number)
	return sourcecontrol.IssueInfo{}
}
