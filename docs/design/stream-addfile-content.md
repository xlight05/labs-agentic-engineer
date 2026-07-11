# Plan — stream `addFile` content live into the collab doc

**Status:** PLAN / in-progress · **Branch:** `feat/stream-addfile-content` (worktree
`/Users/wso2/repos/aep-stream-addfile`, off `agent-improvements` @ `af7de8b1`).

### Progress (2026-07-11)

- **Phase 0 — DONE, all green.** U1/U2/U3 de-risked (see below). Spikes in
  `services/agents/spike/` (throwaway; delete before merge).
- **Phase 1 — DONE, green.** `src/collab/streaming-add.ts` (`StreamingDocWriter`) +
  `streaming-add.test.ts` (7 tests: incremental line-flushed writes, path-before
  -content, skip non-md/existing, rollback on truncation + reject, concurrent calls).
- **Phase 2 — DONE, green.** Flag `AGENT_STREAM_DOC_WRITES` (`shared/config.ts` +
  `.env.example`). Wired into `run-conversation-turn.ts` (hoisted writer, wrapped
  `onEvent`, drain+rollback in `finally`). Default off ⇒ byte-identical to today.
  `pnpm --filter @aep/agents typecheck && lint && test` → clean, 140/140 pass.
- **Phase 3 — NEXT (not started).** e2e on a fresh cluster (`AGENT_STREAM_DOC_WRITES=true`),
  verify the body grows mid-stream via Playwright. Needs the cluster bring-up.

## Goal

When the agent creates a file with `addFile`, make the **file body type itself
live** in the spec editor as the model generates it — the same streaming UX the
chat text already has — instead of the whole body appearing in one write when the
tool call finishes. Target the **markdown `addFile`** case first (the big
`requirements.md` / `design.md` bodies), room-scoped (collab) turns only.

---

## What `addFile` does today (traced)

`addFile` has a **dual path**: it writes to the collab Y.Doc **and** its stream
parts go out over SSE. Both happen, but the collab write is a single shot at the
*end* of the tool call.

### Server side (`services/agents`), room mode

1. Model decides to call `addFile`. AI SDK (`ai@7.0.2`) emits on `result.stream`
   (`run-turn.ts:104`), in order:
   - `tool-input-start { id, toolName:"addFile" }`
   - `tool-input-delta { id, delta }` × N — the JSON args text, streamed
   - `tool-input-end   { id }`
   - `tool-call        { toolCallId, toolName, input:{path,content} }`
2. AI SDK runs the tool's `execute({path,content})`
   (`tools/files.ts:122`) → in room mode the bundle is a **`DocFileBundle`**
   (`collab/doc-bundle.ts`), so:
   `super.addFile` (`FileBundle`, `bundle.ts:107` — validate + mutate in-memory)
   → if `applied`, `mirror()` → `peer.set(path, content)`
   → `setDocFileAsAgent(doc, path, content, AGENT_ORIGIN, {agent,at})`
   (`@aep/collab-doc`) → **writes into the Y.Doc** (diff-and-patch + `agentInsertion`
   mark + awareness caret). Returns an `OpResult` that carries **no content**.
3. AI SDK emits `tool-result { toolCallId, toolName, output:OpResult, input:{path,content} }`.
4. **Every** one of those parts flows `onEvent` → `send` (`server.ts:260`,
   `res.write("data: " + JSON.stringify(part))`) → SSE → BFF broker
   (`turn_runner.go:273` `s.broker.Append`) → the FE turn stream.

### Frontend

- `parseSseStream` → parts; `runTurn.ts` folds `text-delta`→assistant text and
  `tool-result` (file tools)→`toChange` (`change.ts:57`)→a tool card (op+path).
- **The file *content* reaches the browser via the collab doc** (step 2's
  `peer.set`), NOT via the chat tool card. The `tool-input-delta` parts already
  arrive at the FE but are **unused**.

### The one-line summary of the change

Today the collab write is a single `peer.set(path, fullContent)` inside
`execute`, after `tool-input-end`. We want to **consume the `tool-input-delta`
parts (already streaming, already unused) and write growing content to the doc
before `execute`** — with `execute`/`DocFileBundle` staying the authority for
validation and the D14 manifest.

---

## AI SDK feasibility — verified against installed `ai@7.0.2`

