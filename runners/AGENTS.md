# AGENTS.md — runners/

One-shot / job images (not long-lived services). Run to completion in a pod.

**Status:** `remote-worker/` holds the `aep` skill plugin loaded by the
coding-agent runner — a TS Claude Agent SDK one-shot pod that provisions a
workspace, loads the `aep` skill, and runs the Agent SDK. The dev flow
bind-mounts `runners/remote-worker/plugin` into the runner pod for live skill
edits (see `deployments/scripts/setup-k3d.sh`).

## Conventions

- One entry point per pod (`src/oneshot.ts`); everything reachable from it.
- **Never put a credential in a git URL or in argv.** All clones go through
  `lib/git_clone.ts`, which passes the token via a `GIT_ASKPASS` shim in the
  child env — an authenticated URL leaks into `child_process` error messages
  (which the BFF forwards to the console build log), into `ps`, and into
  `.git/config`. Rationale inline in `git_clone.ts`; the BFF keeps a shape-based
  second line of defense in `delivery/codingagent/redact.go`.
- Runner `console.*` is a **user-facing** channel — the BFF turns every
  non-NDJSON pod line into a build-log event. `installConsoleScrubber()` at each
  entry point routes it through the scrubber; don't bypass it.
- Self-contained: all agent and SDK-specific wiring lives here.
- **Skills scope is stated by the caller, never read off `AEP_COMPONENT_NAME`.**
  A milestone Job carries a sentinel there (`aep-milestone`), so an
  implementation run resolves the union of `skillsApplied` across every
  `specs/design/components/*/design.json`; a validation run applies no design
  skills at all. The local harness (`local.ts`) carries its own sentinel
  (`aep-local-milestone`) for the same reason: a playground coding run works
  the whole project, same as the milestone loop, and may touch several
  components — there is no single one to name.
- **ONE authored workflow skill, composed per mode.** `plugin/skills/aep/SKILL.md`
  serves both the platform's GitHub-backed runs and the playground's file-based
  ones. Where they differ, the text is marked `<!-- mode:github -->` /
  `<!-- mode:local -->` — alone on a line to gate whole lines, or with content
  beside it to gate a clause — and `lib/skill_compose.ts` resolves it at session
  start. Anything unmarked is shared and cannot drift. Both modes go through the
  strip step: an unstripped marker region would inject the wrong procedure, so
  there is no "raw" path that skips it. Mode is stated by the entrypoint
  (`BaseAgentConfig.mode`, default `github`), never inferred.
  **The platform text is the trunk** — gate a region only when the ungated
  version would make a mode attempt something impossible, and prefer gating one
  side to writing two variants. A *paired* region is prose duplicated per mode
  and is what the test suite caps; reading an inert paragraph is cheaper than
  maintaining a second copy of it. ADR:
  `remote-worker/design/decisions/ADR-0001-one-mode-composed-skill.md`.
- **Running the `aep` skill by hand.** Install the plugin into your own Claude
  Code (`claude plugin install <repo>/runners/remote-worker/plugin`) and use your
  own `gh auth login`; the workflow is the platform's. Note the installed plugin
  is the *authored* source, markers and all — the composed form is what a real
  run loads, so use the playground (`pnpm play <dir> code`) if you want the
  local-mode body.
- **One image**, `remote-worker/Dockerfile`, serves BOTH task kinds
  (`AEP_TASK_KIND=implementation` and `=validation`). It is Debian-based
  because Playwright's browsers are glibc-linked; do not reintroduce a second,
  slimmer image without moving the Helm/compose/release/`AGENT_RUNNER_IMAGE`
  consumers with it. Build + k3d-import it locally with `make build-runner`.
  Full `deployments/scripts/setup.sh` pre-builds it in the background (off the
  critical path) and imports it in `setup-aep.sh`; `PREBUILD_RUNNER=0` reverts
  to a serial build. The build is skipped when the tag exists, so use
  `FORCE=1 make build-runner` after changing the Dockerfile or `src/`.
