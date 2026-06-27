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
 * Agent SSE event contract — the typed, documented surface over the main spec
 * agent's turn stream.
 *
 * The wire stays RAW `StreamPart` (the SDK's `TextStreamPart`, one frame per
 * part); this module does NOT add an envelope. It exists so the producer (the
 * Express SSE route in `@aep/agents`), the eval, and a future browser client
 * share ONE definition of: the emitted event catalog, the payloads carried
 * inside the frames (`OpResult`, the per-tool `*Input` shapes), the reviewable
 * `Change` projection, and the turn-request body (`TurnRequest`).
 *
 * Ownership: `@aep/contracts` is the leaf source of truth. The domain
 * (`@aep/agents` `bundle.ts` / `tool.ts`) imports these types and the Zod
 * schemas carry a compile-time drift guard asserting they stay assignable to
 * the `*Input` types here — so there is no hand-maintained parallel copy.
 */

// --- Result payloads (the `tool-result.output` value) -----------------------

/** The four file-mutation operations the main agent performs. */
export type Op = "add" | "edit" | "remove" | "frontmatter";

/** Error codes the `FileBundle` returns to steer one-step self-correction. */
export type ErrCode =
  | "ALREADY_EXISTS"
  | "INVALID_PATH"
  | "NOT_FOUND"
  | "NOT_UNIQUE"
  | "NO_SUCH_FILE"
  | "EMPTY_OLD_STRING"
  | "INVALID_YAML"
  | "NO_FRONTMATTER"
  | "PROTECTED_PATH";

/** A candidate line echoed back for NOT_UNIQUE / NOT_FOUND re-anchoring. */
export interface MatchCandidate {
  line: number;
  text: string;
}

/**
 * A successful op. Carries NO file content — a successful result is just the
 * status; the model anchors later edits from its own tool-call args plus the
 * re-inlined CURRENT STATE, and consumers reconstruct file state by folding the
 * stream (see `Change` / `applyToolCall`).
 */
export interface OpOk {
  ok: true;
  path: string;
  op: Op;
  /** `applied` = state changed; `already-applied`/`noop` = idempotent no-change. */
  status: "applied" | "already-applied" | "noop";
}

/** A failed op. Keeps the self-correction payload (candidates / count). */
export interface OpErr {
  ok: false;
  path: string;
  op: Op;
  code: ErrCode;
  message: string;
  /** Populated for NOT_UNIQUE / NOT_FOUND to steer one-step re-anchoring. */
  candidates?: MatchCandidate[];
  count?: number;
}

export type OpResult = OpOk | OpErr;

// --- Per-tool input shapes (the `tool-call.input` value) --------------------
//
// These are the WIRE source of truth. The Zod `inputSchema`s in
// `@aep/agents` `tool.ts` carry a compile-time assert that `z.infer<schema>`
// stays equal to these — divergence fails that package's typecheck.

export interface AddFileInput {
  path: string;
  content: string;
}

export interface EditFileInput {
  path: string;
  oldString: string;
  newString: string;
}

export interface RemoveFileInput {
  path: string;
}

export interface SetFrontmatterFieldInput {
  path: string;
  key: string;
  value: string | number | boolean | string[];
}

// --- Skills (progressive disclosure, ADR-0002) ------------------------------
//
// Skills are GUIDANCE, not code. The caller RESOLVES them (the eval reads
// repo-root `skills/`; in production a BFF reads the org repo) and pushes the
// full candidate set in the turn request. The service never reads skills from
// disk: it shows only a name+description catalog at the end of the system prompt
// and serves a body on demand via the `loadSkill` tool. The body enters context
// only when loaded, and then persists as a tool result in message history.

/** One candidate skill pushed in the turn request — a resolved `SKILL.md`. */
export interface Skill {
  /** Stable id; what the catalog lists and `loadSkill` takes (the dir name). */
  name: string;
  /** One-line summary shown in the catalog so the agent can decide to load it. */
  description: string;
  /** The full guidance body (frontmatter stripped) returned by `loadSkill`. */
  content: string;
}

/** The `loadSkill` tool input. WIRE source of truth; drift-guarded in `tool.ts`. */
export interface LoadSkillInput {
  name: string;
}

/**
 * The `loadSkill` tool result. Success carries the body; a miss carries the
 * available names so the model self-corrects in one round-trip (cf. NOT_FOUND).
 */
export type LoadSkillResult =
  | { ok: true; name: string; content: string }
  | { ok: false; name: string; error: string; available: string[] };

// --- The reviewable change (§7) ---------------------------------------------

/**
 * A reviewable projection of one `tool-result` part: the op intent plus its
 * result. A browser folds these into a live diff; the eval reconstructs files
 * via `applyToolCall` instead. Pure field projection — see `toChange` in
 * `@aep/agents` `change.ts`.
 */
export interface Change {
  toolCallId: string;
  toolName: string;
  op: Op;
  path: string;
  /** editFile payload. */
  oldString?: string;
  newString?: string;
  /** addFile payload. */
  content?: string;
  /** setFrontmatterField payload. */
  key?: string;
  value?: string | number | boolean | string[];
  result: OpResult;
}

// --- The turn request (the `POST /conversations/:id/turns` body) -------------

/**
 * The turn-request body. The caller passes the full accepted `files` snapshot
 * every turn (the service reads no repo/disk); `filesChangedExternally` flags an
 * out-of-band edit so the server prepends a CURRENT-STATE-authoritative note.
 * The producer (server) validates an untrusted body against this shape; the eval
 * client (and a future browser) construct it — one definition, no drift.
 */
export interface TurnRequest {
  instruction: string;
  files: Record<string, string>;
  filesChangedExternally?: boolean;
  /**
   * The candidate skills for this turn (ADR-0002). Only `name`+`description` are
   * shown (the catalog); the agent pulls a body via `loadSkill`. Omitted/empty →
   * no catalog and no `loadSkill` tool (byte-identical to a skill-free turn).
   */
  skills?: Skill[];
}

// --- The emitted event catalog ----------------------------------------------

/**
 * The documented subset of `StreamPart` `type`s the SSE route emits, one SSE
 * frame each. The wire carries the raw part verbatim; this catalog names what a
 * consumer can expect to see.
 */
export const AGENT_SSE_EVENT_TYPES = [
  "text-delta",
  "tool-input-start",
  "tool-input-delta",
  "tool-call",
  "tool-result",
  "tool-error",
  "error",
  "finish",
] as const;

export type AgentSseEventType = (typeof AGENT_SSE_EVENT_TYPES)[number];

/** The terminal sentinel sent after the last frame: `data: [DONE]`. */
export const SSE_DONE = "[DONE]" as const;

// --- The envelope type ------------------------------------------------------

/**
 * The raw stream-part envelope as produced by the AI SDK server stream
 * (`result.stream`). Re-exported for consumers that want the full SDK type;
 * `@aep/agents` keeps its own minimal structural `StreamPart` for the loop seam.
 */
export type { TextStreamPart } from "ai";
