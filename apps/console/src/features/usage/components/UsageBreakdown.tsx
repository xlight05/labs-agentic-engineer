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

import { Box, Divider, Stack, Typography } from "@wso2/oxygen-ui";
import {
  formatTokens,
  formatUsd,
  totalTokens,
  type PhaseUsage,
  type Usage,
} from "../lib/format";

// The three SDLC phases, in pipeline order (#291), each with its user-facing
// label. Shown as a small secondary split under the total token breakdown.
const PHASES: [keyof PhaseUsage, string][] = [
  ["spec", "Spec / design"],
  ["build", "Build"],
  ["validation", "Validation"],
];

// One phase's figure: its stamped USD, or a token count when the phase ran on
// rows the platform could not price (null cost).
function phaseFigure(u: Usage): string {
  return u.costUsd !== null ? formatUsd(u.costUsd) : `${formatTokens(totalTokens(u))} tok`;
}

// The detailed token view behind every folded cost figure (#245): the
// input/output/cache split that explains why agentic token counts look large,
// plus (#291) the per-phase cost split when `phases` is supplied. `context`
// names WHAT the figure covers ("Agent spend — Storefront") so no number
// ever floats without a scope.
export function UsageBreakdown({
  usage,
  phases,
  context,
}: {
  usage: Usage;
  // `| undefined` keeps pass-through from optional props legal under
  // exactOptionalPropertyTypes.
  phases?: PhaseUsage | undefined;
  context?: string | undefined;
}) {
  const rows: [string, number][] = [
    ["Input", usage.inputTokens],
    ["Output", usage.outputTokens],
    ["Cache read", usage.cacheReadTokens],
    ["Cache write", usage.cacheCreationTokens],
  ];
  return (
    <Stack spacing={0.25} sx={{ py: 0.25 }}>
      {context && (
        <Typography variant="caption" sx={{ fontWeight: 600, mb: 0.25 }}>
          {context}
        </Typography>
      )}
      {rows.map(([label, tokens]) => (
        <Box
          key={label}
          sx={{ display: "flex", justifyContent: "space-between", gap: 2 }}
        >
          <Typography variant="caption">{label}</Typography>
          <Typography
            variant="caption"
            sx={{ fontVariantNumeric: "tabular-nums" }}
          >
            {formatTokens(tokens)} tok
          </Typography>
        </Box>
      ))}
      {usage.model && (
        <Typography variant="caption" color="inherit" sx={{ opacity: 0.7 }}>
          {usage.model}
        </Typography>
      )}
      {usage.costUsd === null && (
        <Typography variant="caption" sx={{ opacity: 0.7 }}>
          No stamped cost — this usage predates pricing or its model had no
          rate.
        </Typography>
      )}
      {phases && (
        <>
          <Divider sx={{ my: 0.5 }} />
          <Typography
            variant="caption"
            sx={{ fontWeight: 600, opacity: 0.7, fontSize: "0.65rem" }}
          >
            Cost by phase
          </Typography>
          {PHASES.map(([key, label]) => (
            <Box
              key={key}
              sx={{ display: "flex", justifyContent: "space-between", gap: 2 }}
            >
              <Typography variant="caption" sx={{ fontSize: "0.65rem", opacity: 0.8 }}>
                {label}
              </Typography>
              <Typography
                variant="caption"
                sx={{ fontSize: "0.65rem", opacity: 0.8, fontVariantNumeric: "tabular-nums" }}
              >
                {phaseFigure(phases[key])}
              </Typography>
            </Box>
          ))}
        </>
      )}
    </Stack>
  );
}
