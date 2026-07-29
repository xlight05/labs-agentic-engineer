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

// Agent-chat turn endpoints (#130): start (202), rehydrate (empty), active
// (204), and a scripted SSE stream — narration, one tool result, terminal —
// so the panel is fully drivable in mock mode. Error scenario: instruction
// containing "fail" streams a turn-failed terminal.
//
// Multi-user scenarios (task 2, `aep:mock:chat` — see fixtures/chat.ts):
//   "multiuser"      — settled history, two authors, no running turn.
//   "teammate-turn"  — same history, plus a running turn a teammate started
//                      (its triggering message is in the history; the reply
//                      streams from the same scripted SSE builder below).
//   unset            — unchanged: empty rehydrate, no active turn.

import { http, HttpResponse } from "msw";
import { ANSWER_PREFIX, ANSWERS_PREFIX } from "@aep/agent-stream";
import { GRILLING_DIRECTIVE } from "@aep/contracts/prompts";
import {
  activeTeammateTurn,
  multiuserHistory,
  teammateTurnHistory,
  type ChatScenario,
} from "../fixtures/chat";

let turnCounter = 0;
// Distinguishes this page instance's turns from another client's in the same
// collab room: real turn ids are server-minted uuids, but this counter starts
// at 1 in every tab — colliding ids would merge distinct questions in the
// shared Yjs map (and a closed one would suppress a fresh ask).
const instanceId = Math.random().toString(36).slice(2, 8);
const turnInstruction = new Map<string, string>();

function chatScenario(): ChatScenario | null {
  return localStorage.getItem("aep:mock:chat") as ChatScenario | null;
}

function sse(frames: unknown[]): Response {
  const encoder = new TextEncoder();
  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      let id = 0;
      for (const frame of frames) {
        controller.enqueue(
          encoder.encode(`id: ${id}\ndata: ${JSON.stringify(frame)}\n\n`),
        );
        id += 1;
        await new Promise((r) => setTimeout(r, 250)); // visible streaming
      }
      controller.enqueue(encoder.encode("data: [DONE]\n\n"));
      controller.close();
    },
  });
  return new HttpResponse(stream, {
    headers: { "Content-Type": "text/event-stream" },
  });
}

