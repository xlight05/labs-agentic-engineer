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

type BuildSummary = components["schemas"]["BuildSummary"];
type MilestoneRunView = components["schemas"]["MilestoneRunView"];

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children?: React.ReactNode }) => <a>{children}</a>,
}));

// The settle-fetch effect is the only thing the page needs a client for.
const invalidateQueries = vi.fn();
vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries }),
}));

// The issue list is its own tested surface; here it is a marker that proves
// which liveness the page hands down.
vi.mock("../../tasks/components/IssueSections", () => ({
  IssueSections: ({ tag, live }: { tag: string; live: boolean }) => (
    <div data-testid="issues">{`${tag}:${String(live)}`}</div>
  ),
}));

// The feed opens an SSE stream; stub it and record whether it was mounted.
vi.mock("./RunFeed", () => ({
  RunFeed: ({ runId }: { runId: string }) => (
    <div data-testid="run-feed">{runId}</div>
  ),
}));

let mockBuilds: BuildSummary[] = [];
let mockRuns: MilestoneRunView[] = [];
const cancelMutate = vi.fn();
const cancelState = { isPending: false, isError: false, error: null as unknown };

vi.mock("../api/queries", () => ({
  useBuilds: () => ({
    data: mockBuilds,
    isPending: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  }),
  useBuildRuns: () => ({
    data: { tag: "v2", milestoneNumber: 2, runs: mockRuns },
    isPending: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  }),
  useCancelRun: () => ({ mutate: cancelMutate, ...cancelState }),
}));

import { BuildsPage } from "./BuildsPage";

function build(tag: string, status: BuildSummary["status"]): BuildSummary {
  return {
    tag,
    milestoneNumber: Number(tag.slice(1)),
    status,
    startedAt: "2026-07-10T09:00:00Z",
  };
}

function run(over: Partial<MilestoneRunView> = {}): MilestoneRunView {
  return {
    id: "run-1",
    milestoneNumber: 2,
    milestoneTitle: "v2",
    origin: "spec-build",
    state: "running",
    budgets: {
      cyclesTotal: 2,
      cycleCeiling: 8,
      fixCycles: 1,
      conflictCycles: 0,
      buildRetriggers: 0,
    },
    validation: {},
    cycles: [
      {
        id: "cycle-1",
        kind: "coding",
        attempts: 1,
        branch: "aep/m2-c1",
        prNumber: 3,
        mergeSha: "dcb1edc5fe04",
        createdAt: "2026-07-10T09:05:00Z",
        endedAt: "2026-07-10T09:40:00Z",
      },
      {
        id: "cycle-2",
        kind: "fix",
        attempts: 2,
        createdAt: "2026-07-10T09:45:00Z",
      },
    ],
    createdAt: "2026-07-10T09:00:00Z",
    ...over,
  };
}

afterEach(() => {
  mockBuilds = [];
  mockRuns = [];
  cancelState.isPending = false;
  cancelState.isError = false;
  cancelState.error = null;
  cancelMutate.mockClear();
  invalidateQueries.mockClear();
});

function renderPage(tag?: string, onTagChange = vi.fn()) {
  render(
    <BuildsPage projectName="acme" tag={tag} onTagChange={onTagChange} />,
  );
  return onTagChange;
}

