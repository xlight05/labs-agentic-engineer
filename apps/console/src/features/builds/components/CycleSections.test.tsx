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
import type { RunProgressCycle, RunProgressPhase } from "../hooks/useRunProgress";

type MilestoneRunView = components["schemas"]["MilestoneRunView"];
type RunCycleView = components["schemas"]["RunCycleView"];

let mockCycles: RunProgressCycle[] = [];
let mockPhase: RunProgressPhase = "live";
// Every call's `enabled`, so a test can assert the stream was never opened.
let enabledCalls: boolean[] = [];

vi.mock("../hooks/useRunProgress", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../hooks/useRunProgress")>();
  return {
    ...actual,
    useRunProgress: (_p: string, _r: string | undefined, enabled = true) => {
      enabledCalls.push(enabled);
      return { cycles: mockCycles, phase: mockPhase, settledState: undefined };
    },
  };
});

// The builds block is its own surface (and its own cluster read); here it is a
// marker proving which cycle it was mounted under.
vi.mock("./CycleBuilds", () => ({
  CycleBuilds: ({ cycleId, mergeSha }: { cycleId: string; mergeSha: string }) => (
    <div data-testid="cycle-builds">{`${cycleId}:${mergeSha}`}</div>
  ),
}));

import { CycleSections } from "./CycleSections";

function cycle(over: Partial<RunCycleView> & { id: string }): RunCycleView {
  return {
    kind: "coding",
    attempts: 1,
    createdAt: "2026-07-10T09:00:00Z",
    ...over,
  } as RunCycleView;
}

function run(cycles: RunCycleView[]): MilestoneRunView {
  return {
    id: "run-1",
    milestoneNumber: 2,
    milestoneTitle: "v2",
    origin: "spec-build",
    state: "running",
    budgets: {
      cyclesTotal: cycles.length,
      cycleCeiling: 8,
      fixCycles: 0,
      conflictCycles: 0,
      buildRetriggers: 0,
    },
    validation: {},
    cycles,
    createdAt: "2026-07-10T09:00:00Z",
  };
}

function renderSections(r: MilestoneRunView) {
  render(<CycleSections projectName="acme" tag="v2" run={r} />);
}

afterEach(() => {
  mockCycles = [];
  mockPhase = "live";
  enabledCalls = [];
});

describe("CycleSections", () => {
  it("tells one cycle's whole story in one place — facts, agent, then builds", () => {
    const live = cycle({ id: "c1", branch: "aep/m2-c1", prNumber: 12, mergeSha: "4a91c2f8ab31" });
    mockCycles = [
      {
        cycle: live,
        lines: [
          {
            cycleId: "c1",
            cycleKind: "coding",
            cycleIndex: 1,
            kind: "git_push",
            emitter: "main",
            seq: 1,
            branch: "aep/m2-c1",
          },
        ],
      },
    ];
    renderSections(run([live]));

    expect(screen.getByText("Cycle 1 · coding")).toBeInTheDocument();
    expect(screen.getByText("aep/m2-c1")).toBeInTheDocument();
    expect(screen.getByText("#12")).toBeInTheDocument();
    expect(screen.getByText("4a91c2f8")).toBeInTheDocument();
    // The agent's own line, and the builds its merge produced, under the SAME
    // cycle — the whole point of the convergence.
    expect(screen.getByText("↑ push aep/m2-c1")).toBeInTheDocument();
    expect(screen.getByTestId("cycle-builds")).toHaveTextContent("c1:4a91c2f8ab31");
  });

  // Validation belongs to the deployment surface: the deployment is what gets
  // validated, and its verdict renders there.
  it("does not show the validation cycle", () => {
    const coding = cycle({ id: "c1", endedAt: "2026-07-10T09:40:00Z" });
    const validation = cycle({ id: "c2", kind: "validation" });
    renderSections(run([coding, validation]));

    expect(screen.getByText("Cycle 1 · coding")).toBeInTheDocument();
    expect(screen.queryByText(/validation/)).not.toBeInTheDocument();
  });

  it("opens on the cycle in flight — that is what the user came to watch", () => {
    const done = cycle({ id: "c1", endedAt: "2026-07-10T09:40:00Z" });
    const live = cycle({ id: "c2", kind: "fix" });
    renderSections(run([done, live]));

    // Exactly one builds block is mounted — the live cycle's. The settled
    // cycle is collapsed, and collapsed means unmounted, so it costs nothing.
    expect(screen.getByTestId("cycle-builds")).toHaveTextContent("c2:");
    expect(enabledCalls.at(-1)).toBe(true);
  });

  // The run-level "Show feed" toggle used to be what kept a finished version
  // from opening a connection. Per-cycle expansion has to keep that property.
  it("opens no stream on a settled run until a cycle is expanded", () => {
    const done = cycle({ id: "c1", endedAt: "2026-07-10T09:40:00Z" });
    renderSections(run([done]));

    expect(enabledCalls.every((e) => e === false)).toBe(true);

    fireEvent.click(screen.getByText("Cycle 1 · coding"));
    expect(enabledCalls.at(-1)).toBe(true);
  });

  it("says so when the run has not dispatched a cycle yet", () => {
    renderSections(run([]));
    expect(screen.getByText(/No cycle has been dispatched yet/)).toBeInTheDocument();
  });
});
