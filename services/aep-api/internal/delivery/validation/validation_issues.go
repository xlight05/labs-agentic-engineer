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
	"fmt"
	"log/slog"
	"strings"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// criteriaFilePath is the acceptance-oracle path in the project repo, authored
// by the validation-criteria skill and read by the runner.
const criteriaFilePath = "specs/validation/validation-criteria.json"

// validationTitle is the fixed title of the project's validation issue. Stable
// so a human reading the milestone always sees the same entry, and so the
// open-issue dedupe below never has to guess.
const validationTitle = "Validate the deployed system against its acceptance criteria"

// Service mints the project's validation Task issue. It holds only consumer
// ports; concrete providers are wired at the composition root.
type Service struct {
	issues   IssueClient
	criteria CriteriaReader
}

// Deps is the validation service's collaborator set.
type Deps struct {
	Issues   IssueClient
	Criteria CriteriaReader
}

// NewService wires the validation service from its collaborator set.
func NewService(d Deps) *Service {
	return &Service{issues: d.Issues, criteria: d.Criteria}
}

// EnsureValidationIssue mints the project's single aep:validation issue if the
// acceptance oracle exists and no open one does yet. It is idempotent: the run
// supervisor calls it at deployed-green, and a re-entered validation cycle must
// find the same issue rather than mint a second. A missing or malformed
// criteria file is a clean no-op — there is nothing to validate.
//
// The issue is PROSE with ONE label. It deliberately does NOT carry the `aep`
// working-set label: the validation cycle is dispatched at it by number, and
// working-set membership would hold the run's settle predicate open forever.
func (s *Service) EnsureValidationIssue(ctx context.Context, orgID, projectID, designTag string) error {
	raw, found, err := s.criteria.ReadValidationCriteria(ctx, orgID, projectID)
	if err != nil {
		return fmt.Errorf("validation: read criteria: %w", err)
	}
	if !found {
		// The design agent has not authored the oracle yet — a later planning
		// pass re-mints once it exists.
		return nil
	}
	doc, err := parseCriteria(raw)
	if err != nil {
		// A malformed oracle is the design agent's bug, not a reason to fail the
		// save; skip and let a corrected pass re-mint.
		slog.WarnContext(ctx, "validation: skipping mint — criteria file unusable", "project", projectID, "error", err)
		return nil
	}

	exists, err := s.hasOpenValidationIssue(ctx, orgID, projectID)
	if err != nil {
		return err
	}
	if exists {
		return nil // one validation issue per project until it terminates
	}

	req := sourcecontrol.CreateIssueRequest{
		Title:  validationTitle,
		Body:   rationale(doc.summarize()) + "\n\n" + renderScope(doc),
		Labels: []string{delivery.LabelValidationWork},
		// The version is the MILESTONE the caller files this under, not a label
		// and not a line in the body — the run supervisor's adapter assigns it.
		DedupeKey: "validation:" + projectID + ":" + designTag,
	}
	if _, cerr := s.issues.CreateIssue(ctx, orgID, projectID, req); cerr != nil {
		return fmt.Errorf("validation: create issue: %w", cerr)
	}
	slog.InfoContext(ctx, "validation: minted validation issue", "project", projectID, "designTag", designTag)
	return nil
}

// ResolveValidationTask ensures the project's validation issue exists
// (idempotent — it also covers acceptance criteria authored after the design
// was first approved) and returns its open issue number, or 0 when there are no
// criteria and nothing to validate. The run supervisor calls it at
// deployed-green, before dispatching the validation cycle.
func (s *Service) ResolveValidationTask(ctx context.Context, orgID, projectID, designTag string) (int, error) {
	if err := s.EnsureValidationIssue(ctx, orgID, projectID, designTag); err != nil {
		return 0, err
	}
	return s.findOpenValidationIssue(ctx, orgID, projectID)
}

// hasOpenValidationIssue reports whether an open aep:validation issue already
// exists for the project.
func (s *Service) hasOpenValidationIssue(ctx context.Context, orgID, projectID string) (bool, error) {
	number, err := s.findOpenValidationIssue(ctx, orgID, projectID)
	return number > 0, err
}

