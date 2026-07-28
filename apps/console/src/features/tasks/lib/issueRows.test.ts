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

import { describe, expect, it } from "vitest";
import type { components } from "../../../generated/aep-api";
import { gateSubject, openGates, partitionIssues } from "./issueRows";

type TaskView = components["schemas"]["TaskView"];

function issue(
  issueNumber: number,
  executorClass: string,
  derivedStatus: "pending" | "merged" = "pending",
  title = `Issue ${issueNumber}`,
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

describe("partitionIssues", () => {
  it("splits agent work, gates, and the ledger", () => {
    const p = partitionIssues([
      issue(1, "coding"),
      issue(2, "provision"),
      issue(3, "ledger"),
    ]);
    expect(p.work.map((i) => i.issueNumber)).toEqual([1]);
    expect(p.gates.map((i) => i.issueNumber)).toEqual([2]);
    expect(p.ledger.map((i) => i.issueNumber)).toEqual([3]);
  });

  // A provisioned connection is part of how the version came to exist, so it
  // stays in the record like closed agent work does. Narrowing to what still
  // HOLDS is openGates' job, not the partition's.
  it("keeps a RESOLVED gate — the version's record, not just its blockers", () => {
    const p = partitionIssues([issue(2, "provision", "merged")]);
    expect(p.gates.map((i) => i.issueNumber)).toEqual([2]);
    expect(p.work).toHaveLength(0);
    expect(p.ledger).toHaveLength(0);
  });

  it("keeps closed agent work — that is the version's record of what got done", () => {
    const p = partitionIssues([issue(1, "coding", "merged")]);
    expect(p.work.map((i) => i.issueNumber)).toEqual([1]);
  });

  it("keeps a closed ledger issue in the ledger", () => {
    const p = partitionIssues([issue(3, "ledger", "merged")]);
    expect(p.ledger.map((i) => i.issueNumber)).toEqual([3]);
  });

  it("treats an unknown kind as agent work rather than dropping it", () => {
    // A label vocabulary the console has not learned yet must still be visible.
    const p = partitionIssues([issue(9, "something-new")]);
    expect(p.work.map((i) => i.issueNumber)).toEqual([9]);
  });

  it("preserves order within each section", () => {
    const p = partitionIssues([issue(5, "coding"), issue(4, "coding")]);
    expect(p.work.map((i) => i.issueNumber)).toEqual([5, 4]);
  });
});

describe("openGates", () => {
  it("keeps only the gates that still hold dispatch", () => {
    const gates = [issue(1, "provision"), issue(2, "provision", "merged")];
    expect(openGates(gates).map((i) => i.issueNumber)).toEqual([1]);
  });
});

describe("gateSubject", () => {
  it("names the dependency a gate is holding on", () => {
    expect(gateSubject("Provide configuration: url-shortener-db")).toBe(
      "url-shortener-db",
    );
  });

  it("falls back to the whole title when there is no colon", () => {
    expect(gateSubject("Approve org publish")).toBe("Approve org publish");
  });
});
