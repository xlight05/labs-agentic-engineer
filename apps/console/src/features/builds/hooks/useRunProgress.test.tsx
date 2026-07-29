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

import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

// One SSE body per attach. The hook reconnects on EOF-without-[DONE], so a
// test that wants a single attach must end its body with the sentinel.
let bodies: string[] = [];
const GET = vi.fn(() => {
  const text = bodies.shift() ?? "";
  return Promise.resolve({
    data: new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(text));
        controller.close();
      },
    }),
    error: undefined,
  });
});
// The factory is hoisted above `GET`'s initialiser, so it reads it lazily.
vi.mock("../../../api/client", () => ({
  client: {
    GET: (...args: unknown[]) => GET(...(args as [])),
  },
}));

import { runLineKey, useRunProgress } from "./useRunProgress";

function frames(...events: unknown[]): string {
  return events.map((e) => `data: ${JSON.stringify(e)}\n\n`).join("");
}

const cycle = (id: string, kind: string) => ({
  type: "cycle",
  cycle: { id, kind, attempts: 1, createdAt: "2026-07-10T09:00:00Z" },
});

const line = (cycleId: string, cycleKind: string, seq: number, summary: string, emitter = "main") => ({
  type: "line",
  line: {
    cycleId,
    cycleKind,
    cycleIndex: 1,
    kind: "log",
    emitter,
    seq,
    summary,
    ts: "2026-07-10T09:01:00Z",
  },
});

const DONE = "data: [DONE]\n\n";

afterEach(() => {
  bodies = [];
  GET.mockClear();
});

describe("useRunProgress", () => {
  it("opens nothing without a run id", () => {
    const { result } = renderHook(() => useRunProgress("acme", undefined));
    expect(GET).not.toHaveBeenCalled();
    expect(result.current.phase).toBe("idle");
  });

  // A stream nobody asked for is IDLE, not connecting. Reporting `connecting`
  // for it had a connection-free surface (every build session collapsed) telling
  // the user it was "attaching to the run feed" — forever, since no attach was
  // ever going to happen.
  it("is idle, not connecting, while it is disabled", () => {
    const { result } = renderHook(() => useRunProgress("acme", "run-1", false));
    expect(GET).not.toHaveBeenCalled();
    expect(result.current.phase).toBe("idle");
  });

  it("groups lines under the cycle that produced them", async () => {
    bodies = [
      frames(
        cycle("c1", "coding"),
        cycle("c2", "fix"),
        line("c1", "coding", 1, "first"),
        line("c2", "fix", 2, "second"),
        line("c1", "coding", 3, "third"),
        { type: "done", state: "succeeded" },
      ) + DONE,
    ];
    const { result } = renderHook(() => useRunProgress("acme", "run-1"));

    await waitFor(() => expect(result.current.phase).toBe("ended"));
    expect(result.current.cycles.map((c) => c.cycle.id)).toEqual(["c1", "c2"]);
    expect(result.current.cycles[0]?.lines.map((l) => l.summary)).toEqual([
      "first",
      "third",
    ]);
    expect(result.current.cycles[1]?.lines.map((l) => l.summary)).toEqual([
      "second",
    ]);
    expect(result.current.settledState).toBe("succeeded");
  });

  it("keeps a cycle's lines when its record is re-emitted with fresher facts", async () => {
    // Branch, PR and merge SHA are learned from webhooks after the fact, so a
    // second cycle frame is an UPSERT — it must not reset the accumulated log.
    bodies = [
      frames(
        cycle("c1", "coding"),
        line("c1", "coding", 1, "first"),
        {
          type: "cycle",
          cycle: {
            id: "c1",
            kind: "coding",
            attempts: 1,
            branch: "aep/m1-c1",
            prNumber: 3,
            createdAt: "2026-07-10T09:00:00Z",
          },
        },
        { type: "done", state: "succeeded" },
      ) + DONE,
    ];
    const { result } = renderHook(() => useRunProgress("acme", "run-1"));

    await waitFor(() => expect(result.current.phase).toBe("ended"));
    expect(result.current.cycles[0]?.cycle.branch).toBe("aep/m1-c1");
    expect(result.current.cycles[0]?.lines).toHaveLength(1);
  });

  it("opens a section from a line that outran its cycle frame", async () => {
    bodies = [
      frames(line("c9", "validation", 1, "orphan"), {
        type: "done",
        state: "succeeded",
      }) + DONE,
    ];
    const { result } = renderHook(() => useRunProgress("acme", "run-1"));

    await waitFor(() => expect(result.current.phase).toBe("ended"));
    expect(result.current.cycles[0]?.cycle.id).toBe("c9");
    expect(result.current.cycles[0]?.cycle.kind).toBe("validation");
  });

  it("dedups a replayed line across a reconnect", async () => {
    // First attach ends WITHOUT [DONE] — a dropped connection, not a settled
    // run — so the hook reattaches and the server replays from the start.
    bodies = [
      frames(cycle("c1", "coding"), line("c1", "coding", 1, "first")),
      frames(
        cycle("c1", "coding"),
        line("c1", "coding", 1, "first"),
        line("c1", "coding", 2, "second"),
        { type: "done", state: "succeeded" },
      ) + DONE,
    ];
    const { result } = renderHook(() => useRunProgress("acme", "run-1"));

    await waitFor(() => expect(result.current.phase).toBe("ended"), {
      timeout: 6000,
    });
    expect(GET).toHaveBeenCalledTimes(2);
    expect(result.current.cycles).toHaveLength(1);
    expect(result.current.cycles[0]?.lines.map((l) => l.summary)).toEqual([
      "first",
      "second",
    ]);
  }, 10000);
});

describe("runLineKey", () => {
  it("keys a structured line on its cycle and sequence", () => {
    expect(
      runLineKey({
        cycleId: "c1",
        cycleKind: "coding",
        cycleIndex: 1,
        kind: "log",
        emitter: "main",
        seq: 7,
      }),
    ).toBe("c1:7");
  });

  it("falls back for a seq-0 line so wrapped stdout does not collapse", () => {
    const base = {
      cycleId: "c1",
      cycleKind: "coding",
      cycleIndex: 1,
      kind: "log",
      emitter: "main" as const,
      seq: 0,
      ts: "2026-07-10T09:01:00Z",
    };
    expect(runLineKey({ ...base, summary: "a" })).not.toBe(
      runLineKey({ ...base, summary: "b" }),
    );
  });
});
