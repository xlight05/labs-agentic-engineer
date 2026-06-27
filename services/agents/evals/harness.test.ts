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
 * Deterministic harness integration test — drives the WHOLE eval pipeline
 * (boot app → streamTurn → reconstruct-from-stream → score) over the real SSE
 * route with a MOCK model (no tokens). Proves the reconstruction + scoring
 * contract before the real-model run; the only thing the real run adds is the
 * model's behavior.
 */

import { test } from "node:test";
import assert from "node:assert/strict";
import { runFixture, type EvalSuite } from "./harness.js";
import type { Fixture } from "./fixture.js";
import { SEED_FILES } from "../src/agents/main/prompt.js";
import { mockModel } from "../src/shared/mock-model.js";

const OPENAPI = "specs/design/components/hello-api/openapi.yaml";
const suite: EvalSuite = { agent: "main", fixturesDir: "", defaultSeed: SEED_FILES };

test("single-turn: reconstruct-from-stream + score passes over the real route", async () => {
  const fixture: Fixture = {
    name: "smoke-edit",
    turns: [{ prompt: "rename the example" }],
    expect: {
      filesContain: [{ path: OPENAPI, text: "Hi there!" }],
      filesNotContain: [{ path: OPENAPI, text: 'example: "Hello, World!"' }],
      touched: [OPENAPI],
      yamlValid: [OPENAPI],
      preferEdit: true,
    },
  };
  const model = mockModel([
    {
      kind: "toolCall",
      toolCallId: "c1",
      toolName: "editFile",
      input: { path: OPENAPI, oldString: 'example: "Hello, World!"', newString: 'example: "Hi there!"' },
    },
    { kind: "text", text: "done" },
  ]);

  const result = await runFixture(suite, fixture, { model, samples: 1 });
  assert.equal(result.passed, 1, JSON.stringify(result.sampleResults[0]?.turns, null, 2));
  assert.equal(result.passRate, 1);
});

test("captured-trace continuation: pre-seeds the store, reconstructs prior turns", async () => {
  // Turn 1 recorded as messages (adds an `enabled` frontmatter via setFrontmatterField),
  // turn 2 (mock) edits it. Proves messages pre-seed + reconstruction of prior tool-calls.
  const fixture: Fixture = {
    name: "smoke-continuation",
    messages: [
      { role: "user", content: "add a notes file" },
      {
        role: "assistant",
        content: [
          {
            type: "tool-call",
            toolCallId: "t1",
            toolName: "addFile",
            input: { path: "specs/design/components/hello-api/notes.md", content: "draft\n" },
          },
        ],
      },
      {
        role: "tool",
        content: [
          {
            type: "tool-result",
            toolCallId: "t1",
            toolName: "addFile",
            output: { type: "json", value: { ok: true, op: "add", path: "specs/design/components/hello-api/notes.md", status: "applied" } },
          },
        ],
      },
    ],
    turns: [{ prompt: "edit the notes" }],
    expect: {
      filesContain: [{ path: "specs/design/components/hello-api/notes.md", text: "final" }],
    },
  };
  const model = mockModel([
    {
      kind: "toolCall",
      toolCallId: "c1",
      toolName: "editFile",
      input: { path: "specs/design/components/hello-api/notes.md", oldString: "draft\n", newString: "final\n" },
    },
    { kind: "text", text: "done" },
  ]);

  const result = await runFixture(suite, fixture, { model, samples: 1 });
  assert.equal(result.passed, 1, JSON.stringify(result.sampleResults[0]?.turns, null, 2));
});

test("skills: agent loads a skill, applies an edit; toolsUsed scores, loadSkill not harvested as an OpResult", async () => {
  const skills = [
    { name: "component-architecture", description: "deriving components", content: "Components live at specs/design/components/<name>/design.md." },
  ];
  const fixture: Fixture = {
    name: "skill-apply",
    turns: [{ prompt: "use the relevant skill, then edit" }],
    expect: {
      toolsUsed: ["loadSkill"],
      filesContain: [{ path: OPENAPI, text: "Hi there!" }],
      // preferEdit + noToolErrors only pass if the loadSkill result is excluded
      // from the OpResult harvest (it is not an add and not an error).
      preferEdit: true,
      noToolErrors: true,
    },
  };
  const model = mockModel([
    { kind: "toolCall", toolCallId: "s1", toolName: "loadSkill", input: { name: "component-architecture" } },
    {
      kind: "toolCall",
      toolCallId: "c1",
      toolName: "editFile",
      input: { path: OPENAPI, oldString: 'example: "Hello, World!"', newString: 'example: "Hi there!"' },
    },
    { kind: "text", text: "done" },
  ]);

  const result = await runFixture(suite, fixture, { model, samples: 1, skills });
  assert.equal(result.passed, 1, JSON.stringify(result.sampleResults[0]?.turns, null, 2));
});
