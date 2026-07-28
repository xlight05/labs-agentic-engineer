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

/**
 * Instruction composition — the playground mirrors what aep-api composes
 * server-side for every console turn (docs/design/playground.md §9):
 *
 *   instruction + steeringByUseCase["general"] + collabDepsSteer + targetSuffix
 *
 * and for the plan turn: planInstruction + renderPlanContext(contextFiles).
 *
 * The three live steer strings below are VERBATIM COPIES of the Go constants
 * (extraction was rejected as overkill — see §9). Each carries a provenance
 * comment; `test/steer-parity.test.ts` asserts every copy still appears
 * verbatim in its Go source file, so drift fails CI without any build
 * coupling. The canned phase prompts are shared for real via
 * `@aep/contracts/prompts`.
 */

export { buildSpecGenerationInstruction, buildDesignGenerationInstruction } from "@aep/contracts/prompts";

// MUST match steeringByUseCase[useCaseGeneral],
// services/aep-api/internal/spec/steering.go
export const GENERAL_STEER =
  "\n\nApply the requested change to the spec bundle, or answer the question if no change is requested. " +
  "Spec sources live under specs/ (requirements under specs/requirements/, design under specs/design/) — when creating a file that does not exist yet, always use its full path, never a bare filename.";

// MUST match collabDepsSteer,
// services/aep-api/internal/spec/steering.go
export const COLLAB_DEPS_STEER =
  "\n\nBefore editing any design.json, load the `high-level-architecture` skill. " +
  "When your change adds or edits a component's `dependencies` — especially an `org-service` referencing another project — " +
  "FIRST call `list_org_endpoints` and copy the provider component name VERBATIM; never invent a role-based name. " +
  "When the requested change ALTERS THE ARCHITECTURE — a component added, removed, or renamed; an edge, exposure, or external/SaaS dependency changed — " +
  "load the `cell-architecture-dsl` skill and update specs/design/design.cell FIRST with targeted editFile edits (removeFile + ONE addFile only when restructuring MOST of the diagram), " +
  "then update design.md and every affected design.json to match; do not narrate the process.";

// MUST match planInstruction,
// services/aep-api/internal/delivery/task/plan.go
export const PLAN_INSTRUCTION =
  "Plan the implementation Tasks for this project. Load the task-planning skill and follow it: create one Task per design component with planTask, wire dependsOn by component name, and write each Task's body with updateTask in the same turn. The design is under specs/design/ and the requirements under specs/requirements/. Existing open Tasks (if any) are listed at the end of this message for reference — add Tasks ONLY for components they do not cover, and do not recreate or update the listed Tasks in this turn. Never invent a component the design does not define.";

/** Mirrors targetSuffix, services/aep-api/internal/spec/genai_service.go. */
export function targetSuffix(target: string | undefined): string {
  if (!target || target.trim() === "") return "";
  return "\n\n(target: " + target + ")";
}

/**
 * The full server-side composition every console spec turn gets (all console
 * turns are collab turns, so `collabDepsSteer` always rides — kept here for
 * byte parity even though the playground runs no MCP by default; §10).
 */
export function composeSpecInstruction(text: string, target?: string): string {
  return text + GENERAL_STEER + COLLAB_DEPS_STEER + targetSuffix(target);
}

/**
 * Mirrors renderPlanContext, services/aep-api/internal/delivery/task/plan.go:
 * existing-task renderings (tasks/<n>.md) appended to the plan instruction as
 * deterministic sections — plan context is platform state and rides the
 * INSTRUCTION, never the snapshot.
 */
export function renderPlanContext(files: Record<string, string>): string {
  const paths = Object.keys(files);
  if (paths.length === 0) return "";
  paths.sort();
  let out = "\n\n## Existing open Tasks in this version (reference)\n";
  for (const p of paths) {
    out += `\n--- ${p} ---\n${files[p]}\n`;
  }
  return out;
}

/** The plan turn's full instruction (production channel, §5 phase 3). */
export function composePlanInstruction(contextFiles: Record<string, string>): string {
  return PLAN_INSTRUCTION + renderPlanContext(contextFiles);
}
