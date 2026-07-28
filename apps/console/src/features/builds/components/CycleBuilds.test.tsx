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
import type { BuildLogState } from "../hooks/useBuildLog";

type CycleBuild = components["schemas"]["CycleBuild"];

let mockBuilds: CycleBuild[] = [];
let mockPending = false;
// Every (buildName, open) the log hook was called with — proves a collapsed
// build costs no read.
let logCalls: Array<{ buildName: string; open: boolean }> = [];
let mockLog: BuildLogState = {
  entries: [],
  complete: true,
  loading: false,
  error: undefined,
};

vi.mock("../api/queries", () => ({
  useCycleBuilds: () => ({ data: mockBuilds, isPending: mockPending }),
}));

vi.mock("../hooks/useBuildLog", () => ({
  useBuildLog: (_p: string, _c: string, buildName: string, open: boolean) => {
    logCalls.push({ buildName, open });
    return mockLog;
  },
}));

import { CycleBuilds } from "./CycleBuilds";

function build(over: Partial<CycleBuild> = {}): CycleBuild {
  return {
    component: "workout-api",
    buildName: "proj-workout-api-4a91c2f8ab31-1",
    status: "Running",
    completed: false,
    attempt: 1,
    ...over,
  };
}

function renderBuilds(mergeSha = "4a91c2f8ab31") {
  render(
    <CycleBuilds projectName="acme" tag="v2" cycleId="c1" mergeSha={mergeSha} />,
  );
}

afterEach(() => {
  mockBuilds = [];
  mockPending = false;
  logCalls = [];
  mockLog = { entries: [], complete: true, loading: false, error: undefined };
});

describe("CycleBuilds", () => {
  // Before the merge there is nothing to have built, and an empty box would
  // read as "the builds failed to appear".
  it("renders nothing at all for a cycle that has not merged", () => {
    const { container } = render(
      <CycleBuilds projectName="acme" tag="v2" cycleId="c1" mergeSha="" />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("shows each component's status without anything being opened", () => {
    mockBuilds = [
      build(),
      build({
        component: "workout-tracker-webapp",
        buildName: "proj-workout-tracker-webapp-4a91c2f8ab31-1",
        status: "Succeeded",
        completed: true,
      }),
    ];
    renderBuilds();

    expect(screen.getByText("workout-api")).toBeInTheDocument();
    expect(screen.getByText("Running")).toBeInTheDocument();
    expect(screen.getByText("workout-tracker-webapp")).toBeInTheDocument();
    expect(screen.getByText("Succeeded")).toBeInTheDocument();
    // Status is glanceable; the logs are not fetched until asked for.
    expect(logCalls.every((c) => !c.open)).toBe(true);
  });

  it("marks a re-triggered build — a second attempt means the first went red", () => {
    mockBuilds = [build({ attempt: 2, status: "Failed", completed: true })];
    renderBuilds();
    expect(screen.getByText("attempt 2")).toBeInTheDocument();
    expect(screen.getByText("Failed")).toBeInTheDocument();
  });

  it("reads a build's log only once its row is expanded", () => {
    mockBuilds = [build()];
    mockLog = {
      entries: [{ log: "compiled 41 packages", timestamp: "2026-07-27T10:42:39Z" }],
      complete: false,
      loading: false,
      error: undefined,
    };
    renderBuilds();

    expect(logCalls.every((c) => !c.open)).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: /Show log/ }));

    expect(logCalls.at(-1)).toEqual({
      buildName: "proj-workout-api-4a91c2f8ab31-1",
      open: true,
    });
    expect(screen.getByText("compiled 41 packages")).toBeInTheDocument();
    // A build still writing says so rather than looking finished.
    expect(screen.getByText("…tailing")).toBeInTheDocument();
  });

  // A completed build with no entries is retention, not failure.
  it("explains an aged-out log rather than showing an empty terminal", () => {
    mockBuilds = [build({ status: "Succeeded", completed: true })];
    mockLog = { entries: [], complete: true, loading: false, error: undefined };
    renderBuilds();
    fireEvent.click(screen.getByRole("button", { name: /Show log/ }));

    expect(screen.getByText(/No log retained for this build/)).toBeInTheDocument();
  });

  it("says the merge has produced no build yet rather than showing an empty list", () => {
    mockBuilds = [];
    renderBuilds();
    expect(screen.getByText(/has not produced a build yet/)).toBeInTheDocument();
  });
});
