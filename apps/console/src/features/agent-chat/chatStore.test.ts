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

import { beforeEach, describe, expect, it } from "vitest";

// Node test env: the store only touches localStorage — a Map-backed stub
// keeps the test out of jsdom.
const backing = new Map<string, string>();
globalThis.localStorage = {
  getItem: (k: string) => backing.get(k) ?? null,
  setItem: (k: string, v: string) => void backing.set(k, v),
  removeItem: (k: string) => void backing.delete(k),
  clear: () => backing.clear(),
  key: (i: number) => [...backing.keys()][i] ?? null,
  get length() {
    return backing.size;
  },
} as Storage;
import {
  addMessage,
  appendAssistantText,
  chatKeyFor,
  conversationIdFor,
  dropTurnOutput,
  getMessages,
  setTurnStatus,
  subscribe,
  upsertToolMessage,
} from "./chatStore";

let n = 0;
function freshKey(): string {
  n += 1;
  return chatKeyFor("test-org", `proj-${n}`);
}

beforeEach(() => localStorage.clear());

describe("chatStore", () => {
  it("appends and notifies subscribers", () => {
    const key = freshKey();
    let notified = 0;
    subscribe(key, () => (notified += 1));
    addMessage(key, { role: "user", content: "hi", turnId: "t1", status: "in_flight" });
    expect(getMessages(key)).toHaveLength(1);
    expect(notified).toBe(1);
  });

  it("accumulates streamed text into one assistant message per turn", () => {
    const key = freshKey();
    appendAssistantText(key, "t1", "Hello ");
    appendAssistantText(key, "t1", "world");
    appendAssistantText(key, "t2", "next turn");
    const msgs = getMessages(key);
    expect(msgs).toHaveLength(2);
    expect(msgs[0]).toMatchObject({ role: "assistant", content: "Hello world" });
    expect(msgs[1]).toMatchObject({ role: "assistant", content: "next turn" });
  });

  it("marks the turn's user bubble on terminal", () => {
    const key = freshKey();
    addMessage(key, { role: "user", content: "do it", turnId: "t1", status: "in_flight" });
    setTurnStatus(key, "t1", "completed");
    expect(getMessages(key)[0]).toMatchObject({ status: "completed" });
  });

  it("dropTurnOutput removes streamed output but keeps the user bubble", () => {
    const key = freshKey();
    addMessage(key, { role: "user", content: "go", turnId: "t1", status: "in_flight" });
    appendAssistantText(key, "t1", "partial");
    addMessage(key, {
      role: "tool",
      turnId: "t1",
      toolCallId: "c1",
      status: "done",
      op: "edit",
      path: "requirements/prd.md",
      ok: true,
    });
    dropTurnOutput(key, "t1");
    const msgs = getMessages(key);
    expect(msgs).toHaveLength(1);
    expect(msgs[0]?.role).toBe("user");
  });

  it("upsertToolMessage inserts a streaming card then flips it to done in place", () => {
    const key = freshKey();
    const base = {
      role: "tool" as const,
      turnId: "t1",
      toolCallId: "c1",
      op: "add",
      path: "specs/requirements/requirements.md",
      ok: true,
    };
    upsertToolMessage(key, { ...base, status: "streaming" });
    expect(getMessages(key)).toHaveLength(1);
    expect(getMessages(key)[0]).toMatchObject({ status: "streaming", op: "add" });
    upsertToolMessage(key, { ...base, status: "done" });
    const msgs = getMessages(key);
    expect(msgs).toHaveLength(1); // same card, updated in place — no duplicate row
    expect(msgs[0]).toMatchObject({ status: "done", op: "add" });
  });

  it("upsertToolMessage keeps distinct toolCallIds apart and never matches a blank id", () => {
    const key = freshKey();
    const mk = (toolCallId: string, path: string) => ({
      role: "tool" as const, turnId: "t1", toolCallId, status: "done" as const, op: "add", path, ok: true,
    });
    upsertToolMessage(key, mk("c1", "a.md"));
    upsertToolMessage(key, mk("c2", "b.md"));
    upsertToolMessage(key, mk("", "c.md"));
    upsertToolMessage(key, mk("", "d.md"));
    expect(getMessages(key)).toHaveLength(4);
  });

  it("mints one conversation id per project and persists it", () => {
    expect(conversationIdFor("o", "p", { create: false })).toBeNull();
    const id = conversationIdFor("o", "p", { create: true });
    expect(id).toBeTruthy();
    expect(conversationIdFor("o", "p", { create: false })).toBe(id);
    expect(conversationIdFor("o", "p2", { create: true })).not.toBe(id);
  });
});
