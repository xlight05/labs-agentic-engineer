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

import type { components } from "../../generated/aep-api";
import type { ProjectScenario } from "./project";

type Usage = components["schemas"]["Usage"];
type ProjectUsage = components["schemas"]["ProjectUsage"];

const FABLE = "claude-fable-5";

// The single active model's rates ($/MTok), mirroring aep-api's checked-in
// defaults (deployment-config-overridable there). Cache reads are ~0.1x
// input, cache writes 1.25x, so mock USD figures look like production ones.
const RATES = {
  inputPerMTok: 10,
  outputPerMTok: 50,
  cacheReadPerMTok: 1,
  cacheWritePerMTok: 12.5,
};

// Rate-derived USD, exactly the read-time math aep-api does (ADR-0011).
function priceUsd(u: Omit<Usage, "costUsd" | "model">): number {
  const usd =
    (u.inputTokens * RATES.inputPerMTok +
      u.outputTokens * RATES.outputPerMTok +
      u.cacheReadTokens * RATES.cacheReadPerMTok +
      u.cacheCreationTokens * RATES.cacheWritePerMTok) /
    1_000_000;
  return Math.round(usd * 100) / 100;
}

// Fixture builder: tokens in, priced Usage out. Pass costUsd: null to
// exercise the no-configured-rate state (tokens render without USD).
export function usage(
  inputTokens: number,
  outputTokens: number,
  cacheReadTokens: number,
  cacheCreationTokens: number,
  model = FABLE,
): Usage {
  const tokens = { inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens };
  return { ...tokens, model, costUsd: priceUsd(tokens) };
}

export const zeroUsage: Usage = usage(0, 0, 0, 0);

function sum(items: Usage[], model = FABLE): Usage {
  const totals = items.reduce(
    (acc, u) => ({
      inputTokens: acc.inputTokens + u.inputTokens,
      outputTokens: acc.outputTokens + u.outputTokens,
      cacheReadTokens: acc.cacheReadTokens + u.cacheReadTokens,
      cacheCreationTokens: acc.cacheCreationTokens + u.cacheCreationTokens,
    }),
    { inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheCreationTokens: 0 },
  );
  return { ...totals, model, costUsd: priceUsd(totals) };
}

// Per-task actuals for the building/deployed scenarios — one coding run each,
// cache-heavy the way real agentic runs are. Keyed by issue number.
export const taskUsage: Record<number, Usage> = {
  9: usage(140_000, 88_000, 1_900_000, 260_000), // storefront shell (merged)
  10: usage(95_000, 41_000, 1_150_000, 180_000), // catalog CRUD (in progress, accruing)
  11: usage(210_000, 120_000, 2_600_000, 340_000), // orders payment (failed — burnt before failing)
  // 12 (pending) has no execution yet — exercises the absent-usage cell.
};

const specTurnsAll = usage(180_000, 96_000, 1_400_000, 240_000);
const specDraftCycle = usage(52_000, 27_000, 410_000, 68_000);
const buildingBuildUsage = sum([taskUsage[9]!, taskUsage[10]!, taskUsage[11]!]);
const doneBuildUsage = sum(Object.values(taskUsage));
const validationUsage = usage(48_000, 22_000, 520_000, 70_000);

const noUsageRollup: ProjectUsage = {
  spec: zeroUsage,
  build: zeroUsage,
  validation: zeroUsage,
  draftCycle: zeroUsage,
};

// Per-scenario per-phase actuals for get-project-usage, consistent with the
// tallies in fixtures/project.ts (building = v1 mid-build; deployed = v1 done
// + drifted spec so the draft cycle has fresh spend).
export const projectUsage: Record<
  Exclude<ProjectScenario, "error">,
  ProjectUsage
> = {
  fresh: noUsageRollup,
  spec: {
    spec: specTurnsAll,
    build: zeroUsage,
    validation: zeroUsage,
    draftCycle: specTurnsAll, // nothing published yet — the whole spend is the cycle
  },
  "spec-failed": {
    spec: specDraftCycle,
    build: zeroUsage,
    validation: zeroUsage,
    draftCycle: specDraftCycle,
  },
  building: {
    spec: specTurnsAll,
    build: buildingBuildUsage,
    validation: zeroUsage,
    draftCycle: zeroUsage, // v1 just published; no new spec turns since
  },
  deploying: {
    spec: specTurnsAll,
    build: doneBuildUsage,
    validation: validationUsage,
    draftCycle: zeroUsage,
  },
  deployed: {
    spec: sum([specTurnsAll, specDraftCycle]),
    build: doneBuildUsage,
    validation: validationUsage,
    draftCycle: specDraftCycle, // specs/ drifted past v1 — the v1+ cycle
  },
  "deploy-failed": {
    spec: specTurnsAll,
    build: doneBuildUsage,
    validation: validationUsage,
    draftCycle: zeroUsage,
  },
  "repo-error": noUsageRollup,
};