- **Streamed tool input is emitted by default** — no flag. `result.stream` yields
  the server `TextStreamPart` union:
  `TextStreamToolInputStartPart { id, toolName }`,
  `TextStreamToolInputDeltaPart { id, delta }`,
  `TextStreamToolInputEndPart { id }`. (`dist/index.d.ts` ~2981–3001.) Correlate by
  `id`; it equals the later `toolCallId`.
- **`parsePartialJson`** is exported from `ai`
  (`parsePartialJson(text) → Promise<{ value, state }>`, state ∈
  `successful|repaired|failed|undefined-input`). This is the *correct* way to
  pull the growing field values out of the partial args buffer — it repairs the
  incomplete JSON and returns the **decoded** object, so `value.content` is the
  unescaped partial body (a file newline is `\n` in the delta stream; you must NOT
  concat raw deltas).
- **The schema is already ordered for this.** `addFileInputSchema = z.object({
  path, content })` and `files.ts:24-28` states property order is load-bearing:
  `path` first so a consumer can act on it immediately, the large `content` last so
  it streams delta-by-delta.

### The validation split maps onto `FileBundle.addFile`

| Gate | Depends on | When |
|---|---|---|
| `INVALID_PATH`, `ALREADY_EXISTS` (`bundle.has`) | **path only** | **early** (once `value.path` resolves) |
| `INVALID_YAML` reparse, `design.json` schema (`checkComponentDesign`) | **full content** | **end only** (can't parse half a file) |

⇒ **markdown `addFile` has no content gate** → safe to stream with zero deferred
-validation risk. YAML/`design.json` streaming is a later phase (carries a
rollback-on-reject cost).

---

## Uncertainties to de-risk FIRST (Phase 0, no wiring, minimal deps)

The plan is gated on these — if any fails, the design changes.

- **U1 — Does Anthropic actually stream tool input granularly?** Or does the AI
  SDK/provider buffer it and emit one big `tool-input-delta`? If it arrives in ~1
  chunk, there is nothing to stream. *(Needs a live model call → ANTHROPIC key.)*
- **U2 — Does `parsePartialJson` cleanly extract a growing `content`?** Does
  `path` resolve before `content` starts (schema order holds on the wire)? Does the
  decoded content grow monotonically and unescape correctly? *(Deterministic — can
  fake the delta chunking, no key.)*
- **U3 — Does feeding growing content into a real Y.Doc via `setDocFileAsAgent`
  produce clean incremental inserts?** Especially for **markdown**
  (`Y.XmlFragment` via ProseMirror node-diff): does partial markdown reflow/churn,
  or append cleanly? Compare against plain `Y.Text`. *(Deterministic, no key.)*

Phase 0 deliverables live in `services/agents/spike/` (throwaway; deleted before merge):
- `stream-tool-input.ts` — live-model spike for **U1** (logs delta count/sizes +
  runs `parsePartialJson` per delta, prints content growth). Run with a key.
- `partial-json.test.ts` — **U2** deterministic test.
- `doc-incremental.test.ts` — **U3** deterministic test (markdown + plain text,
  snapshots after each step, reports fragment churn).

**Exit criteria:** U1 shows ≥ several deltas for a multi-KB body; U2 shows path
-before-content + monotonic decoded growth; U3 shows markdown streams as
append-dominant (churn acceptable, or line-boundary flushing fixes it).

### Phase 0 results (2026-07-11)

Spikes in `services/agents/spike/` (`pnpm exec tsx --test spike/*.test.ts`).

- **U2 — GREEN.** `parsePartialJson` fed char-by-char: `pathCompleteAt=42` **<**
  `contentFirstAt=55` (path resolves before content starts — schema order holds
  on the wire), `prefixBreaks=0` (decoded content grows prefix-consistently),
  final deep-equals the original. 261 `repaired-parse` steps + 1 `successful-parse`.
  ⇒ `parsePartialJson` is the right primitive; decode is clean.
- **U3 — GREEN, with a hard requirement.** Feeding growing content into a real
  Y.Doc via `setDocFileAsAgent`:

  | case | prefixBreaks (churn) | Yjs update bytes |
  |---|---|---|
  | plain text, char-by-char | 0 / 233 | 4.4 KB |
  | **markdown, char-by-char** | **13 / 233** | **151 KB** |
  | **markdown, line-by-line** | **0 / 11** | **5 KB** |

  ⇒ char-by-char markdown churns (13 earlier-text rewrites) AND re-emits the whole
  ProseMirror fragment on partial-syntax reparse (151 KB for a 200-char file).
  **Line-boundary flushing is MANDATORY for markdown** — 0 churn, ~30× less write
  volume. (Plain `Y.Text` is clean either way.)
- **U1 — GREEN** (live, `claude-sonnet-5`). One real addFile of a 3,665-char
  markdown body streamed as **37 `tool-input-delta` parts** (sizes min 2 / max 177
  / avg 103), content growing 5 → 1098 → 2265 → 3376. Anthropic streams tool input
  granularly ⇒ **the feature is viable.**

**Phase 0 verdict: all three green — proceed. Line-boundary flushing is a hard
requirement (U3).**

---

## Incremental build (only after Phase 0 passes)

### Phase 1 — `StreamingDocWriter` module (isolated, unit-tested)

`services/agents/src/collab/streaming-add.ts` — a pure observer, injected a
`RoomPeer` + the turn's `FileBundle` (for the early path gate), no wiring:

- `observe(part)`: on `tool-input-start`+`toolName==="addFile"` open a per-`id`
  accumulator; on `tool-input-delta` append + (throttled) `parsePartialJson`; on
  `tool-input-end` drop the accumulator (execute takes over).
- Early gate: once `value.path` resolves and `isMarkdownPath(path)` and passes
  path-only checks (non-empty, `!bundle.has(path)`), begin streaming; else skip.
- Growth: when decoded `content` grows, `peer.set(path, content)` (diff-and-patch
  ⇒ tail insert). **Flush on line boundaries** to limit ProseMirror churn (U3).
- Serialized async tail so `observe` stays sync and writes stay ordered.
- **Rollback**: track streamed-but-not-finalized paths; the turn's failure/abort
  path calls `peer.delete(path)` for any left dangling (truncation, `stopWhen`).

Unit test: replay a captured `tool-input-*` sequence (from Phase 0) → assert the
injected fake peer receives incremental `set`s, path-before-content, and a delete
on a truncated sequence.

### Phase 2 — wire into the turn (flag-gated)

`run-conversation-turn.ts`: when `collabPeer` present and toolset `files`, build a
`StreamingDocWriter(peer, bundle)` and wrap `onEvent`:
`const onEvent = (p) => { writer?.observe(p); input.onEvent(p); }`. Finalize/rollback
in the existing try/finally. Behind `config.streamDocWrites` (env
`AGENT_STREAM_DOC_WRITES`, default off) so it ships dark and is a no-op when unset
(byte-identical to today). `execute`/`DocFileBundle` unchanged — the final
`peer.set` is an idempotent no-op when the streamed content already matches, and it
remains the source of the D14 manifest.

Tests: agents-service integration test with a scripted stream + a fake collab peer;
assert incremental writes with the flag on, single write with it off.

### Phase 3 — e2e (per `e2e-verification-discipline`)

FULL cluster teardown → fresh spawn (`AGENT_STREAM_DOC_WRITES=true` on the agents
service), drive a real "Generate requirements/design" room turn, and verify from
**mid-stream Playwright screenshots** that the markdown body grows incrementally in
the editor (not one jump at the end). Playwright via a Sonnet agent. Confirm:
accept/reject marks still work, the committer still commits the final file, and the
flag-off path is unchanged. fix → re-test → repeat until zero issues.

---

## Gotchas (decided up front)

1. **Deltas are JSON-escaped** — decode via `parsePartialJson`, never concat raw.
2. **Markdown ProseMirror churn** on partial syntax → flush on line boundaries.
3. **Rollback** on reject/truncation (`maxOutputTokens` cutoff mid-`addFile`,
   `run-turn.ts:64`) → `peer.delete` un-finalized paths on the failure path.
4. **Parse cost** O(n) per delta / O(n²) per file → throttle (~50 ms or per line).
5. **Correlate by `id`** (== `toolCallId`); a `Map` handles multiple `addFile`s per step.
6. **Room mode only** — fold mode has no doc; the FE would need its own preview
   (out of scope). Flag-off and non-collab turns are byte-identical to today.

## Scope decisions

- **v1 = room-mode, markdown `addFile`, line-boundary flush, delete-on-failure,
  flag-gated.** ~1 new module + a ~3-line wrap; no changes to `FileBundle`, the
  manifest, or the BFF.
- Deferred: YAML/`design.json` streaming; fold-mode FE live preview; `editFile`
  (anchored — different problem).
