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
 * runTurn — the generic, file-agnostic turn loop. Model + tools + instructions +
 * messages in; events, appended messages, usage, finishReason out. It imports
 * `ai` only, knows nothing file-specific, and WRITES NOTHING. Every consumer
 * (the SSE route, the eval) drives it identically — one waist, one stream shape.
 *
 * Tools run SERVER-SIDE (they keep `execute()`), so the model's one-step
 * self-correction (NOT_UNIQUE / NOT_FOUND / INVALID_YAML) stays inside one
 * `agent.stream()` call. The canonical bundle is already mutated when the stream
 * finishes; consumers read `bundle.snapshot()` (or reconstruct from the stream),
 * they never re-apply.
 */

import {
  ToolLoopAgent,
  isStepCount,
  type StopCondition,
  type ModelMessage,
  type LanguageModel,
  type LanguageModelUsage,
  type ToolSet,
} from "ai";
import type { StreamPart } from "./stream-types.js";

export interface RunTurnInput {
  model: LanguageModel;
  instructions: string;
  tools: ToolSet;
  /** The growing conversation. MUTATED IN PLACE: the user turn + the response are appended. */
  messages: ModelMessage[];
  /** This turn's user instruction text (pushed as a `user` message before streaming). */
  prompt: string;
  /**
   * Stop conditions; defaults to `[isStepCount(maxSteps ?? 20)]`. Stays generic
   * — the main wiring passes `[isStepCount(n), hasToolCall('ask_question')]`
   * without runTurn ever knowing a tool name.
   */
  stopWhen?: StopCondition<ToolSet>[];
  onEvent?: (part: StreamPart) => void;
  abortSignal?: AbortSignal;
  maxSteps?: number;
}

export interface RunTurnResult {
  finishReason: string;
  usage: LanguageModelUsage;
}

export async function runTurn(input: RunTurnInput): Promise<RunTurnResult> {
  // Single source of conversation truth: push this turn, then stream({ messages })
  // ONLY (prompt + messages are mutually exclusive in v7).
  input.messages.push({ role: "user", content: input.prompt });

  const agent = new ToolLoopAgent({
    model: input.model,
    instructions: input.instructions,
    tools: input.tools,
    stopWhen: input.stopWhen ?? [isStepCount(input.maxSteps ?? 20)],
  });

  const result = await agent.stream({
    messages: input.messages,
    ...(input.abortSignal ? { abortSignal: input.abortSignal } : {}),
  });

  // v7: read result.stream (NOT fullStream). The async iterator yields the
  // server TextStreamPart; text-delta carries `.text`, tool-input-delta `.delta`.
  for await (const part of result.stream as AsyncIterable<StreamPart>) {
    input.onEvent?.(part);
  }

  // v7: appended messages come from result.responseMessages (accumulated across
  // ALL steps), not (await result.response).messages (last-step-only). The caller
  // reads the appended turn from its own `messages` array (mutated in place above).
  const appended = await result.responseMessages;
  input.messages.push(...appended);

  return {
    finishReason: await result.finishReason,
    usage: await result.usage, // v7: already the whole-turn sum
  };
}
