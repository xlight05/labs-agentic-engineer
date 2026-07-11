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
import type { components } from "../../../generated/aep-api";
import { client } from "../../../api/client";

type TaskStreamEvent = components["schemas"]["TaskStreamEvent"];
type TaskView = components["schemas"]["TaskView"];
type TimelineEvent = components["schemas"]["TimelineEvent"];
type ExecutionView = components["schemas"]["ExecutionView"];

export type TaskLogPhase = "connecting" | "live" | "reconnecting" | "ended";

export interface TaskLogState {
  /** Live task upserted from `task` frames (fresher than get-task). */
  task: TaskView | undefined;
  /** Unified timeline, deduped by executionId+seq (reconnects re-emit). */
  lines: TimelineEvent[];
  /** Every attempt, upserted from `execution` frames (first appearance order).
   * Drives the waiting-state affordance (is an attempt still running?) while the
   * runner pod is scheduling / pulling its image and the timeline is empty. */
  executions: ExecutionView[];
  /** Settled derivedStatus from the `done` frame — the stream is over. */
  settledStatus: string | undefined;
  phase: TaskLogPhase;
}

const RECONNECT_DELAY_MS = 3_000;

async function openLogStream(
  projectName: string,
  issueNumber: number,
  signal: AbortSignal,
): Promise<ReadableStream<Uint8Array>> {
  const { data, error } = await client.GET(
    "/projects/{projectName}/tasks/{issueNumber}/log",
    {
      params: { path: { projectName, issueNumber } },
      parseAs: "stream",
      signal,
    },
  );
  if (error || !data) throw new Error("Failed to attach to the task log");
  return data as ReadableStream<Uint8Array>;
}

/**
 * Attach to the task's SSE log (stream-task-log) and accumulate its state.
 * The wire format is the platform's standard agent SSE (`data:` JSON frames,
 * keep-alive comments, `[DONE]` sentinel), so this reuses agent-stream's
 * parser; frames here are TaskStreamEvents, not turn StreamParts. Reconnects
 * are safe by contract — the server re-emits state and the client dedups.
 */
export function useTaskLog(
  projectName: string,
  issueNumber: number,
): TaskLogState {
  const [task, setTask] = useState<TaskView>();
  const [lines, setLines] = useState<TimelineEvent[]>([]);
  const [executions, setExecutions] = useState<ExecutionView[]>([]);
  const [settledStatus, setSettledStatus] = useState<string>();
  const [phase, setPhase] = useState<TaskLogPhase>("connecting");
  const seen = useRef(new Set<string>());

  useEffect(() => {
    // Reset on task change (the hook instance survives route param updates).
    seen.current = new Set();
    setTask(undefined);
    setLines([]);
    setExecutions([]);
    setSettledStatus(undefined);
    setPhase("connecting");

    const controller = new AbortController();
    let disposed = false;

    const consume = async (): Promise<"done" | "eof"> => {
      const body = await openLogStream(
        projectName,
        issueNumber,
        controller.signal,
      );
      setPhase("live");
      const frames = parseSseStream(body);
      while (true) {
        const next = await frames.next();
        if (next.done) return next.value;
        const event = next.value as unknown as TaskStreamEvent;
        switch (event.type) {
          case "task":
            if (event.task) setTask(event.task);
            break;
          case "execution": {
            // Timeline rows carry their own executionId/kind, so the log body
            // needs no executions map. We DO keep the attempts here for the
            // waiting-state affordance: while the runner pod is scheduling /
            // pulling its image the timeline is empty, and knowing an attempt is
            // still running lets the page reassure ("still working…") instead of
            // implying the task stalled.
            const exec = event.execution;
            if (!exec) break;
            setExecutions((prev) => {
              const i = prev.findIndex((e) => e.id === exec.id);
              if (i === -1) return [...prev, exec];
              const next = prev.slice();
              next[i] = exec;
              return next;
            });
            break;
          }
          case "line": {
            const line = event.line;
            if (!line) break;
            const key = `${line.executionId}:${line.seq}`;
            if (seen.current.has(key)) break;
            seen.current.add(key);
            setLines((prev) => [...prev, line]);
            break;
          }
          case "done":
            if (event.derivedStatus) setSettledStatus(event.derivedStatus);
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
        // eof without [DONE] or a transport error: the task is still live —
        // wait and reattach (idempotent by contract).
        setPhase("reconnecting");
        await new Promise((r) => setTimeout(r, RECONNECT_DELAY_MS));
      }
    };
    void run();

    return () => {
      disposed = true;
      controller.abort();
    };
  }, [projectName, issueNumber]);

  return { task, lines, executions, settledStatus, phase };
}