export const agentChatHandlers = [
  http.post("*/api/v1/projects/:projectName/agents/:conversationId/messages", async ({ request }) => {
    const body = (await request.json()) as { instruction?: string };
    turnCounter += 1;
    const turnId = `mock-turn-${instanceId}-${turnCounter}`;
    turnInstruction.set(turnId, body.instruction ?? "");
    return HttpResponse.json({ turnId }, { status: 202 });
  }),

  http.get("*/api/v1/projects/:projectName/agents/:conversationId/messages", () => {
    const scenario = chatScenario();
    if (scenario === "multiuser") {
      return HttpResponse.json({ status: "done", messages: multiuserHistory });
    }
    if (scenario === "teammate-turn") {
      return HttpResponse.json({ status: "done", messages: teammateTurnHistory });
    }
    return HttpResponse.json({ status: "done", messages: [] });
  }),

  http.get("*/api/v1/projects/:projectName/turns/active", () => {
    if (chatScenario() === "teammate-turn") {
      return HttpResponse.json(activeTeammateTurn());
    }
    return new HttpResponse(null, { status: 204 });
  }),

  http.get("*/api/v1/projects/:projectName/turns/:turnId/stream", ({ params }) => {
    const turnId = String(params.turnId);
    const instruction = turnInstruction.get(turnId) ?? "";
    const failing = instruction.includes("fail");
    if (failing) {
      return sse([
        { type: "text-delta", delta: "Let me try that…" },
        { type: "turn-failed", message: "Mock turn failure (instruction contained 'fail')." },
      ]);
    }
    // Grilling scenarios (ADR-0012 / #270) — keyed on the shared directive or a
    // typed trigger, never on a mere mention of "grill" in an edit instruction.
    // An answer turn (instruction begins with the shared answer prefix) falls
    // through to the normal generation stream below.
    const isAnswer =
      instruction.startsWith(ANSWER_PREFIX) || instruction.startsWith(ANSWERS_PREFIX);
    if (!isAnswer) {
      const grillSingle =
        instruction.includes(GRILLING_DIRECTIVE) || /\bgrill me\b/i.test(instruction);
      const grillBatch = /\ball at once\b|\bask me everything\b/i.test(instruction);
      if (grillBatch) {
        // A full interview — long enough to exercise the form's scrolling.
        const input = {
          questions: [
            {
              question: "Who is the primary user of this app?",
              options: [
                { label: "Individual consumers", description: "Self-serve signup", recommended: true },
                { label: "Internal teams", description: "SSO, org-managed access" },
                { label: "Both from day one", description: "Two onboarding paths — more scope" },
              ],
            },
            {
              question: "Which platform matters most first?",
              options: [
                { label: "Web", recommended: true },
                { label: "Mobile" },
                { label: "Both" },
              ],
            },
            {
              question: "Which capabilities are in scope for v1?",
              multiSelect: true,
              options: [
                { label: "Accounts" },
                { label: "Payments" },
                { label: "Notifications" },
                { label: "Search" },
              ],
            },
            {
              question: "How should users sign in?",
              options: [
                { label: "Email + password" },
                { label: "Social login", recommended: true },
                { label: "SSO only" },
              ],
            },
            {
              question: "What is the pricing model?",
              options: [
                { label: "Free", recommended: true },
                { label: "Subscription" },
                { label: "One-off purchase" },
              ],
            },
            {
              question: "Where does the data live?",
              options: [
                { label: "Managed Postgres", recommended: true },
                { label: "SQLite per tenant" },
                { label: "Third-party BaaS" },
              ],
            },
            {
              question: "Which integrations matter?",
              multiSelect: true,
              options: [
                { label: "Slack" },
                { label: "Email" },
                { label: "Calendar" },
                { label: "None yet", recommended: true },
              ],
            },
            {
              question: "What is the launch timeline?",
              options: [
                { label: "2 weeks" },
                { label: "1 month", recommended: true },
                { label: "A quarter" },
              ],
            },
            {
              question: "How important is offline support?",
              options: [
                { label: "Not needed", recommended: true },
                { label: "Nice to have" },
                { label: "Critical" },
              ],
            },
            {
              question: "Who administers the workspace?",
              options: [
                { label: "A single owner", recommended: true },
                { label: "Multiple admins" },
                { label: "No admin concept" },
              ],
            },
          ],
        };
        return sse([
          { type: "text-delta", delta: "Let me pin the idea down — a few questions:" },
          { type: "tool-call", toolCallId: `qs-${turnId}`, toolName: "ask_questions", input },
          {
            type: "tool-result",
            toolName: "ask_questions",
            toolCallId: `qs-${turnId}`,
            input,
            output: { status: "awaiting_user_response" },
          },
          { type: "turn-committed", noChanges: true },
        ]);
      }
      if (grillSingle) {
        const input = {
          question: "Who is the primary user of this app?",
          options: [
            { label: "Individual consumers", description: "Self-serve signup, personal workspaces", recommended: true },
            { label: "Internal teams", description: "SSO, org-managed access" },
            { label: "Both from day one", description: "Two onboarding paths — more scope" },
          ],
          multiSelect: false,
        };
        return sse([
          { type: "text-delta", delta: "Before I write anything, let me pin the idea down." },
          { type: "tool-call", toolCallId: `q-${turnId}`, toolName: "ask_question", input },
          {
            type: "tool-result",
            toolName: "ask_question",
            toolCallId: `q-${turnId}`,
            input,
            output: { status: "awaiting_user_response", question: input.question },
          },
          { type: "turn-committed", noChanges: true },
        ]);
      }
    }
    return sse([
      { type: "text-delta", delta: "Joining the spec workspace… " },
      { type: "text-delta", delta: "I'll create the requirements now." },
      // Streamed tool input: the panel shows "Creating requirements.md" as soon
      // as the path resolves, then flips to "Created" on the tool-result.
      { type: "tool-input-start", id: "tc-1", toolName: "addFile" },
      {
        type: "tool-input-delta",
        id: "tc-1",
        delta: '{"path":"specs/requirements/requirements.md","content":"# Requirements',
      },
      { type: "tool-input-delta", id: "tc-1", delta: '\\n\\n## Overview\\nA simple todo app.' },
      { type: "tool-input-end", id: "tc-1" },
      {
        type: "tool-result",
        toolName: "addFile",
        toolCallId: "tc-1",
        input: { path: "specs/requirements/requirements.md" },
        output: { ok: true, op: "add", path: "specs/requirements/requirements.md", status: "applied" },
      },
      { type: "text-delta", delta: "\n\nDone — the change is live in the shared doc." },
      { type: "turn-committed", noChanges: true },
    ]);
  }),

  http.get("*/api/v1/projects/:projectName/turns/:turnId", ({ params }) =>
    HttpResponse.json({
      turnId: String(params.turnId),
      conversationId: "mock-conv",
      useCase: "general",
      status: "completed",
      noChanges: true,
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    }),
  ),
];
