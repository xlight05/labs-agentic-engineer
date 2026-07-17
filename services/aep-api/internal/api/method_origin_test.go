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

package api

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/wso2/aep/aep-api/internal/gen"
)

// The migration ledger + the second of the two nets guarding the edge
// (docs/design/domain-oriented-architecture.md §19.1).
//
// apiServer declares no methods: it satisfies gen.StrictServerInterface purely
// by promotion from its embedded fields. That makes "which embed serves this
// op?" the load-bearing question of the whole migration, and the compiler only
// answers it in one case (a same-depth tie). These tests answer it for all 61.
//
// Why this is not redundant with the legacyShim:
//
//   - the SHIM turns double coverage into a compile error, but ONLY when the two
//     candidates tie at the same depth. A domain aggregator that DECLARED a
//     handler method itself would sit at depth-1 and silently beat the shimmed
//     legacy method at depth-2 — green build, dead legacy duplicate.
//   - this REFLECTION gate asks each embed directly "do you have this method?",
//     so it catches double coverage at ANY depth. It is strictly stronger.
//
// Migrating an op is one edit here: flip its row from legacyShim to the domain's
// embed. A row that disagrees with reality fails, in either direction.

// embedLegacy is the apiServer FIELD name of the legacy shim (the field name of
// an embedded type is its unqualified type name).
const embedLegacy = "legacyShim"

// opOwner maps every operation of the committed contract to the apiServer
// embedded FIELD NAME expected to supply it. P0: every op is still legacy.
var opOwner = map[string]string{
	"ApplyFiles":                    embedLegacy,
	"BuildProject":                  embedLegacy,
	"CollectExternalResourceValues": embedLegacy,
	"CreateIssue":                   embedLegacy,
	"CreateProject":                 embedLegacy,
	"CreateRcaAgentReport":          embedLegacy,
	"CreateSkill":                   embedLegacy,
	"CreateTurn":                    embedLegacy,
	"DeleteExternalResource":        embedLegacy,
	"DeleteProject":                 embedLegacy,
	"DeleteSkill":                   embedLegacy,
	"DisconnectGitProvider":         embedLegacy,
	"DiscoverIdp":                   embedLegacy,
	"GetActiveTurn":                 embedLegacy,
	"GetBuildLogs":                  embedLegacy,
	"GetBuildPreflight":             embedLegacy,
	"GetComponent":                  embedLegacy,
	"GetComponentConfig":            embedLegacy,
	"GetComponentOpenapi":           embedLegacy,
	"GetConfig":                     embedLegacy,
	"GetConversation":               embedLegacy,
	"GetDependencyStatus":           embedLegacy,
	"GetProject":                    embedLegacy,
	"GetProjectBuild":               embedLegacy,
	"GetProjectStatus":              embedLegacy,
	"GetRcaAgentReport":             embedLegacy,
	"GetSkill":                      embedLegacy,
	"GetSpecCollabSession":          embedLegacy,
	"GetTask":                       embedLegacy,
	"GetTurn":                       embedLegacy,
	"ImportSkill":                   embedLegacy,
	"ListAccessRequests":            embedLegacy,
	"ListBuilds":                    embedLegacy,
	"ListComponents":                embedLegacy,
	"ListDeployments":               embedLegacy,
	"ListExternalResources":         embedLegacy,
	"ListFiles":                     embedLegacy,
	"ListIssues":                    embedLegacy,
	"ListOrganizations":             embedLegacy,
	"ListPlatformResourceTypes":     embedLegacy,
	"ListProjectBuilds":             embedLegacy,
	"ListProjectTags":               embedLegacy,
	"ListProjects":                  embedLegacy,
	"ListRcaAgentReports":           embedLegacy,
	"ListSkillUpdates":              embedLegacy,
	"ListSkills":                    embedLegacy,
	"ListTasks":                     embedLegacy,
	"PromoteTaskFromIssue":          embedLegacy,
	"ProvisionPlatformResource":     embedLegacy,
	"ReadFile":                      embedLegacy,
	"RequestOrgServiceAccess":       embedLegacy,
	"RotateIdpClientSecret":         embedLegacy,
	"StartGitProviderConnect":       embedLegacy,
	"StreamTaskLog":                 embedLegacy,
	"StreamTurn":                    embedLegacy,
	"SyncSkills":                    embedLegacy,
	"TriggerBuild":                  embedLegacy,
	"UpdateComponentConfig":         embedLegacy,
	"UpdateConfig":                  embedLegacy,
	"UpdateSkill":                   embedLegacy,
	"ValidateCollabAccess":          embedLegacy,
}

// embedsProviding returns the names of apiServer's embedded fields whose method
// set contains op — the direct question the compiler's promotion rule answers
// only implicitly.
func embedsProviding(op string) []string {
	return embedsProvidingIn(reflect.TypeOf(apiServer{}), op)
}

// embedsProvidingIn is the detector itself, kept generic over the composite type
// so TestMethodOriginGateFires can aim it at a deliberately-broken shape and
// prove it actually reports double coverage.
func embedsProvidingIn(t reflect.Type, op string) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.Anonymous {
			continue // a named field promotes nothing
		}
		ft := f.Type
		if ft.Kind() != reflect.Pointer {
			ft = reflect.PointerTo(ft) // pointer-receiver methods are in *T's set
		}
		if _, ok := ft.MethodByName(op); ok {
			out = append(out, f.Name)
		}
	}
	sort.Strings(out)
	return out
}

