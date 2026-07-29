/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { components } from "../../../generated/aep-api";

type MilestoneRunView = components["schemas"]["MilestoneRunView"];
type RunValidation = components["schemas"]["RunValidation"];

// Router replaced so the PageHeader back-link renders as a plain anchor — no
// RouterProvider needed (mirrors DeploymentsPage.test.tsx / NotFound.test.tsx).
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children?: React.ReactNode }) => <a>{children}</a>,
}));

// The live log is the RUN feed filtered to the validation cycle, and it opens
// an SSE stream. Stub it to a marker so we can assert which lifecycle states
// show the log vs. the report, without a stream.
vi.mock("../../builds/components/RunFeed", () => ({
  RunFeed: ({ cycleKinds }: { cycleKinds?: readonly string[] }) => (
    <div data-testid="run-feed">{(cycleKinds ?? []).join(",")}</div>
  ),
}));

// Controllable status + runs + file queries (no QueryClientProvider / MSW).
let mockValidation = "none";
let mockRun: MilestoneRunView | undefined;

function run(over: {
  validation?: RunValidation;
  cycles?: MilestoneRunView["cycles"];
}): MilestoneRunView {
  return {
    id: "run-1",
    milestoneNumber: 1,
    milestoneTitle: "v1",
    origin: "spec-build",
    state: "succeeded",
    budgets: {
      cyclesTotal: 2,
      cycleCeiling: 8,
      fixCycles: 0,
      conflictCycles: 0,
      buildRetriggers: 0,
    },
    validation: over.validation ?? {},
    cycles: over.cycles ?? [],
    createdAt: "2026-07-10T09:00:00Z",
  };
}

const validationCycle = {
  id: "cycle-2",
  kind: "validation" as const,
  attempts: 1,
  prNumber: 42,
  // The host's own page, as the webhook reported it. Deliberately NOT
  // `${repoUrl}/pull/42`: repoUrl is a clone URL, and this page used to compose
  // one from it — which 404s the moment the clone URL carries a `.git` suffix.
  prUrl: "https://github.com/acme/demo/pull/42",
  createdAt: "2026-07-10T10:00:00Z",
};

const mockCriteria = {
  isPending: false,
  isError: false,
  error: null,
  refetch: vi.fn(),
  data: undefined as { content: string } | undefined,
};
const mockReport = {
  isPending: false,
  isError: false,
  error: null,
  refetch: vi.fn(),
  data: undefined as { content: string } | undefined,
};

vi.mock("../../projects/api/queries", () => ({
  useProjectStatus: () => ({
    isPending: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
    data: {
      repoUrl: "https://github.com/acme/demo",
      deploy: { version: "v1", validation: mockValidation },
    },
  }),
}));

vi.mock("../../builds/api/queries", () => ({
  useBuildRuns: () => ({
    isPending: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
    data: { tag: "v1", milestoneNumber: 1, runs: mockRun ? [mockRun] : [] },
  }),
}));

vi.mock("../api/queries", () => ({
  useValidationCriteria: () => mockCriteria,
  useValidationReport: () => mockReport,
}));

import { ValidationPage } from "./ValidationPage";

const CRITERIA = JSON.stringify({
  requirements: [
    {
      id: "REQ-001",
      statement: "Shoppers can search the catalog.",
      criteria: [
        { id: "AC-001-a", must: "Search returns matches", method: "e2e" },
        { id: "AC-001-b", must: "Category filter works", method: "e2e" },
        { id: "AC-003-b", must: "Payment is encrypted", method: "manual" },
      ],
    },
  ],
});

const REPORT = JSON.stringify({
  criteria: [
    { id: "AC-001-a", status: "pass" },
    {
      id: "AC-001-b",
      status: "fail",
      spec: "tests/e2e/specs/AC-001-b.spec.ts",
      failure: "TimeoutError: category option never appeared",
    },
    { id: "AC-003-b", status: "manual" },
  ],
});

function renderPage(view: "logs" | undefined, onViewChange = vi.fn()) {
  render(
    <ValidationPage
      projectName="acme"
      view={view}
      onViewChange={onViewChange}
    />,
  );
  return onViewChange;
}

afterEach(() => {
  mockValidation = "none";
  mockRun = undefined;
  mockCriteria.isPending = false;
  mockCriteria.isError = false;
  mockCriteria.data = undefined;
  mockReport.isError = false;
  mockReport.data = undefined;
});

