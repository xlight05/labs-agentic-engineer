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
 * Drift guards for the "duplicated + pinned" copies
 * (docs/design/playground.md §9/§14) — read-the-source assertions, zero build
 * coupling:
 *
 *  1. Every TS steer constant must appear verbatim in its Go source file.
 *     Go splits long strings into concatenated literals, so the check
 *     extracts every interpreted string literal from the file, unescapes it,
 *     and joins adjacent literals — the copied constant must be a substring.
 *  2. The aep-local skill's shared sections (Project structure, Constraints)
 *     must be byte-identical to the production aep skill's.
 */

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { GENERAL_STEER, COLLAB_DEPS_STEER, PLAN_INSTRUCTION } from "../src/engine/compose.js";

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");

/** Concatenation of every interpreted Go string literal in the file, unescaped. */
function joinedGoLiterals(goFile: string): string {
  const source = readFileSync(join(REPO_ROOT, goFile), "utf8");
  const literal = /"((?:[^"\\\n]|\\.)*)"/g;
  let out = "";
  for (const m of source.matchAll(literal)) {
    out += (m[1] ?? "")
      .replaceAll("\\n", "\n")
      .replaceAll("\\t", "\t")
      .replaceAll('\\"', '"')
      .replaceAll("\\\\", "\\");
  }
  return out;
}

test("GENERAL_STEER + COLLAB_DEPS_STEER appear verbatim in genai/steering.go", () => {
  const joined = joinedGoLiterals("services/aep-api/internal/spec/steering.go");
  assert.ok(joined.includes(GENERAL_STEER), "GENERAL_STEER drifted from steeringByUseCase[useCaseGeneral]");
  assert.ok(joined.includes(COLLAB_DEPS_STEER), "COLLAB_DEPS_STEER drifted from collabDepsSteer");
});

test("PLAN_INSTRUCTION appears verbatim in task/plan.go", () => {
  const joined = joinedGoLiterals("services/aep-api/internal/delivery/task/plan.go");
  assert.ok(joined.includes(PLAN_INSTRUCTION), "PLAN_INSTRUCTION drifted from planInstruction");
});

test("targetSuffix + renderPlanContext shapes appear in their Go sources", () => {
  const genai = joinedGoLiterals("services/aep-api/internal/spec/genai_service.go");
  assert.ok(genai.includes("\n\n(target: "), "targetSuffix prefix drifted");
  const plan = joinedGoLiterals("services/aep-api/internal/delivery/task/plan.go");
  assert.ok(plan.includes("\n\n## Existing open Tasks in this version (reference)\n"), "renderPlanContext header drifted");
});

// --- aep ⇄ aep-local shared-section identity --------------------------------

function skillSection(skillFile: string, heading: string): string {
  const text = readFileSync(join(REPO_ROOT, skillFile), "utf8");
  const start = text.indexOf(`\n## ${heading}\n`);
  assert.ok(start >= 0, `${skillFile} has no "## ${heading}" section`);
  const afterStart = start + `\n## ${heading}\n`.length;
  const end = text.indexOf("\n## ", afterStart);
  return text.slice(afterStart, end < 0 ? text.length : end).trim();
}

const AEP_SKILL = "runners/remote-worker/plugin/skills/aep/SKILL.md";
const AEP_LOCAL_SKILL = "runners/remote-worker/plugin-local/skills/aep-local/SKILL.md";

for (const heading of ["Project structure", "Constraints"]) {
  test(`aep-local "${heading}" section is byte-identical to the aep skill's`, () => {
    assert.equal(skillSection(AEP_LOCAL_SKILL, heading), skillSection(AEP_SKILL, heading));
  });
}
