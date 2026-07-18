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

package validation

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/models"
)

// ---- fakes ------------------------------------------------------------------

type fakeIssues struct {
	existing []sourcecontrol.IssueInfo
	created  []sourcecontrol.CreateIssueRequest
}

func (f *fakeIssues) ListIssues(_ context.Context, _, _ string, _ []string) ([]sourcecontrol.IssueInfo, error) {
	return f.existing, nil
}

func (f *fakeIssues) CreateIssue(_ context.Context, _, _ string, req sourcecontrol.CreateIssueRequest) (*sourcecontrol.IssueResult, error) {
	f.created = append(f.created, req)
	return &sourcecontrol.IssueResult{Number: 42, URL: "https://example/issues/42"}, nil
}

type fakeDesign struct{ names []string }

func (f fakeDesign) ReadDesignComponents(_ context.Context, _, _ string) ([]models.DesignComponent, error) {
	comps := make([]models.DesignComponent, len(f.names))
	for i, n := range f.names {
		comps[i] = models.DesignComponent{Name: n}
	}
	return comps, nil
}

type fakeCriteria struct {
	raw   []byte
	found bool
}

func (f fakeCriteria) ReadValidationCriteria(_ context.Context, _, _ string) ([]byte, bool, error) {
	return f.raw, f.found, nil
}

const sampleCriteria = `{
  "requirements": [
    { "id": "REQ-001", "statement": "Greets by name",
      "criteria": [
        { "id": "AC-001-a", "must": "A text box is visible", "method": "e2e" },
        { "id": "AC-001-b", "must": "Says Hello, name", "method": "e2e" }
      ] },
    { "id": "REQ-002", "statement": "Copy is clear",
      "criteria": [ { "id": "AC-002-a", "must": "Greeting is friendly", "method": "manual" } ] }
  ]
}`

func newSvc(iss *fakeIssues, names []string, crit fakeCriteria) *Service {
	return NewService(Deps{Issues: iss, Design: fakeDesign{names: names}, Criteria: crit})
}

// ---- tests ------------------------------------------------------------------

func TestEnsureValidationIssue_CreatesFormattedIssue(t *testing.T) {
	iss := &fakeIssues{}
	svc := newSvc(iss, []string{"hello-web", "hello-api"}, fakeCriteria{raw: []byte(sampleCriteria), found: true})

	if err := svc.EnsureValidationIssue(context.Background(), "org", "proj", "design-v3"); err != nil {
		t.Fatalf("EnsureValidationIssue: %v", err)
	}
	if len(iss.created) != 1 {
		t.Fatalf("want 1 created issue, got %d", len(iss.created))
	}
	got := iss.created[0]

	// Labels: marker + validation class + spec-plan origin. No aep:execute —
	// dispatch is driven by the dev workflow's validating phase, not the sweep.
	// Carries the aep:spec/<tag> label (flat mirror of block.SpecTag) so the
	// validation Task is filterable by version like coding Tasks.
	wantLabels := []string{"aep:task", "aep:validation", "aep:origin/spec-plan", "aep:spec/design-v3"}
	if !reflect.DeepEqual(got.Labels, wantLabels) {
		t.Errorf("labels = %v; want %v", got.Labels, wantLabels)
	}
	if got.Title != validationTitle {
		t.Errorf("title = %q; want %q", got.Title, validationTitle)
	}

	// Machine block: project-scoped identity (operation "validate"), dependsOn
	// every component, lineage designTag.
	block, human, err := taskmeta.ParseBody(got.Body)
	if err != nil {
		t.Fatalf("ParseBody: %v", err)
	}
	if block.Operation != "validate" || block.Component != "" {
		t.Errorf("block identity = {component:%q operation:%q}; want operation=validate", block.Component, block.Operation)
	}
	if !reflect.DeepEqual(block.DependsOn, []string{"hello-web", "hello-api"}) {
		t.Errorf("dependsOn = %v; want [hello-web hello-api]", block.DependsOn)
	}
	if block.DesignTag != "design-v3" {
		t.Errorf("designTag = %q; want design-v3", block.DesignTag)
	}
	if block.SpecTag != "design-v3" {
		t.Errorf("specTag = %q; want design-v3", block.SpecTag)
	}
	if block.Key == "" {
		t.Error("block Key must be set for dedup")
	}

	// Human body: the consumer-contract sections the aep-validation skill reads,
	// and NO deployed-endpoints/credentials (those come from validation-context).
	for _, want := range []string{"## Acceptance oracle", "## Test layout", "## Report", "AC-001-a", "specs/validation/validation-criteria.json"} {
		if !strings.Contains(human.Body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(human.Body, "## Deployed endpoints") {
		t.Error("body must NOT carry a Deployed endpoints section (runner fetches endpoints from validation-context)")
	}
	// e2e count reflects the oracle (2 e2e). Coverage is no longer a field —
	// it is derived from committed-spec presence, so the oracle summary just
	// counts by method.
	if !strings.Contains(human.Body, "`e2e` — 2 criteria") {
		t.Errorf("acceptance-oracle counts wrong; body:\n%s", human.Body)
	}
}

func TestEnsureValidationIssue_DedupSkipsWhenOpenExists(t *testing.T) {
	iss := &fakeIssues{existing: []sourcecontrol.IssueInfo{
		{Number: 7, State: "open", Labels: []string{"aep:task", "aep:validation", "aep:origin/spec-plan"}},
	}}
	svc := newSvc(iss, []string{"hello-web"}, fakeCriteria{raw: []byte(sampleCriteria), found: true})

	if err := svc.EnsureValidationIssue(context.Background(), "org", "proj", "design-v3"); err != nil {
		t.Fatalf("EnsureValidationIssue: %v", err)
	}
	if len(iss.created) != 0 {
		t.Fatalf("dedup failed: created %d issues, want 0", len(iss.created))
	}
}

func TestEnsureValidationIssue_SkipsWhenCriteriaAbsent(t *testing.T) {
	iss := &fakeIssues{}
	svc := newSvc(iss, []string{"hello-web"}, fakeCriteria{found: false})

	if err := svc.EnsureValidationIssue(context.Background(), "org", "proj", "design-v3"); err != nil {
		t.Fatalf("EnsureValidationIssue: %v", err)
	}
	if len(iss.created) != 0 {
		t.Fatalf("want no issue when criteria file absent, got %d", len(iss.created))
	}
}

func TestEnsureValidationIssue_SkipsWhenCriteriaMalformed(t *testing.T) {
	iss := &fakeIssues{}
	svc := newSvc(iss, []string{"hello-web"}, fakeCriteria{raw: []byte(`{"requirements": []}`), found: true})

	if err := svc.EnsureValidationIssue(context.Background(), "org", "proj", "design-v3"); err != nil {
		t.Fatalf("EnsureValidationIssue (malformed should skip, not error): %v", err)
	}
	if len(iss.created) != 0 {
		t.Fatalf("want no issue when criteria empty/malformed, got %d", len(iss.created))
	}
}
