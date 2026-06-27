# AGENTS.md — services/agents (`@aep/agents`)

TS interactive spec agents (Vercel AI SDK). Seeded with ONE agent: the **main
file-mutation agent** (prompt-driven add/edit/remove over a spec bundle), exposed
as an **SSE turn stream** (one turn = one HTTP request). The runtime **writes no
files** — accept/edit/save is a separate concern.

## Design

Wire types (SSE events, `OpResult`, `*Input`, `Change`) live in `@aep/contracts`
(`src/agents/sse-events.ts`) — the source of truth; Zod schemas are drift-guarded
against them. See `design/` (`ADR-0001-anchored-file-edits.md`,
`ADR-0002-skills-progressive-disclosure.md`, `agent-loop-and-eval-framework.md`).

**Skills** are guidance (not code): the caller pushes `skills: { name, description,
content }[]` in the turn payload, the service shows a name+description **catalog** at
the end of the system prompt, and the agent pulls a body on demand via the
**`loadSkill`** tool. The service never reads skills from disk (the eval reads
repo-root `skills/`); no skills in the payload → no catalog, behaves as today. See
ADR-0002.

## Run

- `pnpm --filter @aep/agents dev` — SSE server, watch/reload. `start` — run once.
- Endpoints: `POST /conversations/:id/turns` (SSE) · `GET /conversations/:id`.
- Needs `ANTHROPIC_API_KEY` (export, or `.env` at monorepo root — see `.env.example`).

## Test

- `test` — source unit tests (`src/**/*.test.ts`), no tokens.
- `test:eval` — deterministic eval-tree tests (`evals/**/*.test.ts`), no tokens.
- `eval` — model suite over the live route; report-not-gate, skips without a key.
- `typecheck:eval` — typecheck the eval tree (`tsconfig.eval.json`).

## Conventions

- SDK wiring lives here, not in `packages/agent` (SDK-agnostic surface).
- Latest Claude models by default (see the `claude-api` skill for model ids).
- One agent per `src/agents/<name>/`; the loop (`run-turn.ts`) is shared.
- `src/` writes no files; only `evals/` touches the filesystem.
