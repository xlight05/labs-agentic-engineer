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

// Real-browser test (Chromium via Playwright) for the streaming auto-scroll in
// SpecMdEditor. This can NOT run in jsdom: the follow-the-tail behavior is
// driven by use-stick-to-bottom's ResizeObserver over real layout
// (scrollHeight/clientHeight/scrollTop), none of which jsdom provides.
//
// The agent's collab writes are emulated faithfully: the real generation path
// is `services/agents` joining the room and calling `setDocFileAsAgent(doc,
// path, markdown, …)` (room-peer.ts). We call the very same @aep/collab-doc
// helper against a shared Y.Doc whose XmlFragment backs the editor, so the
// editor grows exactly as it does in production — no server, no cluster.

import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, waitFor } from "@testing-library/react";
import * as Y from "yjs";
import { Awareness } from "y-protocols/awareness";
import type { HocuspocusProvider } from "@hocuspocus/provider";
import { OxygenTheme, OxygenUIThemeProvider } from "@wso2/oxygen-ui";
import { setDocFileAsAgent } from "@aep/collab-doc";
import { SpecMdEditor } from "./SpecMdEditor";

const PATH = "requirements/prd.md";
const AGENT_META = { agent: "Spec Agent", at: "2026-07-12T00:00:00.000Z" };

// CollaborationCaret only reads `provider.awareness` (verified against the
// installed extension), so a real Awareness on the doc is a sufficient stand-in
// for a connected HocuspocusProvider.
function fakeProvider(doc: Y.Doc): HocuspocusProvider {
  return { awareness: new Awareness(doc) } as unknown as HocuspocusProvider;
}

// A growing PRD: `n` sections. Each call to setDocFileAsAgent reconciles the
// fragment to this markdown, so larger n ⇒ taller document (the stream).
function prd(n: number): string {
  let s = "# Product Requirements\n\n";
  for (let i = 1; i <= n; i++) {
    s +=
      `## Section ${i}\n\n` +
      `Requirement ${i}: the system shall do the thing described in this ` +
      `paragraph, which is long enough to take vertical space and force the ` +
      `document to overflow its scroll container.\n\n`;
  }
  return s;
}

const raf = () =>
  new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));

// Nearest scrollable ancestor of the editable element = the editor's document
// scroll region (the Box that owns `overflow: auto` in SpecMdEditor).
function scrollParentOf(el: HTMLElement): HTMLElement {
  let node: HTMLElement | null = el;
  while (node) {
    const oy = getComputedStyle(node).overflowY;
    if (oy === "auto" || oy === "scroll") return node;
    node = node.parentElement;
  }
  throw new Error("no scrollable ancestor found");
}

const bottomGap = (s: HTMLElement) =>
  s.scrollHeight - s.clientHeight - s.scrollTop;

// Mount the editor in a fixed-height flex column so its `flexGrow:1` scroll
// region gets a real, bounded height and can actually overflow.
async function mountEditor(doc: Y.Doc, agentStreaming: boolean) {
  const fragment = doc.getXmlFragment(PATH);
  const view = render(
    <OxygenUIThemeProvider theme={OxygenTheme}>
      <div style={{ height: "320px", display: "flex", flexDirection: "column" }}>
        <SpecMdEditor
          fragment={fragment}
          provider={fakeProvider(doc)}
          self={{ name: "Tester", color: "#64b5f6" }}
          agentStreaming={agentStreaming}
        />
      </div>
    </OxygenUIThemeProvider>,
  );
  // The Tiptap editable mounts asynchronously (useEditor).
  const editable = await waitFor(() => {
    const el = view.container.querySelector<HTMLElement>(".ProseMirror");
    if (!el) throw new Error("editor not mounted yet");
    return el;
  });
  return { view, scrollEl: scrollParentOf(editable) };
}

// Emulate the agent streaming `n` sections in, one reconcile per step, giving
// the ResizeObserver/animation a frame between writes (as real token streaming
// would).
async function stream(doc: Y.Doc, sections: number) {
  for (let n = 1; n <= sections; n++) {
    setDocFileAsAgent(doc, PATH, prd(n), "test-agent", AGENT_META);
    await raf();
  }
}

afterEach(() => {
  cleanup();
});

describe("SpecMdEditor streaming auto-scroll", () => {
  it("follows the tail while an agent streams (agentStreaming=true)", async () => {
    const doc = new Y.Doc();
    const { scrollEl } = await mountEditor(doc, true);

    await stream(doc, 40);

    // The document must actually overflow, else the assertion is vacuous.
    await waitFor(
      () => expect(scrollEl.scrollHeight - scrollEl.clientHeight).toBeGreaterThan(200),
      { timeout: 4000 },
    );
    // …and the view must have followed the growth to the bottom.
    await waitFor(() => expect(bottomGap(scrollEl)).toBeLessThan(8), {
      timeout: 4000,
      interval: 50,
    });
    expect(scrollEl.scrollTop).toBeGreaterThan(100);

    doc.destroy();
  });

  it("does NOT auto-scroll when no agent is streaming (agentStreaming=false)", async () => {
    const doc = new Y.Doc();
    const { scrollEl } = await mountEditor(doc, false);

    await stream(doc, 40);

    // Same overflow, but the view stays at the top — no tail-follow off-stream.
    await waitFor(
      () => expect(scrollEl.scrollHeight - scrollEl.clientHeight).toBeGreaterThan(200),
      { timeout: 4000 },
    );
    // Give any stray animation a chance to (wrongly) fire before asserting.
    await new Promise((r) => setTimeout(r, 400));
    expect(scrollEl.scrollTop).toBeLessThan(8);

    doc.destroy();
  });

  it("stops following once the user scrolls up mid-stream", async () => {
    const doc = new Y.Doc();
    const { scrollEl } = await mountEditor(doc, true);

    // Get it following at the bottom first.
    await stream(doc, 25);
    await waitFor(() => expect(bottomGap(scrollEl)).toBeLessThan(8), {
      timeout: 4000,
      interval: 50,
    });

    // User scrolls up — a real wheel-up event breaks the lock immediately.
    scrollEl.dispatchEvent(
      new WheelEvent("wheel", { deltaY: -200, bubbles: true }),
    );
    await raf();

    // More content streams in; the view must NOT snap back to the bottom.
    await stream(doc, 55);
    await new Promise((r) => setTimeout(r, 400));
    expect(bottomGap(scrollEl)).toBeGreaterThan(50);

    doc.destroy();
  });
});
