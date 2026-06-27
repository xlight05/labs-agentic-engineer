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
import type { OpResult } from "@aep/contracts";
import { scoreExpect, allPass } from "./scoring.js";

const SEED = { "a.yaml": "x: 1\n", "b.md": "Hello, World!\n" };
const FINAL = { "a.yaml": "x: 1\ny: 2\n", "b.md": "Hi there!\n" };
const okEdit = (path: string): OpResult => ({ ok: true, op: "edit", path, status: "applied" });
const okAdd = (path: string): OpResult => ({ ok: true, op: "add", path, status: "applied" });
const errEdit = (path: string): OpResult => ({ ok: false, op: "edit", path, code: "NOT_FOUND", message: "x" });

function only(checks: { clause: string; pass: boolean }[]): boolean {
  return allPass(checks);
}

test("filesContain: present text passes, missing text and absent file fail", () => {
  assert.ok(only(scoreExpect({ filesContain: [{ path: "b.md", text: "Hi there!" }] }, FINAL, SEED, [])));
  assert.equal(only(scoreExpect({ filesContain: [{ path: "b.md", text: "nope" }] }, FINAL, SEED, [])), false);
  assert.equal(only(scoreExpect({ filesContain: [{ path: "ghost.md", text: "x" }] }, FINAL, SEED, [])), false);
});

test("filesNotContain: gone text passes; still-present fails; absent file passes", () => {
  assert.ok(only(scoreExpect({ filesNotContain: [{ path: "b.md", text: "Hello, World!" }] }, FINAL, SEED, [])));
  assert.equal(only(scoreExpect({ filesNotContain: [{ path: "b.md", text: "Hi there!" }] }, FINAL, SEED, [])), false);
  assert.ok(only(scoreExpect({ filesNotContain: [{ path: "ghost.md", text: "x" }] }, FINAL, SEED, [])));
});

test("filesAbsent: passes only when the file does not exist", () => {
  assert.ok(only(scoreExpect({ filesAbsent: ["ghost.md"] }, FINAL, SEED, [])));
  assert.equal(only(scoreExpect({ filesAbsent: ["a.yaml"] }, FINAL, SEED, [])), false);
});

test("fileMatches: regex source over the file content", () => {
  assert.ok(only(scoreExpect({ fileMatches: [{ path: "a.yaml", pattern: "y:\\s*2" }] }, FINAL, SEED, [])));
  assert.equal(only(scoreExpect({ fileMatches: [{ path: "a.yaml", pattern: "z:\\s*9" }] }, FINAL, SEED, [])), false);
});

test("touched: changed relative to the pre-turn snapshot", () => {
  assert.ok(only(scoreExpect({ touched: ["a.yaml", "b.md"] }, FINAL, SEED, [])));
  // unchanged file is not 'touched'
  assert.equal(only(scoreExpect({ touched: ["a.yaml"] }, SEED, SEED, [])), false);
});

test("yamlValid: parses valid YAML, fails on broken", () => {
  assert.ok(only(scoreExpect({ yamlValid: ["a.yaml"] }, FINAL, SEED, [])));
  assert.equal(only(scoreExpect({ yamlValid: ["bad"] }, { bad: "a: : :\n" }, SEED, [])), false);
});

test("noToolErrors: fails when any result is not ok", () => {
  assert.ok(only(scoreExpect({ noToolErrors: true }, FINAL, SEED, [okEdit("a.yaml")])));
  assert.equal(only(scoreExpect({ noToolErrors: true }, FINAL, SEED, [okEdit("a.yaml"), errEdit("a.yaml")])), false);
});

test("preferEdit: passes when edits >= adds", () => {
  assert.ok(only(scoreExpect({ preferEdit: true }, FINAL, SEED, [okEdit("a.yaml")])));
  assert.equal(only(scoreExpect({ preferEdit: true }, FINAL, SEED, [okAdd("a.yaml")])), false);
});

test("toolsUsed: passes when each named tool was called, fails when one is missing", () => {
  assert.ok(only(scoreExpect({ toolsUsed: ["loadSkill"] }, FINAL, SEED, [], ["loadSkill", "editFile"])));
  assert.ok(only(scoreExpect({ toolsUsed: ["loadSkill", "editFile"] }, FINAL, SEED, [], ["loadSkill", "editFile"])));
  assert.equal(only(scoreExpect({ toolsUsed: ["loadSkill"] }, FINAL, SEED, [], ["editFile"])), false);
});

test("absent clauses are not asserted (vacuous pass)", () => {
  assert.deepEqual(scoreExpect({}, FINAL, SEED, []), []);
  assert.ok(allPass([]));
});
