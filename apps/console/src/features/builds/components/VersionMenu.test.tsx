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

const navigate = vi.fn();
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
}));

// Record how the ledger query was asked for — "on demand, never polled" is the
// §10 requirement this control exists to honour.
const useBuildsCalls: { enabled?: boolean; poll?: boolean }[] = [];
let mockBuilds: BuildSummary[] | undefined;
vi.mock("../api/queries", () => ({
  useBuilds: (_project: string, opts: { enabled?: boolean; poll?: boolean }) => {
    useBuildsCalls.push(opts);
    return {
      data: opts.enabled ? mockBuilds : undefined,
      isPending: opts.enabled ? mockBuilds === undefined : true,
      isError: false,
    };
  },
}));

import { VersionMenu } from "./VersionMenu";

function build(tag: string, status: BuildSummary["status"]): BuildSummary {
  return {
    tag,
    milestoneNumber: Number(tag.slice(1)),
    status,
    startedAt: "2026-07-10T09:00:00Z",
  };
}

afterEach(() => {
  useBuildsCalls.length = 0;
  mockBuilds = undefined;
  navigate.mockClear();
});

describe("VersionMenu — the version ledger on the overview", () => {
  it("costs nothing until it is opened", () => {
    mockBuilds = [build("v2", "completed")];
    render(
      <VersionMenu projectName="acme" currentVersion="v2" tone="success" />,
    );
    // Closed: the query is disabled, and it is never polled either way.
    expect(useBuildsCalls.every((c) => c.enabled === false)).toBe(true);
    expect(useBuildsCalls.every((c) => c.poll === false)).toBe(true);
  });

  it("fetches the ledger once opened, and never polls it", () => {
    mockBuilds = [build("v2", "completed"), build("v1", "completed")];
    render(
      <VersionMenu projectName="acme" currentVersion="v2" tone="success" />,
    );
    fireEvent.click(screen.getByText("v2"));

    expect(useBuildsCalls.some((c) => c.enabled === true)).toBe(true);
    expect(useBuildsCalls.every((c) => c.poll === false)).toBe(true);
    expect(screen.getByText("v1")).toBeInTheDocument();
  });

  it("deep-links the Builds page at the chosen version", () => {
    mockBuilds = [build("v2", "completed"), build("v1", "failed")];
    render(
      <VersionMenu projectName="acme" currentVersion="v2" tone="success" />,
    );
    fireEvent.click(screen.getByText("v2"));
    fireEvent.click(screen.getByText("v1"));

    expect(navigate).toHaveBeenCalledWith({
      to: "/projects/$projectName/builds",
      params: { projectName: "acme" },
      search: { tag: "v1" },
    });
  });

  it("renders an em-dash when no version has been built", () => {
    render(<VersionMenu projectName="acme" currentVersion="" tone="default" />);
    expect(screen.getByText("—")).toBeInTheDocument();
  });
});
