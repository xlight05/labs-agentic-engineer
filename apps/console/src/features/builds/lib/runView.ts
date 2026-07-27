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

import type { StatusTone } from "../../../components/StatusChip";
import type { components } from "../../../generated/aep-api";

type MilestoneRunView = components["schemas"]["MilestoneRunView"];
type RunCycleView = components["schemas"]["RunCycleView"];
type RunBudgets = components["schemas"]["RunBudgets"];
type RunValidation = components["schemas"]["RunValidation"];

// Pure derivations for the version's run story. The run state is the Builds
// page's single liveness driver — nothing here consults a task's derivedStatus,
// which the flip degraded to "the issue is open, or it is closed".

/** Terminal run states: the loop is over and nothing will move again. */
const TERMINAL_RUN_STATES = new Set(["succeeded", "failed", "cancelled"]);

export function isTerminalRun(state: string): boolean {
  return TERMINAL_RUN_STATES.has(state);
}

/**
 * Is this version still moving? Only the newest run can be live (a milestone
 * sees SEQUENTIAL runs across its life), so this is the whole page's poll
 * predicate and the gate on every GitHub-backed read.
 */
export function versionIsLive(runs: MilestoneRunView[]): boolean {
  const newest = runs[0];
  return newest !== undefined && !isTerminalRun(newest.state);
}

/** The run's own state chip. `waiting` is written only when a run actually
 *  parks, so it is worth its own warning tone — that is when cancel matters. */
export function runStateChip(run: MilestoneRunView): {
  label: string;
  tone: StatusTone;
} {
  switch (run.state) {
    case "waiting":
      return { label: "Waiting", tone: "warning" };
    case "running":
      return { label: "Running", tone: "info" };
    case "succeeded":
      return { label: "Succeeded", tone: "success" };
    case "failed":
      return { label: "Failed", tone: "error" };
    case "cancelled":
      return { label: "Cancelled", tone: "neutral" };
    default:
      // An unknown state renders raw and red rather than hiding.
      return { label: run.state, tone: "error" };
  }
}

// Each terminal reason names exactly ONE failure class (that is the point of
// the vocabulary), so each gets a sentence rather than a re-worded enum.
const TERMINAL_REASONS: Record<string, string> = {
  "redispatch-budget":
    "The coding agent died twice in the same cycle — the per-cycle re-dispatch budget is spent.",
  "build-retrigger-budget":
    "A component's build stayed red after its automatic re-trigger, and no fix issue came back.",
  "fix-chain-budget": "The run spent both of its fix cycles.",
  "conflict-budget": "The run spent both of its conflict cycles.",
  "no-progress":
    "A cycle closed no issues and minted none — the run stopped rather than loop.",
  "cycle-ceiling": "The run hit its total-cycle ceiling.",
  "validation-failed": "Validation failed against the acceptance criteria.",
};

/** A sentence for the run's terminal reason; the raw value when unmapped, so
 *  a reason this console has not learned yet still reaches the user. */
export function terminalReasonText(reason: string): string {
  if (!reason) return "";
  return TERMINAL_REASONS[reason] ?? reason;
}

/** One cycle's section label — "Cycle 3 · fix". */
export function cycleLabel(cycle: RunCycleView, index: number): string {
  return `Cycle ${index + 1} · ${cycle.kind}`;
}

export interface BudgetCounter {
  label: string;
  /** Rendered text: "3 / 8" when the budget has a ceiling, else "1". */
  text: string;
  /** At or past the ceiling — the counter that explains a terminal reason. */
  exhausted: boolean;
}

// The §7 budgets that are per-RUN constants; the per-cycle re-dispatch budget
// belongs to a cycle row, not here.
const FIX_CYCLE_BUDGET = 2;
const CONFLICT_CYCLE_BUDGET = 2;

/**
 * The run's budget counters, in the order the loop spends them. `cycleCeiling`
 * is snapshotted on the run row, so a config change cannot retroactively
 * re-scale a live run's gauge. Build re-triggers have no run-wide ceiling —
 * the real guard is one per component per SHA, derived at the trigger site —
 * so that one renders as a tally with no denominator.
 */
export function budgetCounters(budgets: RunBudgets): BudgetCounter[] {
  return [
    {
      label: "Cycles",
      text: `${budgets.cyclesTotal} / ${budgets.cycleCeiling}`,
      exhausted:
        budgets.cycleCeiling > 0 && budgets.cyclesTotal >= budgets.cycleCeiling,
    },
    {
      label: "Fix cycles",
      text: `${budgets.fixCycles} / ${FIX_CYCLE_BUDGET}`,
      exhausted: budgets.fixCycles >= FIX_CYCLE_BUDGET,
    },
    {
      label: "Conflict cycles",
      text: `${budgets.conflictCycles} / ${CONFLICT_CYCLE_BUDGET}`,
      exhausted: budgets.conflictCycles >= CONFLICT_CYCLE_BUDGET,
    },
    {
      label: "Build re-triggers",
      text: `${budgets.buildRetriggers}`,
      exhausted: false,
    },
  ];
}

/**
 * The run's validation verdict as a chip. This is where the deployment surface
 * reads validation from — the verdict is a RUN property, not a per-issue one.
 * null = the run has no verdict yet, which on a live run means it has not
 * reached its validation cycle.
 */
export function validationVerdictChip(
  validation: RunValidation | undefined,
): { label: string; tone: StatusTone } | null {
  switch (validation?.verdict) {
    case "passed":
      return { label: "Validation passed", tone: "success" };
    case "failed":
      return { label: "Validation failed", tone: "error" };
    case "skipped":
      return { label: "Validation skipped", tone: "neutral" };
    default:
      return null;
  }
}

/** Origin, spelled for a human. */
export function runOriginLabel(origin: string): string {
  return origin === "incident-adoption" ? "Incident" : "Spec build";
}
