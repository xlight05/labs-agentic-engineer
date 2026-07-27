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

import type { components } from "../../../generated/aep-api";

type TimelineEvent = components["schemas"]["TimelineEvent"];

/**
 * Identity of one timeline line — the replay-dedup key and the list key.
 * Structured runner events carry a per-execution monotonic seq. Plain runner
 * stdout wrapped by the BFF (agent_progress.go) arrives with seq 0 on every
 * line, so a bare executionId:seq key would collide and swallow all but the
 * first; those fall back to the line's K8s timestamp + text, which is
 * per-line unique and identical across reconnect replays of the same line.
 */
export function timelineEventKey(e: TimelineEvent): string {
  if (e.seq) return `${e.executionId}:${e.seq}`;
  return `${e.executionId}:0:${e.ts}:${e.summary ?? e.message ?? ""}`;
}

// Friendly labels for phase ids. Covers both the runner's own workspace phases
// and the BFF's synthetic "dark zone" markers (agent_progress.go) that narrate
// pod scheduling / image pull / boot — the stretch before the runner writes its
// first line. An unmapped phase falls back to its summary, then the raw id, so
// nothing hides.
const PHASE_LABELS: Record<string, string> = {
  runner_scheduling: "Waiting for a runner to be scheduled…",
  runner_pulling_image: "Pulling the agent image…",
  runner_image_pull_backoff: "Still pulling the agent image (retrying)…",
  runner_config_error: "Waiting on runner configuration and secrets…",
  runner_starting: "Starting the agent…",
  workspace_provisioning: "Setting up the workspace…",
  workspace_ready: "Workspace ready",
};

/**
 * The runner's progress envelope, minus whatever the transport attributes it
 * to. TimelineEvent (per-execution, task log) and RunProgressLine (per-cycle,
 * run feed) are the same envelope carried by two streams and differ only in
 * their attribution fields, so the formatter below is written against the
 * envelope and both streams feed it.
 */
export interface AgentLogLine {
  kind: string;
  phase?: string;
  tool?: string;
  summary?: string;
  command?: string;
  step?: string;
  sha?: string;
  files?: number;
  branch?: string;
  status?: string;
  error?: string;
  level?: string;
  message?: string;
}

/**
 * One console line per log line, formatted by kind (#173 decisions: flat log;
 * attempts are divider lines, not UI sections).
 */
export function formatLine(e: AgentLogLine): { text: string; tone: string } {
  switch (e.kind) {
    case "phase": {
      const label =
        (e.phase && PHASE_LABELS[e.phase]) ?? e.summary ?? e.phase ?? e.message ?? "phase";
      return { text: `▸ ${label}`, tone: "info.light" };
    }
    case "tool_use":
      // The payload rides in summary (Bash: the command; file tools: the
      // path) — tool_use has no command field, that's gh_action's. For Bash
      // the `$` prompt already says "shell", so the tool name is noise; for
      // every other tool it is the verb (Write/Read/Edit <path>).
      if (e.tool === "Bash" && e.summary) {
        return { text: `$ ${e.summary}`, tone: "grey.400" };
      }
      return {
        text: `$ ${e.tool ?? "tool"}${e.summary ? ` ${e.summary}` : ""}`,
        tone: "grey.400",
      };
    case "git_commit":
      return {
        text: `✓ commit ${e.sha?.slice(0, 7) ?? ""}${e.files ? ` · ${e.files} files` : ""}`,
        tone: "success.light",
      };
    case "git_push":
      return {
        text: `↑ push${e.branch ? ` ${e.branch}` : ""}`,
        tone: "success.light",
      };
    case "gh_action":
    case "build_step":
      // gh_action's payload is its command; build_step's is step/summary.
      return {
        text: `⚙ ${e.step ?? e.summary ?? e.command ?? e.kind}${e.status ? ` — ${e.status}` : ""}`,
        tone: e.status === "failed" ? "error.light" : "info.light",
      };
    case "result":
      return {
        text: `■ ${e.summary ?? e.status ?? "finished"}${e.error ? ` — ${e.error}` : ""}`,
        tone: e.error || e.status === "failed" ? "error.light" : "success.light",
      };
    default: {
      const tone =
        e.level === "error"
          ? "error.light"
          : e.level === "warn"
            ? "warning.light"
            : "grey.300";
      return { text: e.message ?? e.summary ?? "", tone };
    }
  }
}
