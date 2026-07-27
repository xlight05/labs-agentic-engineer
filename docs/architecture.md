# AEP — Architecture

> Repo-wide map. Each service's README is the source of truth for its own
> current architecture — start with [`aep-api`](../services/aep-api/README.md).

## Overview

AEP is a spec-driven, AI-enhanced SDLC platform built on OpenChoreo. It is a
polyglot (Go + TypeScript) monorepo organized around **shared contracts**: every
REST boundary is described by an OpenAPI document owned by its producing service,
and every consumer uses generated types — so an incompatible change is a
compile error, not a runtime surprise.

## Buckets

| Bucket | Contents | Deploys? |
|---|---|---|
| `apps/` | React webapps (Vite + Oxygen UI) | yes |
| `services/` | long-lived deployables (Go + TS) | yes |
| `runners/` | one-shot / job images | as jobs |
| `packages/` | shared libraries (`contracts`, `core`, `ui`, `clients`, `agent`) | no |
| `deployments/` | canonical local setup (k3d + docker-compose); resource types that ship a reference operator keep it under `resource-types/<type>/operator/` (e.g. `thunder-app-operator`) | n/a (operator subdirs: yes, in-cluster) |

## Data & contract ownership

- Each service **owns** the OpenAPI it produces, stored under
  `packages/contracts/<service>/openapi.yaml`.
- Internal events are JSON Schema under `packages/contracts/events/`.
- Generated clients/servers are never hand-edited. Whether they are committed
  differs by consumer: aep-api's contract codegen (`internal/gen/`, `internal/igen/`)
  and the OpenChoreo client are **committed**, with `make gen-api-check` as the CI
  freshness gate; the console's `apps/console/src/generated/` is gitignored and
  regenerated as a build prestep.

## Codegen pipeline

```
openapi/*.yaml ──> openapi-typescript ──> TS client (generated/)
              └──> oapi-codegen ────────> Go StrictServerInterface (*.gen.go)
```

The build graph (`turbo` + `go.work`) wires every consumer's `build`/`typecheck`
behind `gen`, and CI runs `gen` + `git diff --exit-code` to catch staleness. See
`docs/decisions/ADR-0001-tooling-and-naming.md`.

## Service map

- [`aep-api`](../services/aep-api/README.md) — Go BFF + GitHub webhook receiver
  (git ops folded in); domain-oriented modules + vertical slices.
- `agents` — TS interactive spec agents (Vercel AI SDK).
- `collab` — TS Yjs collaboration server.
- `aep-mcp-server` — MCP surface for the SRE/RCA handoff.
- `remote-worker` (runner) — TS Claude Agent SDK one-shot pod; one image serves
  both task kinds.
- `console` (app) — React frontend.

## How a version gets built

A spec version is cut as a `v<N>` tag and executed as **one supervised run over
one GitHub milestone**: the planner mints prose issues into it, one coding agent
works the whole milestone per cycle, its pull request auto-merges, the merge
fans out to a build per changed component, and the run settles when the working
set is empty and validation has a verdict. The decision and its costs are
[ADR-0011](decisions/ADR-0011-milestone-is-the-unit-of-execution.md); the
mechanism is
[`internal/delivery/README.md`](../services/aep-api/internal/delivery/README.md).
