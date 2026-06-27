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
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { parseSkill, loadRepoSkills } from "./skills.js";

const here = dirname(fileURLToPath(import.meta.url));
// services/agents/evals → repo root is three up; skills/ lives there (ADR-0002).
const repoSkillsDir = join(here, "..", "..", "..", "skills");

test("parseSkill takes name+description from frontmatter and the (stripped) body as content", () => {
  const raw = "---\nname: foo\ndescription: does foo\n---\n# Foo\n\nbody text\n";
  const s = parseSkill("dir-id", raw);
  assert.equal(s.name, "foo");
  assert.equal(s.description, "does foo");
  assert.match(s.content, /# Foo/);
  assert.match(s.content, /body text/);
  assert.doesNotMatch(s.content, /name: foo/); // frontmatter is stripped from content
});

test("parseSkill falls back to the directory id when frontmatter name is absent", () => {
  const s = parseSkill("my-dir", "just a body, no frontmatter\n");
  assert.equal(s.name, "my-dir");
  assert.equal(s.description, "");
  assert.equal(s.content, "just a body, no frontmatter");
});

test("loadRepoSkills returns [] for a missing directory (skill-free checkout)", () => {
  assert.deepEqual(loadRepoSkills(join(here, "no-such-skills-dir")), []);
});

test("loadRepoSkills reads the committed repo-root skill library", () => {
  const skills = loadRepoSkills(repoSkillsDir);
  const names = skills.map((s) => s.name);
  assert.ok(names.includes("component-architecture"), JSON.stringify(names));
  const arch = skills.find((s) => s.name === "component-architecture")!;
  assert.notEqual(arch.description, "");
  assert.match(arch.content, /specs\/design\/components/);
});
