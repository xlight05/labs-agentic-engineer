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

import { Box, Chip, Typography } from "@wso2/oxygen-ui";
import type { components } from "../../../generated/aep-api";
import { formatLine } from "../../tasks/lib/timeline";
import { runLineKey } from "../hooks/useRunProgress";

type RunProgressLine = components["schemas"]["RunProgressLine"];

// One cycle's agent output. Extracted so the run's own cycle sections and the
// deployment surface's validation feed render a line identically — two
// renderings of the same stream that drifted apart would read as two different
// agents.

function EmitterChip({ emitter }: { emitter: string }) {
  // The main agent is the overwhelming majority of lines, so only a subagent
  // line is stamped — an unstamped line reads as "the main agent", which is
  // exactly the contract's own rule and keeps the feed quiet.
  if (emitter !== "subagent") return null;
  return (
    <Chip
      label="subagent"
      size="small"
      variant="outlined"
      sx={{
        height: 16,
        fontSize: "0.6875rem",
        color: "grey.400",
        borderColor: "grey.700",
        mr: 1,
        flexShrink: 0,
      }}
    />
  );
}

/** The log surface every machine-written line in this feature renders on. */
export function LogSurface({
  children,
  maxHeight = 420,
}: {
  children: React.ReactNode;
  maxHeight?: number;
}) {
  return (
    <Box
      sx={{
        bgcolor: "grey.900",
        borderRadius: 1,
        p: 2,
        maxHeight,
        overflowY: "auto",
        fontFamily: "monospace",
        fontSize: "0.8125rem",
        lineHeight: 1.7,
      }}
    >
      {children}
    </Box>
  );
}

/** A line of dimmed monospace on the log surface — empty states and notes. */
export function LogNote({ children }: { children: React.ReactNode }) {
  return (
    <Typography component="div" sx={{ font: "inherit", color: "grey.500" }}>
      {children}
    </Typography>
  );
}

export function AgentLogLines({ lines }: { lines: RunProgressLine[] }) {
  if (lines.length === 0) {
    return <LogNote>No output from this cycle yet.</LogNote>;
  }
  return (
    <>
      {lines.map((line) => {
        const { text, tone } = formatLine(line);
        return (
          <Box key={runLineKey(line)} sx={{ display: "flex", alignItems: "baseline" }}>
            <EmitterChip emitter={line.emitter} />
            <Typography
              component="div"
              sx={{
                font: "inherit",
                color: tone,
                whiteSpace: "pre-wrap",
                wordBreak: "break-word",
                minWidth: 0,
              }}
            >
              {text}
            </Typography>
          </Box>
        );
      })}
    </>
  );
}
