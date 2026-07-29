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
 * Drift guards for the "duplicated + pinned" copies — read-the-source
 * assertions, zero build coupling:
 *
 *  1. Every TS steer constant must appear verbatim in its Go source file.
 *     Go splits long strings into concatenated literals, so the check
 *     extracts every interpreted string literal from the file, unescapes it,
 *     and joins adjacent literals — the copied constant must be a substring.
 *  2. The coding-run prompt's skill-pointer clause must appear in BOTH the Go
 *     prompt builder and the playground's local dispatch.
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

// --- the coding-run prompt's skill pointer, either side of a language boundary
//
// The platform's coding prompt is authored in Go (the BFF builds it and stamps
// it as AEP_PROMPT); the playground's is authored in TypeScript (local.ts builds
// its own dispatch). Both are deliberately just a subject plus a pointer at the
// `aep` skill for the procedure — so the pointer clause is the one string that
// has to survive in both, and a rename of the skill has to move both. The skill
// BODIES no longer need a parity test: there is one authored SKILL.md now, and
// the mode blocks in it are checked by
// runners/remote-worker/src/lib/skill_compose.test.ts.

const SKILL_POINTER = "Follow the `aep` skill loaded in your session — it defines discovery, ordering, fan-out";

test("the Go coding prompt points at the aep skill for the procedure", () => {
  const joined = joinedGoLiterals("services/aep-api/internal/delivery/codingagent/coding_executor.go");
  assert.ok(joined.includes(SKILL_POINTER), "buildPrompt no longer carries the shared skill-pointer clause");
});

test("the playground's local dispatch points at the same skill, the same way", () => {
  const localEntry = readFileSync(join(REPO_ROOT, "runners/remote-worker/src/local.ts"), "utf8");
  // local.ts splits the prompt across two adjacent string literals; join them the
  // way the Go check does before looking for the clause.
  const joined = [...localEntry.matchAll(/"((?:[^"\\\n]|\\.)*)"/g)].map((m) => m[1] ?? "").join("");
  assert.ok(joined.includes(SKILL_POINTER), "local.ts's prompt drifted from the shared skill-pointer clause");
});
