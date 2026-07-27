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

import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RunProgressCycle, RunProgressPhase } from "../hooks/useRunProgress";

let mockCycles: RunProgressCycle[] = [];
let mockPhase: RunProgressPhase = "live";
let mockSettled: string | undefined;

vi.mock("../hooks/useRunProgress", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../hooks/useRunProgress")>();
  return {
    ...actual,
    useRunProgress: () => ({
      cycles: mockCycles,
      phase: mockPhase,
      settledState: mockSettled,
    }),
  };
});

import { RunFeed } from "./RunFeed";

function section(id: string, kind: string, emitters: string[]): RunProgressCycle {
  return {
    cycle: { id, kind: kind as never, attempts: 1, createdAt: "2026-07-10T09:00:00Z" },
    lines: emitters.map((emitter, i) => ({
      cycleId: id,
      cycleKind: kind,
      cycleIndex: 1,
      kind: "log",
      emitter: emitter as "main" | "subagent",
      seq: i + 1,
      summary: `${emitter} line ${i + 1}`,
    })),
  };
}

afterEach(() => {
  mockCycles = [];
  mockPhase = "live";
  mockSettled = undefined;
});

describe("RunFeed", () => {
  it("renders one section per cycle, labelled by kind", () => {
    mockCycles = [section("c1", "coding", ["main"]), section("c2", "fix", ["main"])];
    render(<RunFeed projectName="acme" runId="run-1" />);
    expect(screen.getByText("Cycle 1")).toBeInTheDocument();
    expect(screen.getByText("Cycle 2")).toBeInTheDocument();
    expect(screen.getByText("coding")).toBeInTheDocument();
    expect(screen.getByText("fix")).toBeInTheDocument();
  });

  it("stamps a subagent line and leaves the main agent's unstamped", () => {
    mockCycles = [section("c1", "coding", ["main", "subagent"])];
    render(<RunFeed projectName="acme" runId="run-1" />);
    // Exactly one chip: absence of a stamp is the positive fact "main agent".
    expect(screen.getAllByText("subagent")).toHaveLength(1);
  });

  it("filters to the cycle kinds a surface owns", () => {
    mockCycles = [
      section("c1", "coding", ["main"]),
      section("c2", "validation", ["main"]),
    ];
    render(
      <RunFeed projectName="acme" runId="run-1" cycleKinds={["validation"]} />,
    );
    expect(screen.getByText("validation")).toBeInTheDocument();
    expect(screen.queryByText("coding")).not.toBeInTheDocument();
  });

  it("says the run settled once the stream ends — only a terminal run does", () => {
    mockCycles = [section("c1", "coding", ["main"])];
    mockPhase = "ended";
    mockSettled = "succeeded";
    render(<RunFeed projectName="acme" runId="run-1" />);
    expect(screen.getByText(/run settled — succeeded/)).toBeInTheDocument();
  });

  it("says it is reattaching after a dropped connection", () => {
    mockPhase = "reconnecting";
    render(<RunFeed projectName="acme" runId="run-1" />);
    expect(screen.getByText(/reconnecting/)).toBeInTheDocument();
  });
});
