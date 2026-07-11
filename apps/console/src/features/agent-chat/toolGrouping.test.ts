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

import { describe, expect, it } from "vitest";
import type { ChatMessage } from "./chatStore";
import { groupChatItems, type ChatItem } from "./toolGrouping";

const tool = (id: string, path: string, ok = true): ChatMessage => ({
  id,
  role: "tool",
  turnId: "t1",
  toolCallId: id,
  status: "done",
  op: "edit",
  path,
  ok,
});
const assistant = (id: string, content = "…"): ChatMessage => ({
  id,
  role: "assistant",
  turnId: "t1",
  content,
});

// Narrow helper so assertions read cleanly.
function group(
  item: ChatItem | undefined,
): Extract<ChatItem, { kind: "tool-group" }> {
  if (item?.kind !== "tool-group") throw new Error("expected a tool-group");
  return item;
}

describe("groupChatItems", () => {
  it("returns an empty list for no messages", () => {
    expect(groupChatItems([])).toEqual([]);
  });

  it("wraps a lone non-tool message untouched", () => {
    const msg = assistant("a1");
    expect(groupChatItems([msg])).toEqual([{ kind: "message", message: msg }]);
  });

  it("merges a run of consecutive same-file tool calls into one group", () => {
    const items = groupChatItems([
      tool("m1", "components/svc/design.json"),
      tool("m2", "components/svc/design.json"),
      tool("m3", "components/svc/design.json"),
    ]);
    expect(items).toHaveLength(1);
    const g = group(items[0]);
    expect(g.path).toBe("components/svc/design.json");
    expect(g.tools.map((t) => t.id)).toEqual(["m1", "m2", "m3"]);
    // id is stable = first member, so streaming more edits keeps the same key.
    expect(g.id).toBe("m1");
  });

  it("keeps different files in separate groups", () => {
    const items = groupChatItems([
      tool("m1", "a/design.json"),
      tool("m2", "b/openapi.yaml"),
    ]);
    expect(items).toHaveLength(2);
    expect(group(items[0]).tools.map((t) => t.id)).toEqual(["m1"]);
    expect(group(items[1]).tools.map((t) => t.id)).toEqual(["m2"]);
  });

  it("does not merge same-file tools split by narration (timeline preserved)", () => {
    const items = groupChatItems([
      tool("m1", "a/design.json"),
      assistant("a1", "Now tweaking the schema…"),
      tool("m2", "a/design.json"),
    ]);
    expect(items.map((i) => i.kind)).toEqual([
      "tool-group",
      "message",
      "tool-group",
    ]);
    expect(group(items[0]).tools).toHaveLength(1);
    expect(group(items[2]).tools).toHaveLength(1);
  });

  it("closes a group when the file changes then reopens on return", () => {
    const items = groupChatItems([
      tool("m1", "a/design.json"),
      tool("m2", "a/design.json"),
      tool("m3", "b/openapi.yaml"),
      tool("m4", "a/design.json"),
    ]);
    expect(items).toHaveLength(3);
    expect(group(items[0]).tools.map((t) => t.id)).toEqual(["m1", "m2"]);
    expect(group(items[1]).tools.map((t) => t.id)).toEqual(["m3"]);
    expect(group(items[2]).tools.map((t) => t.id)).toEqual(["m4"]);
  });

  it("interleaves plain messages and tool groups in order", () => {
    const a1 = assistant("a1");
    const items = groupChatItems([
      a1,
      tool("m1", "a/design.json"),
      tool("m2", "a/design.json"),
    ]);
    expect(items[0]).toEqual({ kind: "message", message: a1 });
    expect(group(items[1]).tools).toHaveLength(2);
  });
});
