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

// The cycle sections open an SSE stream and read the cluster for builds; stub
// the whole block and record the cycles it was handed, so this file stays about
// the PAGE (version selection, run cards, cancel) — CycleSections has its own.
vi.mock("./CycleSections", () => ({
  CycleSections: ({ run }: { run: MilestoneRunView }) => (
    <div data-testid="cycle-sections">
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

// A gate with a provisioning run in flight against it — the platform is
// standing the dependency up and will close the gate itself.
function provisioningGate(number: number, title: string): TaskView {
  return {
    ...issue(number, title, "provision"),
    executions: {
      provision: {
        id: `x${number}`,
        kind: "provision",
        status: "running",
        createdAt: "2026-07-10T09:01:00Z",
      },
    },
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

  it("lands on the run's cycles, handed the facts webhooks taught it", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run()];
    renderPage();

    // Which cycles the page hands down, and the learned facts riding on them —
    // the rendering of a cycle is CycleSections' own test.
    expect(screen.getByTestId("cycle-sections")).toHaveTextContent(
      "coding:aep/m2-c1:dcb1edc5fe04|fix::",
    );
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
    expect(screen.getByText(/Budget spent: Cycles 8 \/ 8/)).toBeInTheDocument();
    // The fix-cycle allowance is still unspent, so it is not listed.
    expect(screen.queryByText(/Fix cycles/)).not.toBeInTheDocument();
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
    // And no empty cycle section under a notice that already explained itself.
    expect(screen.queryByTestId("cycle-sections")).not.toBeInTheDocument();
  });

  it("names the gates holding a waiting run, and where to release them", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run({ state: "waiting" })];
    mockIssues = [
      issue(1, "Provide configuration: url-shortener-db", "provision"),
      ...withOpenWork(),
    ];
    renderPage();

    expect(screen.getByText(/Held by an unresolved connection/)).toBeInTheDocument();
    // The dependency is named, and the way out is a real navigation.
    expect(screen.getByText("url-shortener-db")).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /Resolve connections/ }),
    ).toHaveAttribute("href", "/projects/acme/spec?connections=open");
    // A gate hold is NOT the unbounded park — the fix for one is nothing like
    // the fix for the other.
    expect(screen.queryByText(/wait is unbounded/)).not.toBeInTheDocument();
  });

  // The reported bug: a postgres cluster and an identity app take ~5 minutes to
  // stand up, and the platform closes both gates the moment they are Ready. The
  // page called that "Held by 2 unresolved connections" and offered a button to
  // resolve them, sending the user after work that did not exist.
  it("reads gates the platform is provisioning as work in progress, not a hold", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run({ state: "waiting" })];
    mockIssues = [
      provisioningGate(1, "Provision resource: user-auth (thunder-app)"),
      provisioningGate(4, "Provision resource: ceramics-db (postgres-cnpg)"),
      ...withOpenWork(),
    ];
    renderPage();

    expect(screen.getByText("Provisioning 2 connections")).toBeInTheDocument();
    expect(screen.queryByText(/unresolved connection/)).not.toBeInTheDocument();
    // Nothing to go and do, so no way out is offered.
    expect(
      screen.queryByRole("link", { name: /Resolve connections/ }),
    ).not.toBeInTheDocument();
    // The dependencies are still named — that is what is being waited on.
    expect(screen.getByText("ceramics-db (postgres-cnpg)")).toBeInTheDocument();
  });

  it("keeps the way out when a gate is stalled with nothing driving it", () => {
    mockBuilds = [build("v2", "in_progress")];
    mockRuns = [run({ state: "waiting" })];
    mockIssues = [
      provisioningGate(1, "Provision resource: db"),
      issue(2, "Provide configuration: stripe", "provision"),
      ...withOpenWork(),
    ];
    renderPage();

    // The stalled one speaks over the one in flight, and only it is named.
    expect(screen.getByText(/Held by an unresolved connection/)).toBeInTheDocument();
    expect(screen.getByText("stripe")).toBeInTheDocument();
    expect(screen.queryByText("db")).not.toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /Resolve connections/ }),
    ).toHaveAttribute("href", "/projects/acme/spec?connections=open");
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
