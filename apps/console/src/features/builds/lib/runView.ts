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
type TaskView = components["schemas"]["TaskView"];
type RunCycleView = components["schemas"]["RunCycleView"];
type RunBudgets = components["schemas"]["RunBudgets"];
type RunValidation = components["schemas"]["RunValidation"];
type CycleBuild = components["schemas"]["CycleBuild"];

// Pure derivations for the version's run story. The run state is the Builds
// page's single liveness driver — nothing here consults a task's derivedStatus,
// which the flip degraded to "the issue is open, or it is closed".

/**
 * The cycle kinds the BUILDS surface owns.
 *
 * Validation is deliberately absent. The deployment is what gets validated, so
 * its cycle and its verdict render on the deployment surface — the same reason
 * the issue list hides the validation issue. A validation cycle showing up
 * here would put the verdict in two places and invite them to disagree.
 */
export const BUILD_CYCLE_KINDS = ["coding", "fix", "conflict"] as const;

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
 *  parks, so it is worth its own warning tone — that is when cancel matters.
 *  `planning` is the platform working, so it reads like `running`. */
export function runStateChip(run: MilestoneRunView): {
  label: string;
  tone: StatusTone;
} {
  switch (run.state) {
    case "planning":
      return { label: "Planning", tone: "info" };
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

/**
 * WHO, if anyone, has to act on an OPEN dispatch gate.
 *
 * Every path that resolves a gate admits a `provision` Execution against the
 * gate's issue before it does anything else — the drawer's config author, the
 * platform-resource provisioner, the org-service visibility request. That row
 * is therefore the answer to "is anything already working on this", and it is
 * the only honest one available: a gate issue is prose plus two labels, and the
 * platform never reads a gate back to decide anything.
 *
 *   provisioning — a run is in flight. The platform closes this gate itself,
 *                  so nothing is needed from the user. A database or an
 *                  identity app takes minutes to stand up, and calling that
 *                  wait a hold is what made a healthy build read as parked on
 *                  a human.
 *   failed       — the provisioning run broke. The gate issue carries why.
 *   idle         — no run at all: nothing is driving this gate. This is the
 *                  one case where a human genuinely has to supply something.
 *
 * A SUCCEEDED run against a still-open gate reads `idle` for the same reason a
 * missing row does — whatever was driving it is finished and the gate is still
 * open, so the next move is a person's.
 */
export type GateDrive = "provisioning" | "failed" | "idle";

/** The Execution statuses that still hold the admission mutex, i.e. work in
 *  flight (mirrors taskmeta.ExecutionStatus.IsActive). */
const ACTIVE_EXEC_STATES = new Set(["queued", "running"]);

export function gateDrive(gate: TaskView): GateDrive {
  const status = gate.executions?.["provision"]?.status;
  if (status === undefined) return "idle";
  if (ACTIVE_EXEC_STATES.has(status)) return "provisioning";
  return status === "failed" || status === "canceled" ? "failed" : "idle";
}

/**
 * WHY NOTHING IS MOVING — the one thing a user asks of a run that is not
 * running, and the one answer the page owes them.
 *
 * The reasons are genuinely different, and conflating them is what makes a
 * healthy build announce itself as parked: the platform writing the milestone
 * is not a hold, a gate the platform is actively provisioning is not a hold
 * either, a gate nothing is driving IS one only a human can release, and an
 * empty milestone is the loop declining to call a version delivered that was
 * never planned. Each has a different thing to do about it — including
 * "nothing" — so each gets its own notice.
 *
 * `milestone` is the issue plane's answer, or undefined while it is still
 * loading — a run is never accused of having no work on the strength of a list
 * that has not arrived. Its `gates` are the OPEN ones; a resolved gate holds
 * nothing.
 */
export interface RunHold {
  kind:
    | "planning"
    | "provisioning"
    | "gate-failed"
    | "gates"
    | "no-work"
    | "parked";
  /** Warning is reserved for a hold only a human can release; error for one
   *  that already went wrong. The platform doing its own bounded work is
   *  information, not an alarm. */
  tone: "info" | "warning" | "error";
  title: string;
  body: string;
  /** The gates this notice is about — the failing ones, the stalled ones, or
   *  the ones being provisioned, never all of them indiscriminately. Empty for
   *  a hold that names no gate. */
  gates: TaskView[];
}

export function runHold(
  run: MilestoneRunView,
  milestone: { gates: TaskView[]; openWork: number } | undefined,
): RunHold | null {
  if (run.state === "planning") {
    return {
      kind: "planning",
      tone: "info",
      title: `Planning ${run.milestoneTitle}`,
      body:
        "Creating this version's issues in GitHub. Nothing is held and nothing " +
        "is needed from you — the first cycle dispatches as soon as the " +
        "milestone is written.",
      gates: [],
    };
  }
  if (run.state !== "waiting" || milestone === undefined) return null;

  const gateHold = holdFromGates(milestone.gates);
  if (gateHold) return gateHold;

  if (milestone.openWork === 0) {
    return {
      kind: "no-work",
      tone: "info",
      title: "Waiting for work",
      body:
        "This version's milestone holds no open issue to dispatch. The run " +
        "waits rather than settling — a milestone nothing was ever planned " +
        "into is not a version that was delivered.",
      gates: [],
    };
  }
  return {
    kind: "parked",
    tone: "warning",
    title: "Parked between cycles",
    body:
      "The wait is unbounded — cancel is its only expiry, and cancelling " +
      "abandons the increment: the way forward is the next build.",
    gates: [],
  };
}

/**
 * The open gates' own notice, or null when there are none.
 *
 * Loudest first, because a mixed set is answered by the gate that needs the
 * most: one broken connection is the story even if three others are still
 * provisioning happily, and one stalled connection is the story even if the
 * rest are in flight — until it is supplied, none of them release the run.
 * Only when EVERY open gate has a run in flight is there nothing to do but
 * wait, and that is the case this whole split exists for.
 */
function holdFromGates(gates: TaskView[]): RunHold | null {
  if (gates.length === 0) return null;

  const failed = gates.filter((gate) => gateDrive(gate) === "failed");
  if (failed.length > 0) {
    return {
      kind: "gate-failed",
      tone: "error",
      title:
        failed.length === 1
          ? "A connection failed to provision"
          : `${failed.length} connections failed to provision`,
      body:
        "The gate issue carries what went wrong. Correct it and build again — " +
        "the run stays parked until every connection resolves.",
      gates: failed,
    };
  }

  const idle = gates.filter((gate) => gateDrive(gate) === "idle");
  if (idle.length > 0) {
    return {
      kind: "gates",
      tone: "warning",
      title:
        idle.length === 1
          ? "Held by an unresolved connection"
          : `Held by ${idle.length} unresolved connections`,
      body:
        "The run dispatches nothing while a connection gate is open. Supply " +
        "the configuration and the remaining issues are released.",
      gates: idle,
    };
  }

  const one = gates.length === 1;
  return {
    kind: "provisioning",
    tone: "info",
    title: one
      ? "Provisioning a connection"
      : `Provisioning ${gates.length} connections`,
    body: one
      ? "The platform is standing this connection up and closes the gate " +
        "itself — nothing is held on you. A database or an identity app takes " +
        "a few minutes; the first cycle dispatches as soon as it is ready."
      : "The platform is standing these connections up and closes each gate " +
        "itself — nothing is held on you. A database or an identity app takes " +
        "a few minutes; the first cycle dispatches as soon as the last one " +
        "is ready.",
    gates,
  };
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

/**
 * What started this run. A milestone can see several SEQUENTIAL runs — the
 * spec build, then an incident adoption — and every run card is titled with the
 * same milestone title, so the origin is the only thing that tells them apart.
 * An origin this build predates is shown raw rather than mislabelled.
 */
export function runOriginLabel(origin: string): string {
  switch (origin) {
    case "spec-build":
      return "Spec build";
    case "incident-adoption":
      return "Incident";
    default:
      return origin;
  }
}

/**
 * One build's chip.
 *
 * The tone is decided by `completed` — OpenChoreo's status is a condition
 * Reason string, not a closed set, so it is a LABEL and never a predicate. A
 * completed build reads success or failure from that string only to choose
 * between two terminal tones; an unrecognised terminal Reason shows itself
 * rather than being flattened to one or the other.
 */
export function buildStatusChip(build: CycleBuild): {
  label: string;
  tone: StatusTone;
} {
  if (!build.completed) {
    // "Pending" is OpenChoreo's word for a run that exists but has not started.
    return {
      label: build.status || "Running",
      tone: build.status === "Pending" ? "neutral" : "info",
    };
  }
  if (build.status === "Succeeded") return { label: "Succeeded", tone: "success" };
  if (build.status === "Failed") return { label: "Failed", tone: "error" };
  return { label: build.status || "Completed", tone: "neutral" };
}

/** Does this fan-out still have a build that could change? */
export function buildsAreSettled(builds: CycleBuild[]): boolean {
  return builds.length > 0 && builds.every((b) => b.completed);
}

export interface SpentBudget {
  label: string;
  /** The count against the ceiling it reached: "2 / 2". */
  text: string;
}

// The §7 budgets that are per-RUN constants; the per-cycle re-dispatch budget
// belongs to a cycle row, not here.
const FIX_CYCLE_BUDGET = 2;
const CONFLICT_CYCLE_BUDGET = 2;

/**
 * The budgets this run has SPENT — a counter sitting at its ceiling, and
 * nothing else. A healthy run returns [], and the card shows no numbers at
 * all: how much allowance is left is loop machinery, and it only becomes the
 * user's business once an allowance runs out and explains why the run stopped.
 * How many cycles ran is already legible from the cycle timeline.
 *
 * `cycleCeiling` is snapshotted on the run row, so a config change cannot
 * retroactively re-scale a live run's gauge. Build re-triggers are absent by
 * design: they have no run-wide ceiling — the real guard is one per component
 * per SHA, derived at the trigger site — so there is nothing here to spend.
 */
export function spentBudgets(budgets: RunBudgets): SpentBudget[] {
  const spent: SpentBudget[] = [];
  if (budgets.cycleCeiling > 0 && budgets.cyclesTotal >= budgets.cycleCeiling) {
    spent.push({
      label: "Cycles",
      text: `${budgets.cyclesTotal} / ${budgets.cycleCeiling}`,
    });
  }
  if (budgets.fixCycles >= FIX_CYCLE_BUDGET) {
    spent.push({
      label: "Fix cycles",
      text: `${budgets.fixCycles} / ${FIX_CYCLE_BUDGET}`,
    });
  }
  if (budgets.conflictCycles >= CONFLICT_CYCLE_BUDGET) {
    spent.push({
      label: "Conflict cycles",
      text: `${budgets.conflictCycles} / ${CONFLICT_CYCLE_BUDGET}`,
    });
  }
  return spent;
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