describe("ValidationPage lifecycle", () => {
  it("shows an empty state when the version's run never reached validation", () => {
    mockRun = run({});
    renderPage(undefined);
    expect(screen.getByText(/No validation has run yet/)).toBeInTheDocument();
    expect(screen.queryByTestId("run-feed")).not.toBeInTheDocument();
  });

  it("shows an empty state when the version has no run rows at all", () => {
    renderPage(undefined);
    expect(screen.getByText(/No validation has run yet/)).toBeInTheDocument();
  });

  it("shows the validation cycle's feed while the run is validating", () => {
    mockValidation = "running";
    mockRun = run({ cycles: [validationCycle] });
    renderPage(undefined);
    // The feed streams the WHOLE run; the page filters it to the one phase it
    // owns, so a coding cycle's output never leaks onto the validation page.
    expect(screen.getByTestId("run-feed")).toHaveTextContent("validation");
  });

  it("shows the feed for a run whose validation failed", () => {
    mockValidation = "failed";
    mockRun = run({
      validation: { verdict: "failed" },
      cycles: [validationCycle],
    });
    mockCriteria.data = { content: CRITERIA };
    renderPage("logs");
    expect(screen.getByTestId("run-feed")).toHaveTextContent("validation");
    // A failed verdict still committed a report, so the toggle back exists.
    expect(screen.getByRole("button", { name: /View report/ })).toBeTruthy();
    expect(screen.getByText("Validation failed")).toBeInTheDocument();
  });

  it("says so, and shows nothing else, when the run SKIPPED validation", () => {
    mockRun = run({ validation: { verdict: "skipped" } });
    renderPage(undefined);
    expect(screen.getByText(/was not validated/)).toBeInTheDocument();
    expect(screen.queryByTestId("run-feed")).not.toBeInTheDocument();
  });

  it("renders the joined report on a passed verdict", () => {
    mockValidation = "completed";
    mockRun = run({
      validation: { verdict: "passed", reportPath: "tests/validation/report.json" },
      cycles: [validationCycle],
    });
    mockCriteria.data = { content: CRITERIA };
    mockReport.data = { content: REPORT };
    renderPage(undefined);

    // The report, not the log.
    expect(screen.queryByTestId("run-feed")).not.toBeInTheDocument();
    expect(screen.getByText("Shoppers can search the catalog.")).toBeInTheDocument();
    // Per-criterion state chips from the join.
    expect(screen.getByText("Passed")).toBeInTheDocument();
    expect(screen.getByText("Failed")).toBeInTheDocument();
    expect(screen.getByText("Manual")).toBeInTheDocument();
    // Rich failure detail for the failing e2e criterion.
    expect(
      screen.getByText(/category option never appeared/),
    ).toBeInTheDocument();
  });

  it("stamps the run's verdict on the header, not the coarse lifecycle", () => {
    mockValidation = "completed";
    mockRun = run({
      validation: { verdict: "passed" },
      cycles: [validationCycle],
    });
    mockCriteria.data = { content: CRITERIA };
    renderPage(undefined);
    expect(screen.getByText("Validation passed")).toBeInTheDocument();
  });

  it("links the validation cycle's PR, learned from the cycle record", () => {
    mockValidation = "completed";
    mockRun = run({
      validation: { verdict: "passed" },
      cycles: [validationCycle],
    });
    mockCriteria.data = { content: CRITERIA };
    renderPage(undefined);
    expect(
      screen.getByRole("link", { name: /Validation pull request/ }),
    ).toHaveAttribute("href", "https://github.com/acme/demo/pull/42");
  });

  it("toggles to the log view via the View logs button", () => {
    mockValidation = "completed";
    mockRun = run({
      validation: { verdict: "passed" },
      cycles: [validationCycle],
    });
    mockCriteria.data = { content: CRITERIA };
    mockReport.data = { content: REPORT };
    const onViewChange = renderPage(undefined);

    fireEvent.click(screen.getByRole("button", { name: /View logs/ }));
    expect(onViewChange).toHaveBeenCalledWith("logs");
  });

  it("shows the feed (and a View report button) when ?view=logs", () => {
    mockValidation = "completed";
    mockRun = run({
      validation: { verdict: "passed" },
      cycles: [validationCycle],
    });
    mockCriteria.data = { content: CRITERIA };
    mockReport.data = { content: REPORT };
    const onViewChange = renderPage("logs");

    expect(screen.getByTestId("run-feed")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /View report/ }));
    expect(onViewChange).toHaveBeenCalledWith(undefined);
  });

  it("falls back to criteria-only with a note when the report is missing", () => {
    mockValidation = "completed";
    mockRun = run({
      validation: { verdict: "passed" },
      cycles: [validationCycle],
    });
    mockCriteria.data = { content: CRITERIA };
    mockReport.isError = true;
    renderPage(undefined);

    expect(screen.getByText(/report wasn't found/)).toBeInTheDocument();
    expect(screen.getByText("Shoppers can search the catalog.")).toBeInTheDocument();
    // No state chips without a report.
    expect(screen.queryByText("Passed")).not.toBeInTheDocument();
  });
});
