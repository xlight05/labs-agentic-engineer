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

import { useEffect, useRef, useState } from "react";
import { parseSseStream } from "@aep/agent-stream";
import { client } from "../../../api/client";
import type { components } from "../../../generated/aep-api";

type RunProgressEvent = components["schemas"]["RunProgressEvent"];
type RunProgressLine = components["schemas"]["RunProgressLine"];
type RunCycleView = components["schemas"]["RunCycleView"];

/**
 * Where the stream is.
 *
 * `idle` is NOT a connection state: it means nobody asked for one (the hook was
 * called with `enabled: false`). It exists because reporting `connecting` for a
 * stream that was never opened made a collapsed, connection-free surface tell the
 * user it was "attaching to the run feed" forever.
 */
export type RunProgressPhase =
  | "idle"
  | "connecting"
  | "live"
  | "reconnecting"
  | "ended";

/** One cycle's section of the feed: the cycle record plus its own lines. */
export interface RunProgressCycle {
  cycle: RunCycleView;
  lines: RunProgressLine[];
}

export interface RunProgressState {
  /** Cycles in dispatch order, each holding the lines attributed to it. */
  cycles: RunProgressCycle[];
  /** Terminal run state from the `done` frame — the stream is over. */
  settledState: string | undefined;
  phase: RunProgressPhase;
}

const RECONNECT_DELAY_MS = 3_000;

/**
 * Identity of one progress line — the replay-dedup key and the list key.
 * The server stamps a per-run sequence, but a line whose seq is 0 (unstructured
 * runner stdout the BFF wrapped) would collide across a cycle, so those fall
 * back to their timestamp plus text, which is per-line unique and identical
 * across reconnect replays of the same line.
 */
export function runLineKey(line: RunProgressLine): string {
  if (line.seq) return `${line.cycleId}:${line.seq}`;
  return `${line.cycleId}:0:${line.ts ?? ""}:${line.summary ?? line.error ?? ""}`;
}

async function openRunStream(
  projectName: string,
  runId: string,
  signal: AbortSignal,
): Promise<ReadableStream<Uint8Array>> {
  const { data, error } = await client.GET(
    "/projects/{projectName}/runs/{runId}/progress",
    {
      params: { path: { projectName, runId } },
      parseAs: "stream",
      signal,
    },
  );
  if (error || !data) throw new Error("Failed to attach to the run feed");
  return data as ReadableStream<Uint8Array>;
}

/**
 * Attach to ONE run's progress stream and accumulate it grouped by cycle.
 *
 * The wire format is the platform's standard agent SSE (`data:` JSON frames,
 * keep-alive comments, `[DONE]` sentinel), so this reuses agent-stream's parser
 * exactly as useTaskLog does; the frames here are RunProgressEvents. ONLY a
 * terminal run settles the stream, so `ended` is a fact about the run, not
 * about the connection — a live run's stream simply stays open, and an EOF
 * without `[DONE]` is a dropped connection to reattach (idempotent by
 * contract: cycles upsert by id, lines dedup by key).
 *
 * Passing no runId keeps the hook inert — no stream is opened. That is how the
 * feed stays closed until the user opens it.
 */
export function useRunProgress(
  projectName: string,
  runId: string | undefined,
  /** Open the stream at all. False keeps a page that nobody is watching
   *  connection-free — the property the old run-level feed toggle gave us. */
  enabled = true,
): RunProgressState {
  const [cycles, setCycles] = useState<RunProgressCycle[]>([]);
  const [settledState, setSettledState] = useState<string>();
  const [phase, setPhase] = useState<RunProgressPhase>("idle");
  const seen = useRef(new Set<string>());

  useEffect(() => {
    // Reset on run change (the hook instance survives a param update).
    seen.current = new Set();
    setCycles([]);
    setSettledState(undefined);
    // Not enabled = nobody is looking. A settled version whose cycles are all
    // collapsed must open no connection and replay no history — and must not
    // claim to be connecting either.
    if (!runId || !enabled) {
      setPhase("idle");
      return;
    }
    setPhase("connecting");

    const controller = new AbortController();
    let disposed = false;

    const upsertCycle = (cycle: RunCycleView) =>
      setCycles((prev) => {
        const i = prev.findIndex((c) => c.cycle.id === cycle.id);
        if (i === -1) return [...prev, { cycle, lines: [] }];
        const next = prev.slice();
        // Keep the accumulated lines: a re-emitted cycle record is fresher
        // metadata (branch, PR, merge SHA learned from webhooks), not a reset.
        next[i] = { cycle, lines: prev[i]?.lines ?? [] };
        return next;
      });

    const appendLine = (line: RunProgressLine) =>
      setCycles((prev) => {
        const i = prev.findIndex((c) => c.cycle.id === line.cycleId);
        if (i === -1) {
          // A line can outrun its cycle frame on a reconnect; the line carries
          // enough attribution to open the section itself.
          return [
            ...prev,
            {
              cycle: {
                id: line.cycleId,
                // The line's attribution is the same vocabulary as the cycle
                // record's; the contract types one as an enum and the other as
                // free text, so this narrows what is already the same value.
                kind: line.cycleKind as RunCycleView["kind"],
                attempts: 0,
                createdAt: line.ts ?? "",
              },
              lines: [line],
            },
          ];
        }
        const next = prev.slice();
        const at = prev[i];
        if (!at) return prev;
        next[i] = { cycle: at.cycle, lines: [...at.lines, line] };
        return next;
      });

    const consume = async (): Promise<"done" | "eof"> => {
      const body = await openRunStream(projectName, runId, controller.signal);
      setPhase("live");
      const frames = parseSseStream(body);
      while (true) {
        const next = await frames.next();
        if (next.done) return next.value;
        const event = next.value as unknown as RunProgressEvent;
        switch (event.type) {
          case "cycle":
            if (event.cycle) upsertCycle(event.cycle);
            break;
          case "line": {
            const line = event.line;
            if (!line) break;
            const key = runLineKey(line);
            if (seen.current.has(key)) break;
            seen.current.add(key);
            appendLine(line);
            break;
          }
          case "done":
            if (event.state) setSettledState(event.state);
            break;
        }
      }
    };

    const run = async () => {
      while (!disposed) {
        try {
          const end = await consume();
          if (end === "done") {
            setPhase("ended");
            return;
          }
        } catch {
          if (disposed) return;
        }
        setPhase("reconnecting");
        await new Promise((r) => setTimeout(r, RECONNECT_DELAY_MS));
      }
    };
    void run();

    return () => {
      disposed = true;
      controller.abort();
    };
  }, [projectName, runId, enabled]);

  return { cycles, settledState, phase };
}
