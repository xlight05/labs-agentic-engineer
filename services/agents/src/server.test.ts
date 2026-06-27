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
 * Deterministic full-route SSE integration test — boots the real Express app
 * with a MOCK model (no tokens) and drives it over `fetch` against an ephemeral
 * port. This is the deterministic end-to-end gate (the real-model eval is the
 * report, Phase 8).
 */

import { test } from "node:test";
import assert from "node:assert/strict";
import type { LanguageModel } from "ai";
import { createApp } from "./server.js";
import { listen0 } from "./shared/listen.js";
import { InMemoryConversationStore } from "./store/memory-store.js";
import { SEED_FILES } from "./agents/main/prompt.js";
import { mockModel } from "./shared/mock-model.js";

const OPENAPI = "specs/design/components/hello-api/openapi.yaml";

async function boot(model: LanguageModel, bodyLimit?: string) {
  const store = new InMemoryConversationStore();
  const app = createApp(bodyLimit ? { store, model, bodyLimit } : { store, model });
  const { baseUrl, close } = await listen0(app.listen(0));
  return { store, baseUrl, close };
}

const jsonPost = (body: unknown) => ({
  method: "POST",
  headers: { "content-type": "application/json" },
  body: JSON.stringify(body),
});

const delay = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

test("POST streams raw StreamPart frames + [DONE], runs execute, persists", async () => {
  const { store, baseUrl, close } = await boot(
    mockModel([
      {
        kind: "toolCall",
        toolCallId: "c1",
        toolName: "editFile",
        input: { path: OPENAPI, oldString: 'example: "Hello, World!"', newString: 'example: "Hi there!"' },
        text: "Updating.",
      },
      { kind: "text", text: "Done." },
    ]),
  );
  try {
    const res = await fetch(`${baseUrl}/conversations/conv1/turns`, jsonPost({ instruction: "rename", files: SEED_FILES }));
    assert.equal(res.status, 200);
    assert.match(res.headers.get("content-type") ?? "", /text\/event-stream/);

    const text = await res.text();
    assert.match(text, /"type":"tool-call"/);
    assert.match(text, /"type":"tool-result"/);
    assert.match(text, /data: \[DONE\]/);

    const stored = await store.get("conv1");
    assert.ok(stored);
    assert.equal(stored.status, "done");
    assert.ok(stored.messages.some((m) => m.role === "tool"));
  } finally {
    await close();
  }
});

test("GET rehydrates the aggregate; 404 for an unknown id", async () => {
  const { baseUrl, close } = await boot(mockModel([{ kind: "text", text: "ok" }]));
  try {
    await (await fetch(`${baseUrl}/conversations/c/turns`, jsonPost({ instruction: "x", files: SEED_FILES }))).text();

    const got = await fetch(`${baseUrl}/conversations/c`);
    assert.equal(got.status, 200);
    const body = (await got.json()) as { status: string; messages: unknown[] };
    assert.equal(body.status, "done");
    assert.ok(Array.isArray(body.messages) && body.messages.length >= 2);

    const missing = await fetch(`${baseUrl}/conversations/does-not-exist`);
    assert.equal(missing.status, 404);
  } finally {
    await close();
  }
});

test("400 when instruction or files is missing", async () => {
  const { baseUrl, close } = await boot(mockModel([{ kind: "text", text: "ok" }]));
  try {
    const r1 = await fetch(`${baseUrl}/conversations/c/turns`, jsonPost({ instruction: "" , files: SEED_FILES }));
    assert.equal(r1.status, 400);
    const r2 = await fetch(`${baseUrl}/conversations/c/turns`, jsonPost({ instruction: "x" }));
    assert.equal(r2.status, 400);
    // non-string file value → clean 400 (not an opaque 500 from FileBundle).
    const r3 = await fetch(`${baseUrl}/conversations/c/turns`, jsonPost({ instruction: "x", files: { "a.md": 123 } }));
    assert.equal(r3.status, 400);
  } finally {
    await close();
  }
});

test("accepts a skills payload; the agent can loadSkill over the route", async () => {
  const { baseUrl, close } = await boot(
    mockModel([
      { kind: "toolCall", toolCallId: "s1", toolName: "loadSkill", input: { name: "component-architecture" } },
      { kind: "text", text: "loaded." },
    ]),
  );
  try {
    const res = await fetch(
      `${baseUrl}/conversations/c/turns`,
      jsonPost({
        instruction: "use the skill",
        files: SEED_FILES,
        skills: [
          { name: "component-architecture", description: "deriving components", content: "Components live at specs/design/components/<name>/design.md." },
        ],
      }),
    );
    assert.equal(res.status, 200);
    const text = await res.text();
    assert.match(text, /"toolName":"loadSkill"/);
    assert.match(text, /specs\/design\/components/); // the loaded body streamed in the tool-result
  } finally {
    await close();
  }
});

test("400 when the skills payload is malformed", async () => {
  const { baseUrl, close } = await boot(mockModel([{ kind: "text", text: "ok" }]));
  try {
    const notArray = await fetch(`${baseUrl}/conversations/c/turns`, jsonPost({ instruction: "x", files: SEED_FILES, skills: "nope" }));
    assert.equal(notArray.status, 400);
    const badItem = await fetch(
      `${baseUrl}/conversations/c/turns`,
      jsonPost({ instruction: "x", files: SEED_FILES, skills: [{ name: "a", description: "b" }] }), // missing content
    );
    assert.equal(badItem.status, 400);
  } finally {
    await close();
  }
});

test("413 when the files snapshot exceeds the body limit", async () => {
  const { baseUrl, close } = await boot(mockModel([{ kind: "text", text: "ok" }]), "1kb");
  try {
    const big = "x".repeat(5000);
    const r = await fetch(`${baseUrl}/conversations/c/turns`, jsonPost({ instruction: "x", files: { "a.md": big } }));
    assert.equal(r.status, 413);
  } finally {
    await close();
  }
});

test("409 when a turn is already in flight for the id", async () => {
  // delayMs keeps turn 1 in-flight so turn 2 hits the in-flight guard.
  const { baseUrl, close } = await boot(
    mockModel([{ kind: "text", text: "a" }, { kind: "text", text: "b" }], { delayMs: 80 }),
  );
  try {
    const opts = jsonPost({ instruction: "x", files: SEED_FILES });
    const p1 = fetch(`${baseUrl}/conversations/c/turns`, opts).then(async (r) => {
      await r.text();
      return r.status;
    });
    await delay(15); // ensure turn 1 acquires the lock first
    const p2 = fetch(`${baseUrl}/conversations/c/turns`, opts).then(async (r) => {
      await r.text();
      return r.status;
    });

    const [s1, s2] = await Promise.all([p1, p2]);
    assert.deepEqual([s1, s2].sort(), [200, 409]);
  } finally {
    await close();
  }
});
