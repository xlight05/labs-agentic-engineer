# AGENTS.md — runners/

One-shot / job images (not long-lived services). Run to completion in a pod.

**Status:** `remote-worker/` holds the `aep` skill plugin loaded by the
coding-agent runner — a TS Claude Agent SDK one-shot pod that provisions a
workspace, loads the `aep` skill, and runs the Agent SDK. The dev flow
bind-mounts `runners/remote-worker/plugin` into the runner pod for live skill
edits (see `deployments/scripts/setup-k3d.sh`).

## Conventions

- One entry point per pod (`src/oneshot.ts`); everything reachable from it.
- Self-contained: all agent and SDK-specific wiring lives here.
- **One image**, `remote-worker/Dockerfile`, serves BOTH task kinds
  (`AEP_TASK_KIND=implementation` and `=validation`). It is Debian-based
  because Playwright's browsers are glibc-linked; do not reintroduce a second,
  slimmer image without moving the Helm/compose/release/`AGENT_RUNNER_IMAGE`
  consumers with it. Build + k3d-import it locally with `make build-runner`.
