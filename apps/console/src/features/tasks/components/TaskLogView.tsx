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

import { useEffect, useMemo, useRef } from "react";
import { Box, Typography } from "@wso2/oxygen-ui";
import type { components } from "../../../generated/aep-api";

type TimelineEvent = components["schemas"]["TimelineEvent"];

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

// One console line per TimelineEvent, formatted by kind (#173 decisions:
// flat log; execution attempts are divider lines, not UI sections).
function formatLine(e: TimelineEvent): { text: string; tone: string } {
  switch (e.kind) {
    case "phase": {
      const label =
        (e.phase && PHASE_LABELS[e.phase]) ?? e.summary ?? e.phase ?? e.message ?? "phase";
      return { text: `▸ ${label}`, tone: "info.light" };
    }
    case "tool_use":
      return {
        text: `$ ${e.tool ?? "tool"}${e.command ? ` ${e.command}` : ""}`,
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
      return {
        text: `⚙ ${e.step ?? e.summary ?? e.kind}${e.status ? ` — ${e.status}` : ""}`,
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

type Row =
  | { type: "divider"; key: string; label: string }
  | { type: "line"; key: string; text: string; tone: string };

// Attempts are numbered by first appearance in the stream; a divider row is
// injected whenever the executionId changes.
function toRows(lines: TimelineEvent[]): Row[] {
  const attemptNo = new Map<string, number>();
  const rows: Row[] = [];
  let current: string | undefined;
  for (const line of lines) {
    if (line.executionId !== current) {
      current = line.executionId;
      if (!attemptNo.has(line.executionId)) {
        attemptNo.set(line.executionId, attemptNo.size + 1);
      }
      rows.push({
        type: "divider",
        key: `div:${line.executionId}:${line.seq}`,
        label: `attempt ${attemptNo.get(line.executionId)} · ${line.executionKind}`,
      });
    }
    const { text, tone } = formatLine(line);
    rows.push({
      type: "line",
      key: `${line.executionId}:${line.seq}`,
      text,
      tone,
    });
  }
  return rows;
}

export function TaskLogView({
  lines,
  tail,
}: {
  lines: TimelineEvent[];
  /** Extra status line rendered under the log (waiting / reconnecting / settled). */
  tail?: string;
}) {
  const rows = useMemo(() => toRows(lines), [lines]);
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickToBottom = useRef(true);

  // Follow the stream like a terminal: stay pinned to the bottom unless the
  // user scrolled up to read; re-pin when they scroll back down.
  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    stickToBottom.current =
      el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  };
  useEffect(() => {
    const el = scrollRef.current;
    if (el && stickToBottom.current) el.scrollTop = el.scrollHeight;
  }, [rows.length, tail]);

  return (
    <Box
      ref={scrollRef}
      onScroll={onScroll}
      sx={{
        bgcolor: "grey.900",
        borderRadius: 1,
        p: 2,
        overflowY: "auto",
        flexGrow: 1,
        minHeight: 0,
        fontFamily: "monospace",
        fontSize: "0.8125rem",
        lineHeight: 1.7,
      }}
    >
      {rows.map((row) =>
        row.type === "divider" ? (
          <Typography
            key={row.key}
            component="div"
            sx={{
              font: "inherit",
              color: "grey.500",
              mt: 1,
              "&:first-of-type": { mt: 0 },
            }}
          >
            {`── ${row.label} ${"─".repeat(Math.max(4, 40 - row.label.length))}`}
          </Typography>
        ) : (
          <Typography
            key={row.key}
            component="div"
            sx={{
              font: "inherit",
              color: row.tone,
              whiteSpace: "pre-wrap",
              wordBreak: "break-word",
            }}
          >
            {row.text}
          </Typography>
        ),
      )}
      {tail && (
        <Typography
          component="div"
          sx={{ font: "inherit", color: "grey.500", mt: 1 }}
        >
          {tail}
        </Typography>
      )}
    </Box>
  );
}
