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
import { FileBundle } from "./bundle.js";
import { SEED_FILES } from "./prompt.js";
import { toChange, applyToolCall, isFileMutationTool } from "./change.js";
import type { StreamPart } from "./stream-types.js";

const OPENAPI = "specs/design/components/hello-api/openapi.yaml";
const DESIGN = "specs/design/design.md";

test("applyToolCall reconstructs file state by folding an editFile call (matches a direct mutation)", () => {
  const direct = new FileBundle(SEED_FILES);
  direct.editFile(OPENAPI, 'example: "Hello, World!"', 'example: "Hi there!"');

  const folded = new FileBundle(SEED_FILES);
  applyToolCall(folded, {
    type: "tool-call",
    toolCallId: "call_1",
    toolName: "editFile",
    input: { path: OPENAPI, oldString: 'example: "Hello, World!"', newString: 'example: "Hi there!"' },
  });

  // Folding the streamed call through a fresh bundle reproduces the server's state
  // with no second matcher — the eval relies on this (newContent is gone, §5).
  assert.deepEqual(folded.snapshot(), direct.snapshot());
  assert.ok(folded.read(OPENAPI)!.includes('"Hi there!"'));
});

test("applyToolCall folds add then remove in stream order", () => {
  const b = new FileBundle(SEED_FILES);
  const path = "specs/design/components/hello-api/notes.md";

  applyToolCall(b, {
    type: "tool-call",
    toolCallId: "c1",
    toolName: "addFile",
    input: { path, content: "hi\n" },
  });
  assert.equal(b.read(path), "hi\n");

  applyToolCall(b, { type: "tool-call", toolCallId: "c2", toolName: "removeFile", input: { path } });
  assert.equal(b.has(path), false);
});

test("applyToolCall folds setFrontmatterField (array value)", () => {
  const b = new FileBundle(SEED_FILES);
  applyToolCall(b, {
    type: "tool-call",
    toolCallId: "c1",
    toolName: "setFrontmatterField",
    input: { path: DESIGN, key: "language", value: "TypeScript" },
  });
  assert.ok(b.read(DESIGN)!.includes("language: TypeScript"));
});

test("applyToolCall ignores malformed / unknown tool calls", () => {
  const b = new FileBundle(SEED_FILES);
  const before = b.snapshot();
  applyToolCall(b, { type: "tool-call", toolName: "editFile", input: { path: OPENAPI } }); // missing strings
  applyToolCall(b, { type: "tool-call", toolName: "bogusTool", input: { path: OPENAPI } });
  applyToolCall(b, { type: "tool-call", toolName: "addFile" }); // no input
  assert.deepEqual(b.snapshot(), before);
});

test("toChange projects a tool-result part into a reviewable Change", () => {
  const part: StreamPart = {
    type: "tool-result",
    toolCallId: "call_1",
    toolName: "editFile",
    input: { path: OPENAPI, oldString: "a", newString: "b" },
    output: { ok: true, op: "edit", path: OPENAPI, status: "applied" },
  };
  const c = toChange(part);
  assert.equal(c.toolCallId, "call_1");
  assert.equal(c.toolName, "editFile");
  assert.equal(c.op, "edit");
  assert.equal(c.path, OPENAPI);
  assert.equal(c.oldString, "a");
  assert.equal(c.newString, "b");
  assert.equal(c.result.ok, true);
});

test("toChange derives op from the tool name when output is absent", () => {
  const c = toChange({
    type: "tool-call",
    toolCallId: "c2",
    toolName: "addFile",
    input: { path: "specs/x.md", content: "hi\n" },
  });
  assert.equal(c.op, "add");
  assert.equal(c.content, "hi\n");
});

test("isFileMutationTool: true for the four ops, false for loadSkill / unknown (no Change)", () => {
  for (const t of ["addFile", "editFile", "removeFile", "setFrontmatterField"]) {
    assert.equal(isFileMutationTool(t), true, t);
  }
  assert.equal(isFileMutationTool("loadSkill"), false);
  assert.equal(isFileMutationTool("ask_question"), false);
  assert.equal(isFileMutationTool(""), false);
});

test("applyToolCall ignores a loadSkill call (not a bundle mutation)", () => {
  const b = new FileBundle(SEED_FILES);
  const before = b.snapshot();
  applyToolCall(b, { type: "tool-call", toolName: "loadSkill", input: { name: "hello-greeting" } });
  assert.deepEqual(b.snapshot(), before);
});
