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
import { FileBundle } from "../src/bundle.js";
import { SEED_FILES } from "./seed.js";
import {
  toChange,
  applyToolCall,
  isFileMutationTool,
  opForTool,
  readToolInputPath,
} from "../src/change.js";
import type { StreamPart } from "../src/stream-types.js";

const OPENAPI = "specs/design/components/hello-api/openapi.yaml";

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

test("readToolInputPath resolves the path once its string closes, decoding escapes", () => {
  // Path string still open → not resolvable yet.
  assert.equal(readToolInputPath('{"path":"specs/req'), undefined);
  // Path closed (content just starting) → resolved even though the object isn't.
  assert.equal(
    readToolInputPath('{"path":"specs/requirements/requirements.md","content":"# R'),
    "specs/requirements/requirements.md",
  );
  // JSON escapes in the path are decoded.
  assert.equal(readToolInputPath('{"path":"a\\"b.md"'), 'a"b.md');
  // No path key present yet.
  assert.equal(readToolInputPath("{"), undefined);
  assert.equal(readToolInputPath(""), undefined);
});

test("opForTool maps file tools to ops (unknown → edit)", () => {
  assert.equal(opForTool("addFile"), "add");
  assert.equal(opForTool("editFile"), "edit");
  assert.equal(opForTool("removeFile"), "remove");
  assert.equal(opForTool("loadSkill"), "edit");
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

test("isFileMutationTool: true for the mutation ops, false for loadSkill / unknown (no Change)", () => {
  for (const t of ["addFile", "editFile", "removeFile"]) {
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
