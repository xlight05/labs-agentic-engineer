# ADR-0002: Skills via progressive disclosure (`loadSkill`), pushed in the turn payload

- **Status:** Accepted · **Date:** 2026-06-27 · **Scope:** `@aep/agents` main agent

## Context

We want the main agent to follow reusable, named **guidance** ("skills") — house
style, API-design rules, etc. — while editing a spec bundle. Skills are authored as
`SKILL.md` files (frontmatter `name`/`description` + markdown body) and live in a
repo-root `skills/` directory.

Two pulls shaped the decision:

- The AI SDK now offers `uploadSkill` + `container.skills` — Anthropic's *container
  skills*. But that path only activates the skill when the same call carries the
  `code_execution` tool and runs in Anthropic's hosted sandbox. Our agent has no
  code execution; its files live **in-memory in the service process** and never
  reach a sandbox. Container skills would change nothing about how the file tools
  behave. So `uploadSkill` is the wrong mechanism for *guidance*.
- A skill body is large; most turns need none or one. Inlining every body into every
  prompt is wasteful and dilutes attention.

## Decision

Deliver skills as **guidance text via progressive disclosure**, with the caller
pushing skills in the turn request payload.

- **Payload, not disk.** The turn request carries `skills: { name, description,
  content }[]`. The service **never reads skills from the filesystem** — the caller
  resolves them. In evals the harness reads repo-root `skills/` and sends the whole
  library; in production a BFF pushes them. (`src/` writes no files; only `evals/`
  touches the fs — existing service invariant.)
- **Catalog, then load.** The service appends a **catalog** — `name` + `description`
  only, no bodies — to the **end** of the system prompt. A new **`loadSkill(name)`**
  tool returns a skill's `content` as a tool result; the body enters context only
  when loaded, and persists in message history thereafter.
- **Corrective miss.** `loadSkill` on an unknown name returns a self-correctable
  error result (`unknown skill: X; available: …`), never aborting the turn —
  matching the `editFile` `NOT_FOUND`/`NOT_UNIQUE` convention (ADR-0001).
- **Inert when absent.** No skills in the payload → no catalog, `loadSkill`
  unregistered → byte-identical to today's behavior. `loadSkill` is not a bundle
  mutation, so it projects to **no `Change`**.

Wire types (`Skill`, `LoadSkillInput`) live in `@aep/contracts`; the Zod schema is
drift-guarded against them, as with the file tools.

## Alternatives rejected

- **Anthropic container skills (`uploadSkill` + `container.skills`).** Requires the
  `code_execution` tool + a hosted sandbox that cannot see our in-memory bundle, and
  delivers an *executable* capability rather than guidance over the existing file
  tools. Wrong shape for this need.
- **Inline every skill body into the system prompt.** Simplest, but spends tokens on
  bodies the agent never uses and weakens focus as the library grows. Progressive
  disclosure keeps the always-on cost to one catalog line per skill.
- **Service reads `skills/` from disk (packed into the image).** Considered first;
  rejected to keep the service free of a skills loader, path config, and a Dockerfile
  dependency, and to preserve the "`src/` touches no fs" invariant. Resolution is the
  caller's job — bytes ride in the payload (the existing push model).

## Consequences

- Always-on prompt cost is one catalog line per skill; bodies cost tokens only when
  loaded. A loaded body persists in history (continuity across later turns in the
  same conversation).
- The catalog sits at the **end** of the system prompt and is identical across a
  conversation's turns (caller sends the same library), so the cacheable prefix is
  preserved.
- Selection is two-tier: the caller decides the *available* set; the agent decides
  what to actually read. Negative/selection behavior ("loaded the right one, ignored
  the rest") is observable in the `loadSkill` call stream and testable in evals.
- `references/*.md` are out of scope: the agent has no file-read tool, so `loadSkill`
  serves the `SKILL.md` body only. A later reference-loading tool can extend this.