// contractOps returns every operation gen.StrictServerInterface requires — the
// list is the contract's, not ours, so a new op in openapi.yaml shows up here
// the moment `make gen-api` runs.
func contractOps() []string {
	t := reflect.TypeOf((*gen.StrictServerInterface)(nil)).Elem()
	ops := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		ops = append(ops, t.Method(i).Name)
	}
	sort.Strings(ops)
	return ops
}

// TestMethodOrigin asserts every contract op is supplied by EXACTLY ONE embed,
// and that it is the expected one. This is the test that fails when a migration
// adds a handler to its domain but forgets to cut the legacy one (2 providers),
// removes it from both (0 providers), or moves it without updating the ledger.
func TestMethodOrigin(t *testing.T) {
	for _, op := range contractOps() {
		want, listed := opOwner[op]
		if !listed {
			t.Errorf("op %q has no opOwner row — add one naming the embed that serves it", op)
			continue
		}
		got := embedsProviding(op)
		switch {
		case len(got) == 0:
			t.Errorf("op %q: NO embed supplies it (the interface cannot be satisfied)", op)
		case len(got) > 1:
			t.Errorf("op %q: DOUBLE COVERAGE — supplied by %v. The migration added it to its "+
				"domain but did not cut the legacy method; delete the legacy one.", op, got)
		case got[0] != want:
			t.Errorf("op %q: served by %q, ledger says %q — update the opOwner row in the same "+
				"commit that moves the op", op, got[0], want)
		}
	}
}

// TestOpOwnerLedgerIsHonest asserts the ledger names exactly the contract's ops
// — no stale row survives an op's removal, no new op goes unlisted. Same
// honesty discipline as internal/arch's allowlists.
func TestOpOwnerLedgerIsHonest(t *testing.T) {
	ops := map[string]bool{}
	for _, op := range contractOps() {
		ops[op] = true
	}
	for op := range opOwner {
		if !ops[op] {
			t.Errorf("opOwner names %q, which is not an operation of the contract — remove the row", op)
		}
	}
	if len(opOwner) != len(ops) {
		t.Errorf("opOwner has %d rows, the contract has %d ops", len(opOwner), len(ops))
	}
}

// TestApiServerDeclaresNoMethods pins the promotion-only property the whole
// scheme rests on: if apiServer ever declared a handler method itself it would
// sit at depth-0 and silently beat EVERY embed, shim included.
func TestApiServerDeclaresNoMethods(t *testing.T) {
	for _, op := range contractOps() {
		m, ok := reflect.PointerTo(reflect.TypeOf(apiServer{})).MethodByName(op)
		if !ok {
			continue // TestMethodOrigin reports the missing-op case
		}
		// A promoted method's Index chain starts at the embedded field; a
		// method declared on apiServer itself has no embedded provider.
		if len(embedsProviding(op)) == 0 {
			t.Errorf("op %q (%v) is declared on apiServer itself — the edge composes, it never implements", op, m.Name)
		}
	}
}

// ── Proof that the nets fire ────────────────────────────────────────────────
//
// A green gate proves nothing on its own: these plant the exact mistakes the
// migration is expected to make and assert the gate reports them.

// plantedSlice stands in for a migrated slice handler. It declares a REAL
// contract op, so the shape below is byte-for-byte the shape P1+ produces.
type plantedSlice struct{}

func (plantedSlice) ListProjects(ctx context.Context, request gen.ListProjectsRequestObject) (gen.ListProjectsResponseObject, error) {
	return nil, nil
}

// plantedAggregator mirrors a domain's httpapi.Handlers: a pure aggregator that
// declares nothing and only embeds slice handlers.
type plantedAggregator struct{ plantedSlice }

// plantedDoubleCover is the migration slip: ListProjects was added to the domain
// but NOT cut from legacy. Both candidates sit at depth-2, which is why the real
// apiServer would refuse to compile — so the fixture is a separate type, and the
// detector is aimed at it directly.
type plantedDoubleCover struct {
	legacyShim
	plantedAggregator
}

// TestMethodOriginGateFires proves the double-coverage detector reports the slip
// the compiler CANNOT catch. The compiler only rejects a same-depth tie; an
// aggregator that declared its op directly would sit at depth-1 and silently
// beat the shimmed legacy method. The reflection gate asks every embed
// regardless of depth, so it catches both.
func TestMethodOriginGateFires(t *testing.T) {
	got := embedsProvidingIn(reflect.TypeOf(plantedDoubleCover{}), "ListProjects")
	if len(got) != 2 {
		t.Fatalf("planted double coverage went UNDETECTED: embedsProvidingIn = %v, want both "+
			"[legacyShim plantedAggregator] — the migration's headline net is not working", got)
	}
	// And it must name the culprits, not merely count them.
	if got[0] != "legacyShim" || got[1] != "plantedAggregator" {
		t.Fatalf("detector named %v, want [legacyShim plantedAggregator]", got)
	}
}

// TestMethodOriginGateAcceptsACorrectCut is the mirror: once legacy is cut, the
// op resolves to exactly one embed. Without this, a detector that always
// reported "2" would pass the test above.
func TestMethodOriginGateAcceptsACorrectCut(t *testing.T) {
	type correctCut struct {
		plantedAggregator // legacy method cut -> only the domain supplies it
	}
	if got := embedsProvidingIn(reflect.TypeOf(correctCut{}), "ListProjects"); len(got) != 1 {
		t.Fatalf("a correctly-cut op reported %v, want exactly one provider", got)
	}
}
