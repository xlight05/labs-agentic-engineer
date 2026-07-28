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

import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { components } from "../../../generated/aep-api";

type TaskView = components["schemas"]["TaskView"];

// Router stubbed to plain anchors — no RouterProvider needed. createLink is
// what the gate banner's deep link uses, so it has to survive the stub.
vi.mock("@tanstack/react-router", () => ({
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

let mockIssues: TaskView[] = [];
const refetch = vi.fn();
vi.mock("../api/queries", () => ({
  useAllTasks: () => ({
    data: mockIssues,
    isPending: false,
    isError: false,
    error: null,
    refetch,
  }),
}));

import { IssueSections } from "./IssueSections";

function issue(
  issueNumber: number,
  title: string,
  executorClass: string,
  derivedStatus: "pending" | "merged" = "pending",
): TaskView {
  return {
    issueNumber,
    title,
    issueUrl: `https://github.com/o/r/issues/${issueNumber}`,
    executorClass: executorClass as TaskView["executorClass"],
    derivedStatus,
    dependsOn: null,
    attention: null,
    executions: {},
    hold: false,
    lineage: { specTag: "v1" },
  };
}

afterEach(() => {
  mockIssues = [];
});

function renderSections(live = false) {
  render(<IssueSections projectName="acme" tag="v1" live={live} />);
}

describe("IssueSections", () => {
  it("renders agent work as rows carrying DURABLE facts only", () => {
    mockIssues = [
      issue(2, "Implement the shortener API", "coding"),
      issue(3, "Add the redirect handler", "coding", "merged"),
    ];
    renderSections();

    expect(screen.getByText("Implement the shortener API")).toBeInTheDocument();
    // GitHub state, and nothing inferred about what an agent is doing now.
    expect(screen.getByText("Open")).toBeInTheDocument();
    expect(screen.getByText("Done")).toBeInTheDocument();
    // The only affordance is the issue itself.
    expect(
      screen.getByRole("link", { name: "GitHub issue #2" }),
    ).toHaveAttribute("href", "https://github.com/o/r/issues/2");
  });

  // A provisioned connection is as much a part of how a version came to exist
  // as a merged pull request — a list that hides it tells an incomplete story.
  // What it must NOT do is read as agent work, hence the tag.
  it("lists provisioning gates with agent work, tagged for what they are", () => {
    mockIssues = [
      issue(1, "Provide configuration: url-shortener-db", "provision", "merged"),
      issue(2, "Implement the shortener API", "coding"),
    ];
    renderSections();

    expect(
      screen.getByText("Provide configuration: url-shortener-db"),
    ).toBeInTheDocument();
    expect(screen.getByText("Provisioning")).toBeInTheDocument();
    // Agent work carries no kind tag — a list that is mostly coding rows does
    // not need every one of them labelled "Coding".
    expect(screen.getAllByText("Provisioning")).toHaveLength(1);
    // …and no second warning competing with the run card's hold.
    expect(screen.queryByText(/unresolved/)).not.toBeInTheDocument();
  });

  it("orders rows the way the version happened — connections, then the work", () => {
    mockIssues = [
      issue(6, "Implement ceramics-webapp", "coding"),
      issue(1, "Provision resource: user-auth", "provision", "merged"),
      issue(5, "Implement ceramics-api", "coding"),
    ];
    renderSections();

    const titles = screen
      .getAllByRole("row")
      .slice(1) // drop the header row
      .map((row) => row.textContent);
    expect(titles?.[0]).toContain("Provision resource: user-auth");
    expect(titles?.[1]).toContain("Implement ceramics-api");
    expect(titles?.[2]).toContain("Implement ceramics-webapp");
  });

  it("puts bare human issues in their own Ledger section", () => {
    mockIssues = [
      issue(2, "Implement the shortener API", "coding"),
      issue(7, "Login is slow", "ledger"),
    ];
    renderSections();

    expect(screen.getByText("Ledger")).toBeInTheDocument();
    expect(screen.getByText(/Never worked and never/)).toBeInTheDocument();
    expect(screen.getByText("Login is slow")).toBeInTheDocument();
  });

  it("omits the Ledger section entirely when there is nothing in it", () => {
    mockIssues = [issue(2, "Implement the shortener API", "coding")];
    renderSections();
    expect(screen.queryByText("Ledger")).not.toBeInTheDocument();
  });

  it("counts each section beside its title, gates included", () => {
    mockIssues = [
      issue(1, "Provide configuration: db", "provision", "merged"),
      issue(2, "One", "coding"),
      issue(3, "Two", "coding"),
      issue(7, "Ledger one", "ledger"),
    ];
    renderSections();
    const issuesHeading = screen.getByText("Issues").parentElement;
    expect(within(issuesHeading as HTMLElement).getByText("3")).toBeInTheDocument();
  });

  it("says the plan has not landed yet when a version has no work", () => {
    renderSections();
    expect(screen.getByText(/No issues for v1 yet/)).toBeInTheDocument();
  });
});
