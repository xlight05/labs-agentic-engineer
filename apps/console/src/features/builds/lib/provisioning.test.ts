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
import { gateRows, gatesNeedAction, provisioningStage } from "./provisioning";

type TaskView = components["schemas"]["TaskView"];

/** A dispatch gate, with the provisioning Execution that says who is on it. */
function gate(
  issueNumber: number,
  title: string,
  execStatus?: string,
  derivedStatus = "pending",
): TaskView {
  return {
    issueNumber,
    title,
    issueUrl: `https://github.com/acme/repo/issues/${issueNumber}`,
    executorClass: "provision",
    dependsOn: [],
    lineage: {},
    derivedStatus,
    hold: false,
    attention: [],
    executions: execStatus
      ? { provision: { id: "e1", kind: "provision", status: execStatus, createdAt: "" } }
      : {},
  } as TaskView;
}

describe("provisioningStage", () => {
  // A version that needs no connections gets no section — an empty reassurance
  // about nothing is worse than silence.
  it("is absent when the version needs no connections", () => {
    expect(provisioningStage([])).toBeNull();
  });

  it("counts what is still open, and says so when nothing is", () => {
    expect(
      provisioningStage([
        gate(1, "Provision resource: db", "succeeded", "merged"),
        gate(2, "Provide configuration: stripe", undefined, "merged"),
      ]),
    ).toMatchObject({ state: "done", fact: "2 resolved" });
    expect(
      provisioningStage([
        gate(1, "Provision resource: db", "running"),
        gate(2, "Provide configuration: stripe", undefined, "merged"),
      ]),
    ).toMatchObject({ state: "active", fact: "1 of 2 open" });
  });

  // THE reported bug this distinction exists for: a postgres cluster and an
  // identity app take minutes to stand up and the platform closes both gates
  // itself. Calling that a hold sent the user looking for something to do that
  // did not exist.
  it("reads gates the platform is provisioning as its own work, not a hold", () => {
    const stage = provisioningStage([
      gate(1, "Provision resource: user-auth (thunder-app)", "running"),
      gate(4, "Provision resource: ceramics-db (postgres-cnpg)", "queued"),
    ]);
    expect(stage?.state).toBe("active");
    expect(stage?.note).toMatch(/nothing is held on you/);
    expect(stage?.note).not.toMatch(/Supply/);
  });

  // A mixed set is answered by the gate that needs the most: until the stalled
  // one is supplied, the ones in flight release nothing.
  it("lets one stalled gate speak over gates that are in flight", () => {
    const stage = provisioningStage([
      gate(1, "Provision resource: db", "running"),
      gate(2, "Provide configuration: stripe"),
    ]);
    expect(stage?.state).toBe("attention");
    expect(stage?.note).toMatch(/Supply the configuration/);
  });

  it("puts a failed provisioning run above every other gate", () => {
    const stage = provisioningStage([
      gate(1, "Provision resource: db", "failed"),
      gate(2, "Provide configuration: stripe"),
      gate(3, "Provision resource: cache", "running"),
    ]);
    expect(stage?.state).toBe("failed");
    expect(stage?.note).toMatch(/Correct it and build again/);
  });
});

describe("gateRows", () => {
  it("says who is acting on each connection, one gate at a time", () => {
    const rows = gateRows([
      gate(1, "Provision resource: db", "running"),
      gate(2, "Provide configuration: stripe"),
      gate(3, "Provision resource: cache", "failed"),
      gate(4, "Provision resource: queue", "succeeded", "merged"),
    ]);
    expect(rows.map((row) => row.label)).toEqual([
      "provisioning",
      "needs you",
      "failed",
      "provisioned",
    ]);
  });

  // A SUCCEEDED provisioning run against a still-open gate means whatever was
  // driving it has finished and the gate is still open: the next move is a
  // person's, exactly as if no run had ever existed.
  it("treats a finished run against an open gate as needing a person", () => {
    expect(gateRows([gate(1, "Provision resource: db", "succeeded")])[0]?.label).toBe("needs you");
  });
});

describe("gatesNeedAction", () => {
  it("is true only when a human actually has something to do", () => {
    expect(gatesNeedAction([gate(1, "Provision resource: db", "running")])).toBe(false);
    expect(gatesNeedAction([gate(1, "Provision resource: db", "succeeded", "merged")])).toBe(false);
    expect(gatesNeedAction([gate(1, "Provide configuration: stripe")])).toBe(true);
    expect(gatesNeedAction([gate(1, "Provision resource: db", "failed")])).toBe(true);
  });
});
