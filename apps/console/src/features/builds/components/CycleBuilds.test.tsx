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
// Every (buildName, open) the log hook was called with — proves a collapsed
// build costs no read.
let logCalls: Array<{ buildName: string; open: boolean }> = [];
let mockLog: BuildLogState = {
  entries: [],
  complete: true,
  loading: false,
  error: undefined,
};

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

function renderBuilds() {
  render(<CycleBuilds projectName="acme" builds={mockBuilds} />);
}

afterEach(() => {
  mockBuilds = [];
  logCalls = [];
  mockLog = { entries: [], complete: true, loading: false, error: undefined };
});

describe("CycleBuilds", () => {
  // Whether the merge has landed, whether the fan-out has appeared, and whether
  // it was read at all are all said by the Builds STAGE above these rows, so
  // there is nothing left for this component to say when it has no builds.
  it("renders nothing when the builds have not been read", () => {
    const { container } = render(
      <CycleBuilds projectName="acme" builds={undefined} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing for a fan-out that has not appeared yet", () => {
    const { container } = render(<CycleBuilds projectName="acme" builds={[]} />);
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

});
