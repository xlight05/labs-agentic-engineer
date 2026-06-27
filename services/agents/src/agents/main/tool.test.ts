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

import { test } from "node:test";
import assert from "node:assert/strict";
import type { Skill, LoadSkillResult } from "@aep/contracts";
import { buildTools, LOAD_SKILL } from "./tool.js";
import { FileBundle } from "./bundle.js";

const SKILLS: Skill[] = [
  { name: "component-architecture", description: "deriving components", content: "Components live at specs/design/components/<name>/design.md." },
  { name: "openapi-conventions", description: "openapi", content: "operationId is lowerCamelCase" },
];

type LoadSkillExec = (i: { name: string }, o: unknown) => Promise<LoadSkillResult>;
const loadSkillExec = (skills: Skill[]): LoadSkillExec =>
  buildTools(new FileBundle({}), skills)[LOAD_SKILL]!.execute as unknown as LoadSkillExec;

test("buildTools omits loadSkill when no skills are supplied (skill-free = today)", () => {
  const tools = buildTools(new FileBundle({}));
  assert.equal(LOAD_SKILL in tools, false);
  // The four file-mutation tools are always present.
  assert.ok(tools.addFile && tools.editFile && tools.removeFile && tools.setFrontmatterField);
});

test("buildTools registers loadSkill when skills are supplied", () => {
  assert.ok(buildTools(new FileBundle({}), SKILLS)[LOAD_SKILL]);
});

test("loadSkill returns the body for a known name", async () => {
  const res = await loadSkillExec(SKILLS)({ name: "component-architecture" }, {});
  assert.equal(res.ok, true);
  if (res.ok) {
    assert.equal(res.name, "component-architecture");
    assert.match(res.content, /specs\/design\/components/);
  }
});

test("loadSkill returns a self-correctable miss (with available names) for an unknown name", async () => {
  const res = await loadSkillExec(SKILLS)({ name: "nope" }, {});
  assert.equal(res.ok, false);
  if (!res.ok) {
    assert.match(res.error, /unknown skill: nope/);
    assert.deepEqual(res.available, ["component-architecture", "openapi-conventions"]);
  }
});
