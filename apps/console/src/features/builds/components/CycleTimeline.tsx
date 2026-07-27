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

import { Box, Chip, Stack, Typography } from "@wso2/oxygen-ui";
import type { components } from "../../../generated/aep-api";
import { cycleLabel } from "../lib/runView";

type RunCycleView = components["schemas"]["RunCycleView"];

// The cycle timeline: one row per dispatch, oldest first — the run's loop
// POSITION, which is deliberately never stored as an enum. A fix or conflict
// cycle re-enters an earlier phase of the loop, so only the latest cycle can
// say where the run actually is.
//
// Branch, PR number and merge SHA are LEARNED FROM WEBHOOKS (the agent derives
// its own branch identity), so an empty column is a fact — that cycle's agent
// has not got there yet, or died before it did — and renders as an em-dash
// rather than being hidden.

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

export function CycleTimeline({ cycles }: { cycles: RunCycleView[] }) {
  if (cycles.length === 0) {
    return (
      <Typography variant="body2" color="text.secondary">
        No cycle has been dispatched yet — the run is waiting on its dispatch
        predicate.
      </Typography>
    );
  }

  return (
    <Stack spacing={0}>
      {cycles.map((cycle, i) => {
        const running = !cycle.endedAt;
        return (
          <Stack
            key={cycle.id}
            direction="row"
            spacing={1.5}
            sx={{ alignItems: "flex-start", py: 1 }}
          >
            {/* Rail: a dot per cycle, joined by a line so the column reads as
                one sequence rather than a stack of unrelated rows. */}
            <Box
              sx={{
                display: "flex",
                flexDirection: "column",
                alignItems: "center",
                alignSelf: "stretch",
                pt: 0.75,
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
              {i < cycles.length - 1 && (
                <Box sx={{ flexGrow: 1, width: "1px", bgcolor: "divider", mt: 0.5 }} />
              )}
            </Box>
            <Box sx={{ minWidth: 0, flexGrow: 1 }}>
              <Stack
                direction="row"
                spacing={1}
                sx={{ alignItems: "center", flexWrap: "wrap" }}
              >
                <Typography variant="subtitle2">
                  {cycleLabel(cycle, i)}
                </Typography>
                {cycle.attempts > 1 && (
                  // The per-cycle re-dispatch budget: 2 dispatches, reset at
                  // every cycle boundary. A second attempt means the first
                  // agent died.
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
          </Stack>
        );
      })}
    </Stack>
  );
}
