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

import { useState } from "react";
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
import type { components } from "../../../generated/aep-api";
import { useRunProgress, type RunProgressCycle } from "../hooks/useRunProgress";
import { BUILD_CYCLE_KINDS, cycleLabel } from "../lib/runView";
import { AgentLogLines, LogSurface } from "./AgentLogLines";
import { CycleBuilds } from "./CycleBuilds";

type MilestoneRunView = components["schemas"]["MilestoneRunView"];
type RunCycleView = components["schemas"]["RunCycleView"];

// The run's cycles, each as ONE section telling that cycle's whole story in the
// order it happened: the facts the platform learned, the agent that ran, then
// the builds its merge produced.
//
// This is deliberately one component and not two. A cycle used to appear twice
// on this page — as a facts row in a timeline, and again as a log section in a
// feed below it — which put a cycle's agent output and its build output at
// opposite ends of the page. They are the same story.
//
// Grouping by cycle is also what keeps the loop legible: a fix or conflict
// cycle re-enters an earlier phase, so a flat log would read as the agent going
// backwards.

/** One learned fact. An em-dash is a fact too: the platform has not been told
 *  yet, because branch, PR and merge all arrive by webhook. */
function Fact({ label, value }: { label: string; value: string }) {
  return (
    <Stack direction="row" spacing={0.5} sx={{ alignItems: "baseline" }}>
      <Typography variant="caption" color="text.secondary">
        {label}
      </Typography>
      <Typography
        variant="caption"
        sx={{ fontFamily: "monospace", fontVariantNumeric: "tabular-nums" }}
      >
        {value || "—"}
      </Typography>
    </Stack>
  );
}

function CycleSection({
  projectName,
  tag,
  cycle,
  index,
  lines,
  expanded,
  onToggle,
}: {
  projectName: string;
  tag: string;
  cycle: RunCycleView;
  index: number;
  lines: RunProgressCycle["lines"];
  expanded: boolean;
  onToggle: (expanded: boolean) => void;
}) {
  const running = !cycle.endedAt;
  return (
    <Stack direction="row" spacing={1.5} sx={{ alignItems: "stretch" }}>
      {/* Rail: a dot per cycle joined by a line, so the column reads as one
          sequence rather than a stack of unrelated sections. */}
      <Box
        sx={{
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          pt: 2.25,
        }}
      >
        <Box
          sx={{
            width: 8,
            height: 8,
            borderRadius: "50%",
            flexShrink: 0,
            bgcolor: running ? "info.main" : "success.main",
          }}
        />
        <Box sx={{ flexGrow: 1, width: "1px", bgcolor: "divider", mt: 0.5 }} />
      </Box>

      <Accordion
        disableGutters
        elevation={0}
        expanded={expanded}
        onChange={(_, isExpanded) => onToggle(isExpanded)}
        // Collapsed means UNMOUNTED, not merely hidden. The details hold a
        // cluster-backed builds read; left mounted, every cycle on the page
        // would poll it whether or not anyone had opened the section.
        slotProps={{ transition: { unmountOnExit: true } }}
        sx={{ flexGrow: 1, minWidth: 0, bgcolor: "transparent", "&:before": { display: "none" } }}
      >
        <AccordionSummary expandIcon={<ChevronDown size={16} />}>
          <Box sx={{ minWidth: 0 }}>
            <Stack
              direction="row"
              spacing={1}
              sx={{ alignItems: "center", flexWrap: "wrap", rowGap: 0.5 }}
            >
              <Typography variant="subtitle2">{cycleLabel(cycle, index)}</Typography>
              {cycle.attempts > 1 && (
                // The per-cycle re-dispatch budget: 2 dispatches, reset at every
                // cycle boundary. A second attempt means the first agent died.
                <Chip
                  label={`attempt ${cycle.attempts}/2`}
                  size="small"
                  variant="outlined"
                  color="warning"
                />
              )}
              {running && (
                <Typography variant="caption" color="info.main">
                  in flight
                </Typography>
              )}
            </Stack>
            <Stack
              direction="row"
              spacing={2}
              sx={{ flexWrap: "wrap", rowGap: 0.5, mt: 0.25 }}
            >
              <Fact label="branch" value={cycle.branch ?? ""} />
              <Fact label="PR" value={cycle.prNumber ? `#${cycle.prNumber}` : ""} />
              <Fact label="merge" value={(cycle.mergeSha ?? "").slice(0, 8)} />
            </Stack>
          </Box>
        </AccordionSummary>
        <AccordionDetails sx={{ pt: 0 }}>
          <Typography variant="overline" color="text.secondary">
            Coding agent
          </Typography>
          <LogSurface>
            <AgentLogLines lines={lines} />
          </LogSurface>
          <CycleBuilds
            projectName={projectName}
            tag={tag}
            cycleId={cycle.id}
            mergeSha={cycle.mergeSha ?? ""}
          />
        </AccordionDetails>
      </Accordion>
    </Stack>
  );
}

/**
 * Every cycle of one run, oldest first.
 *
 * The cycle RECORDS come from the run read, not from the stream: a cycle exists
 * the moment it is dispatched, and waiting for its first log line to render it
 * would hide the boot window entirely. The stream then fills each section's
 * lines in.
 */
export function CycleSections({
  projectName,
  tag,
  run,
}: {
  projectName: string;
  tag: string;
  run: MilestoneRunView;
}) {
  // Validation is not this surface's cycle — the deployment is what gets
  // validated, and its verdict renders there.
  const cycles = run.cycles.filter((c) =>
    (BUILD_CYCLE_KINDS as readonly string[]).includes(c.kind),
  );

  // A live run opens on its cycle in flight — that is what the user came to
  // watch. A settled run opens on nothing, which is what keeps a finished
  // version from replaying every agent log the moment the page loads.
  const [expanded, setExpanded] = useState<Set<string>>(() => {
    const live = cycles.find((c) => !c.endedAt);
    return new Set(live ? [live.id] : []);
  });
  const toggle = (id: string, open: boolean) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (open) next.add(id);
      else next.delete(id);
      return next;
    });

  // The agent log is only read while a section is open — one stream for the
  // whole run, opened on demand.
  const progress = useRunProgress(projectName, run.id, expanded.size > 0);
  const linesByCycle = new Map(progress.cycles.map((c) => [c.cycle.id, c.lines]));

  if (cycles.length === 0) {
    return (
      <Typography variant="body2" color="text.secondary">
        No cycle has been dispatched yet — the run is waiting on its dispatch
        predicate.
      </Typography>
    );
  }

  let tail: string | undefined;
  if (progress.phase === "connecting") {
    tail = "attaching to the run feed…";
  } else if (progress.phase === "reconnecting") {
    tail = "connection lost — reconnecting…";
  }

  return (
    <Box>
      {cycles.map((cycle, i) => (
        <CycleSection
          key={cycle.id}
          projectName={projectName}
          tag={tag}
          cycle={cycle}
          index={i}
          lines={linesByCycle.get(cycle.id) ?? []}
          expanded={expanded.has(cycle.id)}
          onToggle={(open) => toggle(cycle.id, open)}
        />
      ))}
      {tail && (
        <Typography variant="caption" color="text.secondary" sx={{ pl: 3.25 }}>
          {tail}
        </Typography>
      )}
    </Box>
  );
}
