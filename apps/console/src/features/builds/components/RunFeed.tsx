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

import {
  Accordion,
  AccordionDetails,
  AccordionSummary,
  Box,
  Chip,
  Stack,
  Typography,
} from "@wso2/oxygen-ui";
import { ChevronDown } from "@wso2/oxygen-ui-icons-react";
import { formatLine } from "../../tasks/lib/timeline";
import {
  runLineKey,
  useRunProgress,
  type RunProgressCycle,
} from "../hooks/useRunProgress";

// The run feed: ONE SSE stream for the whole run, rendered as one accordion
// section per cycle. Grouping by cycle is the point — a fix or conflict cycle
// re-enters an earlier phase of the loop, so a flat log would read as the agent
// going backwards. Each line carries an emitter chip: `main` for the run's main
// agent, `subagent` for work it fanned out with the Task tool.

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

function CycleSection({
  section,
  index,
  defaultExpanded,
}: {
  section: RunProgressCycle;
  index: number;
  defaultExpanded: boolean;
}) {
  const { cycle, lines } = section;
  return (
    <Accordion
      disableGutters
      elevation={0}
      defaultExpanded={defaultExpanded}
      sx={{ "&:before": { display: "none" } }}
    >
      <AccordionSummary expandIcon={<ChevronDown size={16} />}>
        <Stack direction="row" spacing={1} sx={{ alignItems: "center" }}>
          <Typography variant="subtitle2">Cycle {index + 1}</Typography>
          <Chip label={cycle.kind} size="small" variant="outlined" />
          {cycle.attempts > 1 && (
            <Typography variant="caption" color="text.secondary">
              {cycle.attempts} attempts
            </Typography>
          )}
          <Typography variant="caption" color="text.secondary">
            {lines.length} line{lines.length === 1 ? "" : "s"}
          </Typography>
        </Stack>
      </AccordionSummary>
      <AccordionDetails sx={{ pt: 0 }}>
        <Box
          sx={{
            bgcolor: "grey.900",
            borderRadius: 1,
            p: 2,
            maxHeight: 420,
            overflowY: "auto",
            fontFamily: "monospace",
            fontSize: "0.8125rem",
            lineHeight: 1.7,
          }}
        >
          {lines.length === 0 ? (
            <Typography component="div" sx={{ font: "inherit", color: "grey.500" }}>
              No output from this cycle yet.
            </Typography>
          ) : (
            lines.map((line) => {
              const { text, tone } = formatLine(line);
              return (
                <Box
                  key={runLineKey(line)}
                  sx={{ display: "flex", alignItems: "baseline" }}
                >
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
            })
          )}
        </Box>
      </AccordionDetails>
    </Accordion>
  );
}

/**
 * The run's per-cycle progress feed. Mounted only where it should stream —
 * the hook opens the SSE connection on mount and closes it on unmount, so
 * keeping this behind a toggle is what keeps a settled page connection-free.
 */
export function RunFeed({
  projectName,
  runId,
  cycleKinds,
}: {
  projectName: string;
  runId: string;
  /** Show only these cycle kinds. The stream is always the whole run — the
   *  filter is presentational, for a surface that owns one phase of the loop
   *  (the deployment surface owns validation). Omitted = every cycle. */
  cycleKinds?: readonly string[];
}) {
  const all = useRunProgress(projectName, runId);
  const feed = cycleKinds
    ? {
        ...all,
        cycles: all.cycles.filter((c) => cycleKinds.includes(c.cycle.kind)),
      }
    : all;

  let tail: string | undefined;
  if (feed.phase === "connecting") {
    tail = "attaching to the run feed…";
  } else if (feed.phase === "reconnecting") {
    tail = "connection lost — reconnecting…";
  } else if (feed.phase === "ended") {
    tail = `run settled${feed.settledState ? ` — ${feed.settledState}` : ""}`;
  }

  return (
    <Box>
      {feed.cycles.length === 0 ? (
        <Typography variant="body2" color="text.secondary" sx={{ py: 2 }}>
          No cycle output yet — the run's first agent has not written a line.
        </Typography>
      ) : (
        feed.cycles.map((section, i) => (
          <CycleSection
            key={section.cycle.id}
            section={section}
            // Numbered within what is shown; a filtered feed owns one phase and
            // its section is "Cycle 1" of that phase, not of the whole run.
            index={i}
            // The newest cycle is what the user came to watch; older ones stay
            // collapsed so a long run does not open as a wall of log.
            defaultExpanded={i === feed.cycles.length - 1}
          />
        ))
      )}
      {tail && (
        <Typography variant="caption" color="text.secondary" sx={{ display: "block", mt: 1 }}>
          {tail}
        </Typography>
      )}
    </Box>
  );
}
