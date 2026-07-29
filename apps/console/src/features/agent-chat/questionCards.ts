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

// The pure half of the question cards (ADR-0012 / #270): parsing the
// ask_question / ask_questions tool-call payloads off the wire into a uniform
// list of questions, and deciding which cards are still answerable. The wire
// tool names and answer serialization live in @aep/agent-stream (the contract);
// this module stays free of React/store imports so it unit-tests standalone.

import {
  ASK_QUESTION_TOOL,
  ASK_QUESTIONS_TOOL,
  buildAnswerInstruction,
  buildAnswersInstruction,
  type AskQuestionInput,
  type QuestionAnswer,
  type AskQuestionOption,
} from "@aep/agent-stream";
import type { ChatMessage } from "./chatStore";

/** Parse one question object; null if malformed or its labels aren't unique. */
function parseOneQuestion(value: unknown): AskQuestionInput | null {
  if (typeof value !== "object" || value === null) return null;
  const v = value as Record<string, unknown>;
  if (typeof v.question !== "string" || !v.question) return null;
  if (!Array.isArray(v.options) || v.options.length === 0) return null;
  const options: AskQuestionOption[] = [];
  for (const raw of v.options) {
    if (typeof raw !== "object" || raw === null) return null;
    const o = raw as Record<string, unknown>;
    if (typeof o.label !== "string" || !o.label) return null;
    options.push({
      label: o.label,
      ...(typeof o.description === "string" ? { description: o.description } : {}),
      ...(o.recommended === true ? { recommended: true } : {}),
    });
  }
  // Labels are the selection identity on the card AND in the serialized answer;
  // duplicates would make a pick ambiguous.
  if (new Set(options.map((o) => o.label)).size !== options.length) return null;
  return {
    question: v.question,
    options,
    ...(v.multiSelect === true ? { multiSelect: true } : {}),
  };
}

/**
 * Parse an `ask_question` (single) or `ask_questions` (batch) tool-call input
 * — object or the provider's stringified JSON — into a uniform, non-empty list
 * of questions. Anything malformed → null; the fold then renders no card and
 * the turn's prose still carries the question.
 */
export function parseQuestionsInput(toolName: string, input: unknown): AskQuestionInput[] | null {
  let value = input;
  if (typeof value === "string") {
    try {
      value = JSON.parse(value);
    } catch {
      return null;
    }
  }
  if (toolName === ASK_QUESTION_TOOL) {
    const one = parseOneQuestion(value);
    return one ? [one] : null;
  }
  if (toolName === ASK_QUESTIONS_TOOL) {
    if (typeof value !== "object" || value === null) return null;
    const list = (value as Record<string, unknown>).questions;
    if (!Array.isArray(list) || list.length === 0) return null;
    const out: AskQuestionInput[] = [];
    for (const q of list) {
      const parsed = parseOneQuestion(q);
      if (!parsed) return null;
      out.push(parsed);
    }
    return out;
  }
  return null;
}

/** True when `toolName` is one of the question tools (single or batch). */
export function isQuestionTool(toolName: string | undefined): boolean {
  return toolName === ASK_QUESTION_TOOL || toolName === ASK_QUESTIONS_TOOL;
}

/**
 * Serialize a card's answer(s) into the next turn's plain-text instruction —
 * one definition shared by the chat hook and the collab banner. Single question
 * → `Answer to "…"`, batch → an `Answers:` list (the wire contract's builders).
 */
export function serializeQuestionAnswer(
  questions: AskQuestionInput[],
  answers: QuestionAnswer[],
): string {
  if (questions.length === 1) {
    return buildAnswerInstruction(
      questions[0]!.question,
      answers[0]?.selected ?? [],
      answers[0]?.freeText,
    );
  }
  return buildAnswersInstruction(
    questions.map((q, i) => ({
      question: q.question,
      selected: answers[i]?.selected ?? [],
      ...(answers[i]?.freeText ? { freeText: answers[i]!.freeText } : {}),
    })),
  );
}

/**
 * The ids of question cards that still accept input: unanswered via the card
 * AND not superseded by any later user message that actually reached the server
 * (a `failed` send supersedes nothing — the agent never saw it). Single
 * backward pass, computed once per render; derived purely from the log, so
 * reloads and second tabs converge.
 */
export function answerableQuestionIds(messages: ChatMessage[]): Set<string> {
  const ids = new Set<string>();
  let superseded = false;
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i]!;
    if (m.role === "user" && m.status !== "failed") superseded = true;
    else if (m.role === "question" && !superseded && !m.answers) ids.add(m.id);
  }
  return ids;
}
