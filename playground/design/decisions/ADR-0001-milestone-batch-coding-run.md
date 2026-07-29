# ADR-0001: The playground's coding run is one milestone-style batch, not one issue at a time

**Status:** Accepted (2026-07-29)

## Context

Production's `feat/issue-driven-execution` flip (`docs/decisions/ADR-0011-milestone-is-the-unit-of-execution.md`,
commit `4d68637a`) retired per-issue dispatch entirely: the BFF's prompt to
the coding agent is a **milestone reference and nothing else**
(`delivery/codingagent/coding_executor.go:buildPrompt`), and the `aep` skill
discovers its own working set from the live issues API, orders it by
`Depends on #N` prose, works as many as it can in one session — fanning
independent ones out to subagents — and opens ONE pull request. An issue's
`derivedStatus` was reduced to two honest values (`pending`/`merged`,
`delivery/read_views.go`), read from live GitHub issue state, never cached.

The playground's coding-run engine predated that flip and still mirrored the
retired model: `codeCommand` ran ONE `remote-worker/local.ts` process per
issue, the CLI computed a topological execution order
(`FsIssueStore.executionOrder`) and a `blockedBy` dependency gate itself, and
`coding-run.ts` was the writer of record for a `derivedStatus` frontmatter
field (`ready` → `running` → `deployed`/`failed`) driven by the child
process's exit code. None of that shape exists in production anymore.

## Decision

The playground's coding run now mirrors the milestone loop:

1. **One session works the whole project.** `codeCommand` spawns exactly one
   `runCodingAgent` call over the project directory — never per issue. There
   is no single-issue debug entry point.
2. **Discovery and ordering move into the skill**, out of the CLI.
   `FsIssueStore.executionOrder` and the `blockedBy` dependency gate are
   deleted. The `aep` skill, composed for local mode, reads
   `issues/*.md` itself, orders on the `dependsOn` component edges in each
   issue's frontmatter, and fans independent work out to subagents under the
   same bar `aep` uses (independent + disjoint App Paths + big enough).
3. **No status field, anywhere.** Production's `derivedStatus` is never
   persisted in a context file (`taskplan/context_file.go`'s `Render` never
   emits it) — it is read fresh from GitHub issue state. The playground has
   no such oracle, so it re-derives the same fact the same way: whether an
   issue is done is decided by whether its component's **App Path** already
   holds a working implementation that satisfies it, checked fresh by the
   skill's discovery step every run. Deleting a component's generated code —
   with no edit to any issue file — is enough to put its issue back in the
   working set. The TUI (`tasks.ts`, `phase-menu.ts`) shows a cheap directory-
   existence probe (`state/status.ts#issueLooksResolved`) for a list glyph
   only; it is not the agent's real judgment.
4. **No tolerant-repair safety net.** Production's dispatch returns before
   any outcome is known — a run signal is "a wake-up, never evidence"
   (`delivery/run/doc.go`) — so `coding-run.ts` no longer reads back or
   flips any frontmatter on exit code, and no longer stamps an in-flight
   `"running"` marker. The agent's own edits are trusted completely.
5. **A batch's exit code tracks whether the session completed, not whether
   every issue got resolved.** Leaving issues open for a later run is normal
   (the `aep` skill's own Finish section: "a later cycle picks it up"), never a failure by itself.
   Only a crash or the agent giving up entirely is a failure.
6. **Skills resolve at project scope.** `local.ts` reads
   `{kind: "project"}` (the union of every component's `design.json`
   `skillsApplied`) instead of one component's — the same
   already-shared mechanism `skills_resolver.ts` uses for prod's milestone
   Jobs. Its `AEP_COMPONENT_NAME` carries a label-only sentinel
   (`aep-local-milestone`), mirroring prod's `aep-milestone`.

## Consequences

- The playground can now exercise the actual thing worth validating locally:
  cross-issue ordering, fan-out, and one session resolving many issues — the
  core of what changed in production. Editing the workflow skill is now literally
  the same edit as editing production's — one authored `SKILL.md`, mode-composed
  (`runners/remote-worker/design/decisions/ADR-0001-one-mode-composed-skill.md`).
- `dependsOn` in the playground's issue frontmatter names **components**
  (read straight off the frontmatter), not issue-number prose the way `aep`'s
  `Depends on #N` convention does — this was already true before this ADR and
  is unchanged; only who *acts* on it moved (skill, not CLI).
- Per-issue commits with `(#N)` attribution (git repo only) remain a
  developer diffing courtesy, and double as the same crash-resume aid `aep`
  uses (§3b there) — but they are never load-bearing for "is this done," since
  the playground doesn't require a git repo. Whether the code exists is.
- A project copied without its earlier per-issue status history behaves
  identically to one that always ran this way — there is nothing to migrate.
