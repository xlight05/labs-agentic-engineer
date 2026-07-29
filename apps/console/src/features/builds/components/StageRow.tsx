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

import { Box, Stack, Typography, alpha } from "@wso2/oxygen-ui";
import type { StatusTone } from "../../../components/StatusChip";
import { stageTone, type SpineStage, type StageState } from "../lib/stage";

// One stage on the rail, and the rail itself.
//
// The dot is the state, the line is the sequence, and the STEP NUMBER is the
// order said out loud: a run is one numbered flow from its first connection to
// its last deployment, so the reader can see where they are in it without
// counting dots. There is exactly one rail per run — no nested sub-spines and no
// summary strip of dots, both of which asked the reader to hold two orderings in
// mind at once.

const DOT_SIZE = 9;

// The rail is a CONNECTOR, not a divider: it is what makes a column of dots read
// as one sequence rather than as unrelated sections, which is the whole claim of
// this layout. The `divider` token is tuned for hairlines between rows and at 1px
// on a tinted card it measured as roughly 2% contrast — invisible, leaving the
// dots floating. So the rail derives its own weight from the text colour.
const RAIL_WIDTH = 2;
const railColor = (theme: { palette: { text: { primary: string } } }) =>
  alpha(theme.palette.text.primary, 0.16);

/** The palette family a tone reads from; neutral has none and uses text. */
function dotColor(tone: StatusTone): string {
  return tone === "neutral" ? "text.disabled" : `${tone}.main`;
}

/**
 * A stage's dot. A stage in flight is HOLLOW and pulses; everything settled is
 * solid. Motion is reserved for the one stage that is actually moving, so a page
 * of finished sessions is still.
 */
export function StageDot({ state }: { state: StageState }) {
  const tone = stageTone(state);
  const live = state === "active";
  return (
    <Box
      sx={{
        width: DOT_SIZE,
        height: DOT_SIZE,
        borderRadius: "50%",
        flexShrink: 0,
        boxSizing: "border-box",
        bgcolor: live ? "transparent" : dotColor(tone),
        border: live ? 2 : 0,
        borderColor: dotColor(tone),
        // A waiting stage is drawn but quiet — it is a placeholder for something
        // that has not happened, not a thing that failed to happen.
        opacity: state === "waiting" ? 0.45 : 1,
        "@keyframes stagePulse": { "50%": { opacity: 0.35 } },
        animation: live ? "stagePulse 1.8s ease-in-out infinite" : "none",
        "@media (prefers-reduced-motion: reduce)": { animation: "none" },
      }}
    />
  );
}

/**
 * A label BETWEEN stages, carrying the rail through it — the marker that says a
 * later build session starts here.
 *
 * It has no dot on purpose: a dot is a stage, and this is not one. What it is
 * doing is naming a re-entry, because a fix or conflict session runs the same
 * five stages again and without a boundary the rail would read as the agent
 * going backwards.
 */
export function SpineLabel({ children }: { children: React.ReactNode }) {
  return (
    <Stack direction="row" spacing={1.5} sx={{ alignItems: "stretch" }}>
      <Stack sx={{ alignItems: "center", width: DOT_SIZE }}>
        <Box
          sx={(theme) => ({
            flexGrow: 1,
            width: RAIL_WIDTH,
            borderRadius: RAIL_WIDTH,
            bgcolor: railColor(theme),
          })}
        />
      </Stack>
      <Box sx={{ minWidth: 0, flexGrow: 1, pb: 2 }}>
        <Typography
          variant="caption"
          color="text.secondary"
          sx={{ textTransform: "uppercase", letterSpacing: "0.06em" }}
        >
          {children}
        </Typography>
      </Box>
    </Stack>
  );
}

/**
 * One stage on the rail: its step number, its dot, its name, who is acting, the
 * fact it learned, and one sentence of what it is waiting for or what it did.
 *
 * `children` is the stage's own detail — the agent's log, the build rows, the
 * issues it worked — so a stage's evidence renders inside the stage that owns it
 * rather than in a section of its own further down the page.
 */
export function StageRow({
  stage,
  step,
  last = false,
  children,
}: {
  stage: SpineStage;
  /** This stage's position in the run's one numbered flow, 1-based. */
  step: number;
  /** The last stage draws no continuing rail below it. */
  last?: boolean;
  children?: React.ReactNode;
}) {
  const tone = stageTone(stage.state);
  const muted = stage.state === "waiting";
  return (
    <Stack direction="row" spacing={1.5} sx={{ alignItems: "stretch" }}>
      <Stack sx={{ alignItems: "center", pt: 0.75, width: DOT_SIZE }}>
        <StageDot state={stage.state} />
        {!last && (
          <Box
            sx={(theme) => ({
              flexGrow: 1,
              width: RAIL_WIDTH,
              borderRadius: RAIL_WIDTH,
              bgcolor: railColor(theme),
              mt: 0.5,
            })}
          />
        )}
      </Stack>
      <Box sx={{ minWidth: 0, flexGrow: 1, pb: last ? 0 : 2 }}>
        <Stack
          direction="row"
          spacing={1}
          sx={{ alignItems: "baseline", flexWrap: "wrap", rowGap: 0.25 }}
        >
          {/* Fixed width and tabular figures so the names line up whether the
              run is on step 3 or step 13. */}
          <Typography
            variant="caption"
            color="text.disabled"
            data-testid="stage-step"
            sx={{ fontVariantNumeric: "tabular-nums", minWidth: 16, flexShrink: 0 }}
          >
            {step}
          </Typography>
          <Typography variant="subtitle2" color={muted ? "text.secondary" : "text.primary"}>
            {stage.name}
          </Typography>
          {stage.fact && (
            // A fact that names something on the host is a LINK to it — the
            // pull request number a reader wants to open is the same string
            // they are already looking at, so it needs no row of its own. When
            // the platform recorded no URL there is nothing to open, and the
            // fact stays plain text rather than becoming a dead anchor.
            <Typography
              variant="caption"
              {...(stage.factHref
                ? {
                    component: "a",
                    href: stage.factHref,
                    target: "_blank",
                    rel: "noreferrer",
                    "aria-label": `${stage.name} ${stage.fact}`,
                  }
                : {})}
              sx={{
                fontFamily: "monospace",
                fontVariantNumeric: "tabular-nums",
                color: muted ? "text.disabled" : `${tone}.main`,
                ...(stage.factHref && {
                  textDecoration: "none",
                  "&:hover": { textDecoration: "underline" },
                }),
              }}
            >
              {stage.fact}
            </Typography>
          )}
          <Box sx={{ flexGrow: 1 }} />
          <Typography variant="caption" color="text.disabled">
            {stage.actor}
          </Typography>
        </Stack>
        {stage.note && (
          <Typography
            variant="body2"
            color={muted ? "text.disabled" : "text.secondary"}
            sx={{ fontStyle: muted ? "italic" : "normal", mt: 0.25 }}
          >
            {stage.note}
          </Typography>
        )}
        {children && <Box sx={{ mt: 1.5 }}>{children}</Box>}
      </Box>
    </Stack>
  );
}
