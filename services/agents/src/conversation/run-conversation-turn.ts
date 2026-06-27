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
 * The single per-turn orchestration the SSE route calls: load-or-lazy-create →
 * mark active → fresh throwaway WorkingBundle from the passed snapshot →
 * `runTurn` (prepending a one-line CURRENT-STATE-authoritative note ONLY when
 * the FE flagged an external edit) → set status → persist the whole aggregate.
 *
 * Files NEVER touch the store; the passed `files` snapshot is both the inlined
 * CURRENT STATE and (when diverged) the basis of the note. History is
 * append-only — no turn rewrites a prior message, so the prompt-cache prefix is
 * preserved across turns.
 */

import { isStepCount, type LanguageModel, type ModelMessage } from "ai";
import type { Skill } from "@aep/contracts";
import { runTurn } from "../agents/main/run-turn.js";
import { buildTools, ASK_QUESTION } from "../agents/main/tool.js";
import { FileBundle } from "../agents/main/bundle.js";
import { buildInstructions, buildPrompt } from "../agents/main/prompt.js";
import type { StreamPart } from "../agents/main/stream-types.js";
import { config } from "../shared/config.js";
import type { Conversation, ConversationStore } from "../store/conversation-store.js";

/** Thrown when a second turn starts for an id whose turn is still in flight (→ HTTP 409). */
export class ConcurrentTurnError extends Error {
  readonly code = "CONCURRENT_TURN";
  constructor(public readonly conversationId: string) {
    super(`a turn is already in progress for conversation ${conversationId}`);
    this.name = "ConcurrentTurnError";
  }
}

/**
 * Per-id in-flight guard — serializes turns for one conversation. This (not a
 * status read) is what makes 409 real and testable: status is only persisted at
 * turn END and `get()` returns a clone, so a mid-turn second POST would never
 * observe `status === 'active'`. One guard per app (composition root).
 */
export class TurnGuard {
  private readonly inflight = new Set<string>();
  acquire(id: string): void {
    if (this.inflight.has(id)) throw new ConcurrentTurnError(id);
    this.inflight.add(id);
  }
  release(id: string): void {
    this.inflight.delete(id);
  }
}

const DIVERGENCE_NOTE =
  "NOTE: files were changed outside your last proposals — the CURRENT STATE " +
  "below is authoritative; ignore any earlier value you proposed.\n\n";

function freshConversation(id: string): Conversation {
  const now = new Date(); // store re-stamps on save; this is the lazy-create placeholder
  return { id, messages: [], status: "active", createdAt: now, updatedAt: now };
}

/**
 * True when the turn ended on an `ask_question` tool-call (HITL). Scans only the
 * messages appended THIS turn. Dormant while `ask_question` is disabled (§5).
 */
function endedAwaitingHuman(appended: ModelMessage[]): boolean {
  for (const m of appended) {
    if (m.role !== "assistant" || !Array.isArray(m.content)) continue;
    for (const part of m.content) {
      if (part.type === "tool-call" && part.toolName === ASK_QUESTION) return true;
    }
  }
  return false;
}

export interface RunConversationTurnInput {
  id: string;
  instruction: string;
  files: Record<string, string>;
  /** Optional FE flag (default false): prepend the CURRENT-STATE-authoritative note (§10). */
  filesChangedExternally?: boolean;
  /**
   * Candidate skills for this turn (ADR-0002), resolved by the caller. Their
   * name+description form the catalog at the end of the system prompt; the agent
   * pulls a body via `loadSkill`. Omitted/empty → no catalog, no `loadSkill`.
   */
  skills?: Skill[];
  /** Injected at the composition root (createModel is called ONCE there, not per turn). */
  model: LanguageModel;
  store: ConversationStore;
  guard: TurnGuard;
  onEvent: (p: StreamPart) => void;
  abortSignal?: AbortSignal;
}

export async function runConversationTurn(input: RunConversationTurnInput): Promise<Conversation> {
  input.guard.acquire(input.id); // throws ConcurrentTurnError on a concurrent turn → 409
  try {
    // 1. load or lazily create
    const conv = (await input.store.get(input.id)) ?? freshConversation(input.id);

    // 2. mark active (in memory; not saved mid-turn — the guard handles concurrency)
    conv.status = "active";

    // 3. throwaway WorkingBundle from the passed snapshot; ask_question DISABLED.
    //    `loadSkill` is registered only when the caller pushed skills (ADR-0002).
    const bundle = new FileBundle(input.files);
    const tools = buildTools(bundle, input.skills);

    // 4. one generic turn. buildInstructions appends the skill catalog at the END
    //    of the system prompt; buildPrompt inlines CURRENT STATE; prepend a one-line
    //    divergence note ONLY when the FE flagged an external edit (append-only).
    const note = input.filesChangedExternally ? DIVERGENCE_NOTE : "";
    const startLen = conv.messages.length;
    const res = await runTurn({
      model: input.model,
      instructions: buildInstructions(input.skills),
      prompt: note + buildPrompt(input.files, input.instruction),
      messages: conv.messages, // appended in place by runTurn
      tools,
      stopWhen: [isStepCount(config.maxSteps) /*, hasToolCall("ask_question") */],
      onEvent: input.onEvent,
      ...(input.abortSignal ? { abortSignal: input.abortSignal } : {}),
    });

    // 5. set status (awaiting-human dormant while ask_question disabled)
    conv.status = endedAwaitingHuman(conv.messages.slice(startLen)) ? "awaiting-human" : "done";

    // 6. observe per-turn spend (runTurn returns usage; today nothing keeps it)
    if (config.logLevel === "debug") {
      process.stderr.write(
        `[turn ${conv.id}] finishReason=${res.finishReason} tokens in/out=` +
          `${res.usage.inputTokens ?? "?"}/${res.usage.outputTokens ?? "?"}\n`,
      );
    }

    // 7. persist the whole aggregate (history is append-only)
    await input.store.save(conv);
    return conv;
  } finally {
    input.guard.release(input.id);
  }
}
