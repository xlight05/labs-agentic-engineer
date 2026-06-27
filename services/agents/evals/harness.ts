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
 * The eval harness — a headless SSE INTEGRATION client (§11). Per fixture, per
 * turn, ×K it: boots the real Express app on an ephemeral port with a fresh
 * `InMemoryConversationStore`, POSTs the turn, consumes the SSE stream, and
 * RECONSTRUCTS the final files by folding the streamed tool-calls through a fresh
 * `FileBundle` (`applyToolCall`, §7) — reusing the canonical ops, so it
 * reproduces the server's state with no second matcher. Then it scores the
 * reconstruction + the harvested `OpResult`s. Report-not-gate.
 */

import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import type { LanguageModel, ModelMessage } from "ai";
import type { OpResult, Skill } from "@aep/contracts";
import { createApp } from "../src/server.js";
import { listen0 } from "../src/shared/listen.js";
import { InMemoryConversationStore } from "../src/store/memory-store.js";
import { FileBundle } from "../src/agents/main/bundle.js";
import { applyToolCall, isFileMutationTool } from "../src/agents/main/change.js";
import type { StreamPart } from "../src/agents/main/stream-types.js";
import { streamTurn, type TurnRequest } from "./sse-client.js";
import { scoreExpect, allPass, type CheckResult } from "./scoring.js";
import { writePreview } from "./preview.js";
import type { Fixture } from "./fixture.js";

export interface EvalSuite {
  agent: string;
  fixturesDir: string;
  defaultSeed: Record<string, string>;
  /** Repo-root `skills/` dir whose whole library is pushed on every turn (ADR-0002). */
  skillsDir?: string;
}

export interface TurnResult {
  turn: number;
  prompt: string;
  pass: boolean;
  checks: CheckResult[];
}
export interface SampleResult {
  pass: boolean;
  turns: TurnResult[];
}
export interface FixtureResult {
  name: string;
  description?: string;
  difficulty?: string;
  samples: number;
  passed: number;
  passRate: number;
  sampleResults: SampleResult[];
}
export interface SuiteResult {
  agent: string;
  fixtures: FixtureResult[];
}

export interface RunOptions {
  model: LanguageModel;
  samples: number;
  /** When set, dump the reconstructed snapshot per turn (k === 0 only). */
  writePreviewDir?: string;
  /** The whole skill library, pushed in every turn body (ADR-0002). */
  skills?: Skill[];
  onLog?: (msg: string) => void;
}

interface Booted {
  store: InMemoryConversationStore;
  baseUrl: string;
  close: () => Promise<void>;
}

async function boot(model: LanguageModel): Promise<Booted> {
  const store = new InMemoryConversationStore();
  const app = createApp({ store, model });
  const { baseUrl, close } = await listen0(app.listen(0));
  return { store, baseUrl, close };
}

/** Tool-call parts embedded in stored assistant messages (for trace reconstruction). */
function toolCallsFromMessages(messages: ModelMessage[]): StreamPart[] {
  const out: StreamPart[] = [];
  for (const m of messages) {
    if (m.role !== "assistant" || !Array.isArray(m.content)) continue;
    for (const p of m.content) {
      if (p.type === "tool-call") {
        out.push({ type: "tool-call", toolName: p.toolName, toolCallId: p.toolCallId, input: p.input });
      }
    }
  }
  return out;
}

/** Fold tool-calls onto a snapshot via the canonical ops → reconstructed files. */
function reconstruct(seed: Record<string, string>, toolCalls: StreamPart[]): Record<string, string> {
  const bundle = new FileBundle(seed);
  for (const tc of toolCalls) applyToolCall(bundle, tc);
  return bundle.snapshot();
}

function turnBody(
  prompt: string,
  files: Record<string, string>,
  changed: boolean,
  skills?: Skill[],
): TurnRequest {
  return {
    instruction: prompt,
    files,
    ...(changed ? { filesChangedExternally: true } : {}),
    ...(skills && skills.length > 0 ? { skills } : {}),
  };
}

/** Drive one turn over HTTP and collect every raw StreamPart frame. */
async function collectTurn(baseUrl: string, id: string, body: TurnRequest): Promise<StreamPart[]> {
  const parts: StreamPart[] = [];
  for await (const part of streamTurn(baseUrl, id, body)) parts.push(part);
  return parts;
}

