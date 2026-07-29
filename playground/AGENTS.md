# AGENTS.md — @aep/playground

Root-level local-filesystem playground: runs the **real** engineering agent
(in-process `createApp` boot) and the **real** coding agent (remote-worker
`local.ts`) against a plain project directory. Purpose: edit a `SKILL.md`, a
prompt, or a steer copy → rerun one phase → observe. No git, no GitHub, no
Postgres, no cluster.

The coding agent loads the **same `aep` skill production loads**, composed for
`mode: "local"` — one authored `SKILL.md` with the GitHub-shaped steps marked
inline, so tuning it here IS tuning the platform's. See
`runners/remote-worker/design/decisions/ADR-0001-one-mode-composed-skill.md`.

## Run

```
pnpm play                              # picker → phase menu
pnpm play <dir>                        # phase menu
pnpm play <dir> requirements --idea "…"
pnpm play <dir> design | tasks | check | undo
pnpm play <dir> code [--restore] [--yes]   # ONE coding-agent session works the
                                            # whole project — no per-issue run
```

`code` mirrors prod's milestone cycle (ADR-0011): the CLI never picks an issue
or an order — the `aep` skill discovers its own working set from
`issues/` (does each issue's App Path already look done?), orders it, and
works as many as it reasonably can in one session, fanning independent ones
out to subagents. See `design/decisions/ADR-0001-milestone-batch-coding-run.md`.

No review/browse affordances: the playground auto-writes files and the user's
editor (VS Code) is where browsing, diffs, and hand-edits happen — including
authoring `issues/<n>.md` by hand (picked up automatically).

Flags: `--idea`, `--target`, `--fresh` (rotate the general conversation),
`--silent`, `--restore`, `--yes` (headless coding consent). Every verb exits
nonzero on failure — the edit-skill → rerun loop is scriptable.

Relative project paths resolve against where you launched `pnpm play` (pnpm's
`INIT_CWD`). The picker's default is `<repo>/playground/.projects/my-app` —
`playground/.projects/` is the ONE place inside the checkout where projects
may live (a gitignored dot-dir, invisible to lint + license gates). Anywhere
else inside the repo is refused.

Requires `ANTHROPIC_API_KEY` (env or `deployments/.env`). Skills load from the
working-tree `skills/` on EVERY turn — edits apply next run, no rebuild.

**AI SDK DevTools is always on** (`src/devtools-default.ts`): every
engineering-agent LLM call — the composed prompt, tool calls, usage, timing —
is captured to `playground/.devtools/generations.json` (gitignored). Inspect
with `npx @ai-sdk/devtools` (port 4983). Opt out per run with
`AGENT_DEVTOOLS=false pnpm play …`. The coding agent is an Agent SDK session,
not an AI SDK model — its full transcript is the run's
`.aep-playground/runs/<ts>/…/claude.log` instead.

## Fidelity contract

The bytes reaching the model are production-identical: the same server code
path (auth middleware, TurnGuard, workspace shape, snapshot filter, write
gates), the same instruction composition (`src/engine/compose.ts` carries
provenance-pinned verbatim copies of the live Go steer strings —
`test/steer-parity.test.ts` fails on drift), the same skills materialization,
the same runner session options (`resolveBaseAgentConfig` defaults are
unit-pinned in remote-worker), and the same authored workflow skill (only the
mode-marked GitHub steps are swapped — `skill_compose.test.ts` pins which text
is shared and asserts neither mode leaks the other's procedure).

## Scope ends when the code lands

The playground covers requirements → design → tasks → code, ending when the
coding-agent session stops (some issues may stay open — a later run picks them
up, same as prod). There is NO build/deploy half and none should be
added: no image builds, no `docker build` of the authored Dockerfile, no
deploy attempt, no validation-task runs. Two things that look build-ish stay
deliberately — the agent's local toolchain verification (`go build`,
`tsc --noEmit`; code quality, not a platform build) and `workload.yaml` +
`Dockerfile` authoring (the component's shape; what you hone here must stay
platform-ready). Taking a project further is a HANDOFF, not a playground
feature: the project is a plain directory with production-layout `specs/` —
push it to a repo and let the platform's normal flow build/deploy it.

## Documented divergences from production (do not mistake for platform behavior)

| Divergence | Why | Parity path |
|---|---|---|
| `issues/` excluded from spec-turn snapshots | production spec turns never see tasks (they live in GitHub) | n/a — this IS parity in effect |
| MCP off by default | no cluster; avoids a localhost mint attempt per turn | run aep-api locally + AEP_MCP_URL (its MCP resolver) |
| `collabDepsSteer` present without a live MCP tool | kept for byte parity — all console turns carry it | MCP passthrough makes the named tool real |
| No CRT-annotation append, no lineage diffs in replans | platform resources/tags don't exist locally | manual edit; replan is still files-based |
| Issue `key` lineage constant `"local"`; no spec/design tags | no builds/tags locally | dedupe across replans still works |
| Design/tasks gates are playground-side UX | production has no server gate on the console's spec paths | advisory only |
| No status field on an issue file | prod's own `derivedStatus` is read from GitHub issue state, never cached; the playground has no such oracle, so it re-derives "is this done" from whether the App Path looks implemented, every run | none needed — deleting a component's code puts its issue back in the working set |
| Coding agent runs bypassPermissions ON THE HOST | production uses a disposable pod | mandatory undo snapshot + first-run consent; point it at scratch/git-tracked projects |
| No GitHub-shaped steps in the workflow skill (issue files, no branch, no PR) | there is no remote to discover issues from or open a PR against | the deliberate one: the same authored `aep` skill, composed for `mode: "local"`; everything outside a `<!-- mode:… -->` block is shared with production verbatim |

## Layout

`src/ports/` — the swapped adapters (FsSpecWorkspace, FileConversationStore,
FsIssueStore). `src/engine/` — session boot, the §5 turn loop, instruction
composition, coding-run spawn. `src/tui/` — clack screens; every screen is
also a headless verb in `src/commands.ts`. `test/` — mock-model phase tests
(no tokens) + the parity pins.

A playground project keeps its state in `<project>/.aep-playground/`
(conversations, runs, undo snapshots, prompt.md, project.json) — a dot-dir,
so engineering-agent turns never see it.