describe("BuildsPage — one version's story", () => {
  it("invites the first build when there is none", () => {
    renderPage();
    expect(screen.getByText(/No builds yet/)).toBeInTheDocument();
  });

  it("defaults to the newest version, not to a ledger list", () => {
    mockBuilds = [build("v2", "in_progress"), build("v1", "completed")];
    mockRuns = [run()];
    renderPage();

    // The newest version's run is the page — no intermediate list of versions.
    expect(screen.getByText("v2")).toBeInTheDocument();
    expect(screen.getByTestId("issues")).toHaveTextContent("v2:true");
  });

  it("falls back to newest for an unknown ?tag rather than erroring", () => {
    mockBuilds = [build("v2", "completed"), build("v1", "completed")];
    mockRuns = [run({ state: "succeeded" })];
    renderPage("v99");
    expect(screen.getByTestId("issues")).toHaveTextContent("v2:false");
  });

  it("lands directly on the live run with its feed already open", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run({ state: "running" })];
    renderPage();
    expect(screen.getByTestId("run-feed")).toHaveTextContent("run-1");
  });

  it("leaves the feed closed on a settled version", () => {
    mockBuilds = [build("v2", "completed")];
    mockRuns = [run({ state: "succeeded" })];
    renderPage();
    expect(screen.queryByTestId("run-feed")).not.toBeInTheDocument();
  });

  it("renders the cycle timeline with the facts webhooks taught it", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run()];
    renderPage();

    expect(screen.getByText("Cycle 1 · coding")).toBeInTheDocument();
    expect(screen.getByText("Cycle 2 · fix")).toBeInTheDocument();
    expect(screen.getByText("aep/m2-c1")).toBeInTheDocument();
    expect(screen.getByText("#3")).toBeInTheDocument();
    expect(screen.getByText("dcb1edc5")).toBeInTheDocument();
    // A re-dispatched cycle shows its per-cycle budget.
    expect(screen.getByText("attempt 2/2")).toBeInTheDocument();
  });

  it("renders the budget counters", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run()];
    renderPage();
    expect(screen.getByText("2 / 8")).toBeInTheDocument();
    expect(screen.getByText("1 / 2")).toBeInTheDocument();
  });

  it("makes cancel PROMINENT and explained on a waiting run", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run({ state: "waiting" })];
    renderPage();

    expect(screen.getByText("Waiting")).toBeInTheDocument();
    expect(screen.getByText(/wait is unbounded/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Cancel run/ }));
    expect(cancelMutate).toHaveBeenCalledWith("run-1");
  });

  it("offers no cancel on a terminal run", () => {
    mockBuilds = [build("v2", "failed")];
    mockRuns = [run({ state: "failed", terminalReason: "no-progress" })];
    renderPage();

    expect(
      screen.queryByRole("button", { name: /Cancel run/ }),
    ).not.toBeInTheDocument();
    // …and says WHY it stopped, in words.
    expect(screen.getByText(/closed no issues/)).toBeInTheDocument();
  });

  it("says nothing was cancelled when the engine is unreachable", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run({ state: "waiting" })];
    cancelState.isError = true;
    cancelState.error = new Error("workflow engine unavailable");
    renderPage();
    expect(screen.getByText(/Nothing was cancelled/)).toBeInTheDocument();
  });

  it("tells every run of the milestone, newest first", () => {
    // A milestone sees SEQUENTIAL runs: the spec build, then an incident.
    mockBuilds = [build("v2", "completed")];
    mockRuns = [
      run({ id: "run-2", origin: "incident-adoption", state: "succeeded" }),
      run({ id: "run-1", state: "succeeded" }),
    ];
    renderPage();
    expect(screen.getByText("Incident")).toBeInTheDocument();
    expect(screen.getByText("Spec build")).toBeInTheDocument();
  });

  it("explains a version tagged before the platform kept run rows", () => {
    mockBuilds = [build("v2", "completed")];
    mockRuns = [];
    renderPage();
    expect(screen.getByText(/no run rows/)).toBeInTheDocument();
  });

  it("re-reads the issue list exactly once when the run settles", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run({ state: "running" })];
    const { rerender } = render(
      <BuildsPage projectName="acme" tag={undefined} onTagChange={vi.fn()} />,
    );
    expect(invalidateQueries).not.toHaveBeenCalled();

    // The run turns terminal: the GitHub-backed list has stopped polling, but
    // the writes that settle a version can land in the same instant.
    mockRuns = [run({ state: "succeeded" })];
    rerender(
      <BuildsPage projectName="acme" tag={undefined} onTagChange={vi.fn()} />,
    );
    expect(invalidateQueries).toHaveBeenCalledTimes(1);

    // A further render on the same settled state must not re-read.
    rerender(
      <BuildsPage projectName="acme" tag={undefined} onTagChange={vi.fn()} />,
    );
    expect(invalidateQueries).toHaveBeenCalledTimes(1);
  });
});
