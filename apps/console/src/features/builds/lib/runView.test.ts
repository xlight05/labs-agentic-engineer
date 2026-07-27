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
import {
  budgetCounters,
  cycleLabel,
  isTerminalRun,
  runOriginLabel,
  runStateChip,
  terminalReasonText,
  validationVerdictChip,
  versionIsLive,
} from "./runView";

type MilestoneRunView = components["schemas"]["MilestoneRunView"];
type RunBudgets = components["schemas"]["RunBudgets"];

const budgets = (over: Partial<RunBudgets> = {}): RunBudgets => ({
  cyclesTotal: 0,
  cycleCeiling: 8,
  fixCycles: 0,
  conflictCycles: 0,
  buildRetriggers: 0,
  ...over,
});

const run = (over: Partial<MilestoneRunView> = {}): MilestoneRunView => ({
  id: "run-1",
  milestoneNumber: 1,
  milestoneTitle: "v1",
  origin: "spec-build",
  state: "running",
  budgets: budgets(),
  validation: {},
  cycles: [],
  createdAt: "2026-07-10T09:00:00Z",
  ...over,
});

describe("isTerminalRun / versionIsLive", () => {
  it("names the three terminal states and nothing else", () => {
    expect(isTerminalRun("succeeded")).toBe(true);
    expect(isTerminalRun("failed")).toBe(true);
    expect(isTerminalRun("cancelled")).toBe(true);
    expect(isTerminalRun("waiting")).toBe(false);
    expect(isTerminalRun("running")).toBe(false);
  });

  it("a version with no runs is not live", () => {
    expect(versionIsLive([])).toBe(false);
  });

  it("only the NEWEST run decides — a milestone's runs are sequential", () => {
    // Newest first. An old succeeded run behind a live one must not settle the
    // page, and an old running row behind a terminal one must not keep it
    // polling forever.
    expect(versionIsLive([run({ state: "waiting" }), run({ state: "succeeded" })])).toBe(
      true,
    );
    expect(versionIsLive([run({ state: "succeeded" }), run({ state: "running" })])).toBe(
      false,
    );
  });
});

describe("runStateChip", () => {
  it("gives waiting its own warning tone — that is when cancel matters", () => {
    expect(runStateChip(run({ state: "waiting" }))).toEqual({
      label: "Waiting",
      tone: "warning",
    });
  });

  it("renders an unknown state raw and red rather than hiding it", () => {
    expect(runStateChip(run({ state: "quantum" as never }))).toEqual({
      label: "quantum",
      tone: "error",
    });
  });
});

describe("terminalReasonText", () => {
  it("spells each failure class as a sentence", () => {
    expect(terminalReasonText("no-progress")).toMatch(/closed no issues/);
    expect(terminalReasonText("cycle-ceiling")).toMatch(/total-cycle ceiling/);
  });

  it("passes an unmapped reason through so it still reaches the user", () => {
    expect(terminalReasonText("a-reason-from-the-future")).toBe(
      "a-reason-from-the-future",
    );
  });

  it("is empty for a run that has no reason", () => {
    expect(terminalReasonText("")).toBe("");
  });
});

describe("budgetCounters", () => {
  it("shows the run's own snapshotted ceiling, not a hardcoded one", () => {
    const [cycles] = budgetCounters(budgets({ cyclesTotal: 3, cycleCeiling: 5 }));
    expect(cycles?.text).toBe("3 / 5");
    expect(cycles?.exhausted).toBe(false);
  });

  it("marks a spent budget — the counter that explains the terminal reason", () => {
    const counters = budgetCounters(
      budgets({ cyclesTotal: 8, fixCycles: 2, conflictCycles: 1 }),
    );
    const by = Object.fromEntries(counters.map((c) => [c.label, c]));
    expect(by["Cycles"]?.exhausted).toBe(true);
    expect(by["Fix cycles"]?.exhausted).toBe(true);
    expect(by["Conflict cycles"]?.exhausted).toBe(false);
  });

  it("gives build re-triggers no denominator — the real guard is per component per SHA", () => {
    const by = Object.fromEntries(
      budgetCounters(budgets({ buildRetriggers: 3 })).map((c) => [c.label, c]),
    );
    expect(by["Build re-triggers"]?.text).toBe("3");
    expect(by["Build re-triggers"]?.exhausted).toBe(false);
  });
});

describe("validationVerdictChip", () => {
  it("is null until the validation cycle settles", () => {
    expect(validationVerdictChip(undefined)).toBeNull();
    expect(validationVerdictChip({})).toBeNull();
  });

  it("names the verdict", () => {
    expect(validationVerdictChip({ verdict: "passed" })?.tone).toBe("success");
    expect(validationVerdictChip({ verdict: "failed" })?.tone).toBe("error");
    expect(validationVerdictChip({ verdict: "skipped" })?.tone).toBe("neutral");
  });
});

describe("cycleLabel / runOriginLabel", () => {
  it("numbers a cycle from 1 and names its kind", () => {
    expect(
      cycleLabel({ id: "c", kind: "fix", attempts: 1, createdAt: "" }, 2),
    ).toBe("Cycle 3 · fix");
  });

  it("spells the origin", () => {
    expect(runOriginLabel("spec-build")).toBe("Spec build");
    expect(runOriginLabel("incident-adoption")).toBe("Incident");
  });
});
