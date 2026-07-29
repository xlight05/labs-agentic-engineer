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
type TaskView = components["schemas"]["TaskView"];

// Router stubbed to plain anchors — no RouterProvider needed. createLink is
// what the gate hold's deep link uses, so it has to survive the stub.
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children?: React.ReactNode }) => <a>{children}</a>,
  createLink: (Component: React.ElementType) =>
    ({
      to,
      params,
      search,
      children,
      ...rest
    }: {
      to: string;
      params?: Record<string, string>;
      search?: Record<string, string>;
      children?: React.ReactNode;
    }) => {
      const path = Object.entries(params ?? {}).reduce(
        (acc, [k, v]) => acc.replace(`$${k}`, v),
        to,
      );
      const query = new URLSearchParams(search ?? {}).toString();
      return (
        <Component {...rest} component="a" href={query ? `${path}?${query}` : path}>
          {children}
        </Component>
      );
    },
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

// The build sessions open an SSE stream and read the cluster for builds; stub
// the whole block and record what it was handed, so this file stays about the
// PAGE (version selection, run cards, cancel) — RunSpine has its own test.
vi.mock("./RunSpine", () => ({
  RunSpine: ({
    run,
    work,
    gates,
  }: {
    run: MilestoneRunView;
    work: TaskView[];
    gates: TaskView[];
  }) => (
    <div
      data-testid="run-spine"
      data-work={work.map((t) => t.issueNumber).join(",")}
      data-gates={gates.map((t) => t.issueNumber).join(",")}
    >
      {run.cycles.map((c) => `${c.kind}:${c.branch ?? ""}:${c.mergeSha ?? ""}`).join("|")}
    </div>
  ),
}));

// The issue plane the run card reads to tell its holds apart. `undefined` is
// the list not having arrived yet, which is a different thing from an empty
// milestone.
let mockIssues: TaskView[] | undefined = [];
vi.mock("../../tasks/api/queries", () => ({
  useAllTasks: () => ({ data: mockIssues }),
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
  useCycleBuilds: () => ({ data: [], isPending: false }),
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

function issue(
  number: number,
  title: string,
  executorClass: string,
  derivedStatus = "pending",
): TaskView {
  return {
    issueNumber: number,
    title,
    executorClass,
    derivedStatus,
    issueUrl: `https://github.com/o/r/issues/${number}`,
    executions: {},
  } as TaskView;
}

// One open coding issue: a milestone with work in it, so a waiting run's hold
// is the unbounded park rather than an empty working set.
const withOpenWork = () => [issue(2, "Implement the shortener API", "coding")];

afterEach(() => {
  mockBuilds = [];
  mockRuns = [];
  mockIssues = [];
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

  it("lands on the run's build sessions, handed the facts webhooks taught it", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run()];
    mockIssues = withOpenWork();
    renderPage();

    // Which sessions the page hands down, and the learned facts riding on them —
    // the rendering of a session is RunSpine's own test.
    expect(screen.getByTestId("run-spine")).toHaveTextContent(
      "coding:aep/m2-c1:dcb1edc5fe04|fix::",
    );
    // Plus the milestone's agent work, which is what a session attributes its
    // issues from. WHOLE, not narrowed to the open ones: a settled session's
    // issues were closed by the very merge that completed it.
    expect(screen.getByTestId("run-spine").dataset.work).toBe("2");
  });

  it("shows no budget counters on a healthy run — unspent allowance is not the user's business", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run()]; // 2/8 cycles, 1/2 fix cycles — nothing at its ceiling
    renderPage();
    expect(screen.queryByText("2 / 8")).not.toBeInTheDocument();
    expect(screen.queryByText("1 / 2")).not.toBeInTheDocument();
  });

  it("surfaces a budget counter once it is spent, next to the reason it explains", () => {
    mockBuilds = [build("v2", "failed")];
    mockRuns = [
      run({
        state: "failed",
        terminalReason: "cycle-ceiling",
        budgets: {
          cyclesTotal: 8,
          cycleCeiling: 8,
          fixCycles: 1,
          conflictCycles: 0,
          buildRetriggers: 0,
        },
      }),
    ];
    renderPage();
    // Rendered as one line next to the terminal reason: "Budget spent: …".
    expect(screen.getByText(/Budget spent: Build sessions 8 \/ 8/)).toBeInTheDocument();
    // The fix-session allowance is still unspent, so it is not listed.
    expect(screen.queryByText(/Fix sessions/)).not.toBeInTheDocument();
  });

  it("makes cancel PROMINENT and explained on a waiting run", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run({ state: "waiting" })];
    mockIssues = withOpenWork();
    renderPage();

    expect(screen.getByText("Waiting")).toBeInTheDocument();
    expect(screen.getByText(/wait is unbounded/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Cancel run/ }));
    expect(cancelMutate).toHaveBeenCalledWith("run-1");
  });

  // The reported bug: a build busy writing its milestone announced itself as
  // parked, with a cancel button, on a project that had no gates at all.
  it("reads a planning run as work in progress, not as a hold", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run({ state: "planning", cycles: [] })];
    mockIssues = [];
    renderPage();

    expect(screen.getByText("Planning")).toBeInTheDocument();
    expect(screen.getByText("Planning v2")).toBeInTheDocument();
    expect(screen.getByText(/Nothing is held/)).toBeInTheDocument();
    expect(screen.queryByText(/wait is unbounded/)).not.toBeInTheDocument();
    // Cancel is a signal to a supervisor that does not exist yet: offering it
    // would 202 and do nothing.
    expect(
      screen.queryByRole("button", { name: /Cancel run/ }),
    ).not.toBeInTheDocument();
    // And no empty session rail under a notice that already explained itself.
    expect(screen.queryByTestId("run-spine")).not.toBeInTheDocument();
  });

  // The gate story is the PROVISIONING STAGE on the run's rail — RunSpine's own
  // test owns how it reads. What the PAGE owes is handing the gates down at all,
  // resolved ones included: they are the version's record.
  it("hands the run's rail every gate in the milestone", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run({ state: "waiting" })];
    mockIssues = [
      issue(1, "Provide configuration: url-shortener-db", "provision"),
      issue(3, "Provision resource: cache", "provision", "merged"),
      ...withOpenWork(),
    ];
    renderPage();

    expect(screen.getByTestId("run-spine")).toHaveAttribute("data-gates", "1,3");
    // A gate hold is NOT the unbounded park — the fix for one is nothing like
    // the fix for the other, so the notice must not claim it.
    expect(screen.queryByText(/wait is unbounded/)).not.toBeInTheDocument();
  });

  it("tells an empty milestone apart from a gate hold", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run({ state: "waiting" })];
    mockIssues = [issue(2, "Add the redirect handler", "coding", "merged")];
    renderPage();

    expect(screen.getByText("Waiting for work")).toBeInTheDocument();
    expect(screen.queryByText(/unresolved connection/)).not.toBeInTheDocument();
  });

  it("accuses no run of having no work before the issue list arrives", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run({ state: "waiting" })];
    mockIssues = undefined;
    renderPage();
    expect(screen.queryByText("Waiting for work")).not.toBeInTheDocument();
  });

  // A resolved gate holds nothing, so it must not keep explaining a hold.
  it("drops the hold once every gate is resolved", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run({ state: "waiting" })];
    mockIssues = [
      issue(1, "Provide configuration: db", "provision", "merged"),
      ...withOpenWork(),
    ];
    renderPage();
    expect(screen.queryByText(/unresolved/)).not.toBeInTheDocument();
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
    mockIssues = withOpenWork();
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