/** Run all turns once, scoring each. `k` selects the sample (preview on k===0). */
async function runSample(
  suite: EvalSuite,
  fixture: Fixture,
  opts: RunOptions,
  k: number,
): Promise<SampleResult> {
  const seed = fixture.seed ?? suite.defaultSeed;
  const { store, baseUrl, close } = await boot(opts.model);
  try {
    const id = fixture.name;
    let current = seed;

    if (fixture.messages && fixture.messages.length > 0) {
      // Captured-trace continuation: pre-seed the store + reconstruct the pre-turn files.
      const now = new Date();
      await store.save({ id, messages: fixture.messages, status: "done", createdAt: now, updatedAt: now });
      current = reconstruct(seed, toolCallsFromMessages(fixture.messages));
    }

    const turns: TurnResult[] = [];
    for (let i = 0; i < fixture.turns.length; i++) {
      const turn = fixture.turns[i]!;
      const files = turn.files ?? current;

      const body = turnBody(turn.prompt, files, turn.filesChangedExternally === true, opts.skills);
      const parts = await collectTurn(baseUrl, id, body);

      const toolCalls = parts.filter((p) => p.type === "tool-call");
      const toolNames = toolCalls.map((p) => p.toolName ?? "");
      // Only file-mutation tool-results are OpResults; loadSkill (ADR-0002) is not.
      const results = parts
        .filter((p) => p.type === "tool-result" && isFileMutationTool(p.toolName ?? ""))
        .map((p) => p.output as OpResult);
      const finalState = reconstruct(files, toolCalls);

      if (opts.writePreviewDir && k === 0) {
        writePreview(join(opts.writePreviewDir, fixture.name, `turn-${i + 1}`), finalState);
      }

      const checks = scoreExpect(turn.expect ?? fixture.expect, finalState, files, results, toolNames);

      // A tool-error (execute threw) or mid-stream error frame yields no
      // tool-result, so it is invisible to scoreExpect — fail the turn
      // explicitly so a stream error can't report a false green.
      const errorFrames = parts.filter((p) => p.type === "tool-error" || p.type === "error");
      if (errorFrames.length > 0) {
        checks.push({ clause: "noStreamErrors", pass: false, detail: `${errorFrames.length} error/tool-error frame(s)` });
      }

      turns.push({ turn: i + 1, prompt: turn.prompt, pass: allPass(checks), checks });
      current = finalState; // auto-accept: next turn reasons against this state
    }

    return { pass: turns.every((t) => t.pass), turns };
  } finally {
    await close();
  }
}

export async function runFixture(suite: EvalSuite, fixture: Fixture, opts: RunOptions): Promise<FixtureResult> {
  const sampleResults: SampleResult[] = [];
  for (let k = 0; k < opts.samples; k++) {
    const result = await runSample(suite, fixture, opts, k);
    sampleResults.push(result);
    opts.onLog?.(`  ${fixture.name} [sample ${k + 1}/${opts.samples}] ${result.pass ? "PASS" : "FAIL"}`);
  }
  const passed = sampleResults.filter((s) => s.pass).length;
  const result: FixtureResult = {
    name: fixture.name,
    samples: opts.samples,
    passed,
    passRate: opts.samples === 0 ? 0 : passed / opts.samples,
    sampleResults,
  };
  if (fixture.description !== undefined) result.description = fixture.description;
  if (fixture.difficulty !== undefined) result.difficulty = fixture.difficulty;
  return result;
}

export async function runSuite(suite: EvalSuite, fixtures: Fixture[], opts: RunOptions): Promise<SuiteResult> {
  const results: FixtureResult[] = [];
  for (const fixture of fixtures) {
    opts.onLog?.(`▶ ${fixture.name}`);
    results.push(await runFixture(suite, fixture, opts));
  }
  return { agent: suite.agent, fixtures: results };
}

/**
 * Record a fixture's turns over the real route and return the resulting
 * `messages` — for authoring thread-2 captured-trace fixtures (`EVAL_RECORD`).
 * Does not score.
 */
export async function recordFixture(
  suite: EvalSuite,
  fixture: Fixture,
  model: LanguageModel,
  skills?: Skill[],
): Promise<ModelMessage[]> {
  const seed = fixture.seed ?? suite.defaultSeed;
  const { store, baseUrl, close } = await boot(model);
  try {
    const id = fixture.name;
    let current = seed;
    for (const turn of fixture.turns) {
      const files = turn.files ?? current;
      const body = turnBody(turn.prompt, files, turn.filesChangedExternally === true, skills);
      const parts = await collectTurn(baseUrl, id, body);
      current = reconstruct(files, parts.filter((p) => p.type === "tool-call"));
    }
    const conv = await store.get(id);
    return conv?.messages ?? [];
  } finally {
    await close();
  }
}

/** Write the stable, overwritten result file (committed for diffs). */
export function writeResults(path: string, result: SuiteResult): void {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, `${JSON.stringify(result, null, 2)}\n`, "utf8");
}