// findOpenValidationIssue returns the open aep:validation issue's number, or 0
// when none exists. The LABEL is the whole query — nothing parses a body.
func (s *Service) findOpenValidationIssue(ctx context.Context, orgID, projectID string) (int, error) {
	issues, err := s.issues.ListIssues(ctx, orgID, projectID, []string{delivery.LabelValidationWork})
	if err != nil {
		return 0, fmt.Errorf("validation: list issues: %w", err)
	}
	for _, issue := range issues {
		if strings.EqualFold(issue.State, "open") {
			return issue.Number, nil
		}
	}
	return 0, nil
}

// rationale is the one-line blockquote summary in the issue body.
func rationale(s criteriaSummary) string {
	return fmt.Sprintf("Validate the deployed system against %d e2e, %d scenario, and %d manual acceptance criteria.",
		s.E2E, s.Scenario, s.Manual)
}

// renderScope builds the human markdown body of the validation issue — the
// consumer contract the aep-validation skill reads (acceptance oracle + test
// layout + report). Deployed endpoints and test credentials are deliberately
// absent: the runner fetches endpoints from the secure validation-context
// endpoint and requests credentials on demand from the test-credentials
// endpoint, never from this public issue. Mirrors scripts/create-validation-issue.mjs
// renderBody minus the Deployed-endpoints section.
func renderScope(doc *criteriaDoc) string {
	sum := doc.summarize()
	var b strings.Builder
	w := func(lines ...string) {
		for _, l := range lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}

	w(
		"Validate the deployed system against its acceptance criteria: author end-to-end tests, run them against the deployed system, and open a PR containing the tests and a validation report.",
		"",
		"The deployed endpoint URLs and any test credentials are provided to the validation runner by the platform at dispatch time — they are not in this issue.",
		"",
		"## Acceptance oracle",
		fmt.Sprintf("The source of truth is `%s` in this repo. It is read-only input for this task — do not modify it or anything else under `specs/`.", criteriaFilePath),
		"",
		fmt.Sprintf("- `e2e` — %d criteria: a committed spec already at `tests/e2e/specs/<AC-ID>.spec.ts` runs as regression; author specs for the rest.", sum.E2E),
		fmt.Sprintf("- `manual` — %d criteria: render as an unchecked human checklist in the report.", sum.Manual),
		fmt.Sprintf("- `scenario` — %d criteria: out of scope for automation in this run; list as not-yet-validated in the report.", sum.Scenario),
		"",
	)

	for _, r := range doc.Requirements {
		w(fmt.Sprintf("### %s — %s", r.ID, r.Statement), "", "| Criterion | Method | Must |", "|---|---|---|")
		for _, c := range r.Criteria {
			w(fmt.Sprintf("| %s | %s | %s |", c.ID, c.Method, strings.ReplaceAll(c.Must, "|", "\\|")))
		}
		w("")
	}

	w(
		"Per-component design docs: `specs/design/components/<name>/design.md` (OpenAPI contract, when present, alongside as `openapi.yaml`); system overview: `specs/design/design.md`.",
		"",
		"## Test layout",
		"- Playwright package at repo root `tests/e2e/` (own `package.json`; do not touch application source under any component app path).",
		"- One spec file per criterion: `tests/e2e/specs/<AC-ID>.spec.ts`; test title MUST start with `<AC-ID>: ` — that prefix is the join key for the report.",
		"- UI criteria: browser specs (`@playwright/test`). API criteria: the built-in `request` fixture. Explore with `playwright-cli` first; never commit exploration sessions.",
		"",
		"## Report",
		"- Commit `tests/validation/report.md` (summary, per-criterion results, manual checklist, scenario not-yet-validated list) and `tests/validation/report.json`.",
		"- Post a summary comment on this issue when done.",
		"",
		"---",
		"Open one PR whose body includes `Closes #<this issue's number>` so the platform links it back. One PR; tests and report only.",
	)

	return strings.TrimRight(b.String(), "\n")
}
