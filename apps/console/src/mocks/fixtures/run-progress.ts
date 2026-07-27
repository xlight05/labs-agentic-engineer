import type { components } from "../../generated/aep-api";

type RunCycleView = components["schemas"]["RunCycleView"];
type RunProgressLine = components["schemas"]["RunProgressLine"];

// Per-cycle agent output for the run progress stream. The runner's envelope is
// the same one the task log carries; what differs is the attribution — a line
// belongs to a CYCLE, and it says whether the main agent or one of its Task
// subagents produced it.

const T0 = Date.parse("2026-07-10T09:14:00Z");
const iso = (offsetSec: number) => new Date(T0 + offsetSec * 1000).toISOString();

/** Terminal run states — only a terminal run settles the progress stream. */
export function isTerminalRunState(state: string): boolean {
  return ["succeeded", "failed", "cancelled"].includes(state);
}

function attribution(cycle: RunCycleView, index: number) {
  return {
    cycleId: cycle.id,
    cycleKind: cycle.kind,
    cycleIndex: index + 1,
    schemaVersion: 1,
  };
}

/** One cycle's replayed log. Kinds are chosen to exercise every formatter
 *  branch, and the fan-out lines are stamped `subagent`. */
export function runCycleLines(
  cycle: RunCycleView,
  index: number,
  startSeq: number,
): RunProgressLine[] {
  const base = attribution(cycle, index);
  let seq = startSeq;
  const line = (
    rest: Omit<RunProgressLine, keyof ReturnType<typeof attribution> | "emitter" | "seq" | "ts"> & {
      emitter?: RunProgressLine["emitter"];
    },
  ): RunProgressLine => ({
    ...base,
    emitter: "main",
    seq: seq++,
    ts: iso(seq * 10),
    ...rest,
  });

  if (cycle.kind === "validation") {
    return [
      line({ kind: "phase", phase: "planning" }),
      line({ kind: "log", summary: "Reading specs/validation/validation-criteria.json" }),
      line({ kind: "tool_use", tool: "Bash", summary: "pnpm playwright test" }),
      line({ kind: "log", summary: "3 criteria checked, 3 passed" }),
      line({ kind: "git_commit", sha: "7ab41c90ee31d5f0", files: 4 }),
      line({ kind: "result", status: "succeeded", summary: "report committed" }),
    ];
  }

  return [
    line({ kind: "phase", phase: "planning" }),
    line({ kind: "log", summary: `Working milestone ${String(index + 1)}'s open issues` }),
    line({ kind: "phase", phase: "implementing" }),
    // Fan-out: the main agent judged two issues independent and dispatched
    // subagents; their forwarded lines carry the stamp.
    line({ kind: "tool_use", tool: "Write", summary: "src/api/shorten.ts", emitter: "subagent" }),
    line({ kind: "tool_use", tool: "Write", summary: "src/api/redirect.ts", emitter: "subagent" }),
    line({ kind: "tool_use", tool: "Bash", summary: "go test ./..." }),
    line({ kind: "log", summary: "ok  github.com/acme/shortener  0.412s" }),
    ...(cycle.mergeSha
      ? [
          line({ kind: "git_commit", sha: cycle.mergeSha, files: 6 }),
          line({ kind: "git_push", branch: cycle.branch ?? "" }),
          line({
            kind: "result",
            status: "succeeded",
            summary: `PR #${String(cycle.prNumber ?? 0)} opened with Resolves`,
          }),
        ]
      : []),
  ];
}

/** A heartbeat line for a live run's newest cycle. */
export function runHeartbeatLine(
  cycle: RunCycleView,
  index: number,
  seq: number,
  tick: number,
): RunProgressLine {
  return {
    ...attribution(cycle, index),
    emitter: "main",
    seq,
    ts: new Date().toISOString(),
    kind: "log",
    summary: `still working — tick ${String(tick)}`,
  };
}
