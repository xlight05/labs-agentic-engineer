---
name: aep-local
description: Load when working a component task in the AEP playground's LOCAL mode. The cwd is a plain project directory (not a git clone); the task is an issue FILE under issues/ passed in your prompt — there is no GitHub, no remote, no PR. Defines the local workflow, the issue-file status contract (derivedStatus), constraints, the deny-list, project-structure conventions, and the verify-before-done step. Stack-specific conventions (Go, React, …) live in separate project skills the playground also preloads — apply them.
---

# AEP playground component task (local mode)

You are working a single component task in the AEP playground. The current
working directory is the project itself — a plain local directory the
developer chose. There is **no git remote, no GitHub, and no PR**: you edit
the project in place, and the task is described by a markdown **issue file**
(e.g. `issues/3.md`) whose path is passed in your prompt.

> This skill is the local-mode counterpart of the platform's `aep` skill. The
> project conventions below (Project structure, Constraints, workload.yaml)
> are shared VERBATIM with that skill — what you hone here transfers to the
> platform unchanged.

## Active project skills

In addition to this `aep-local` skill, the playground preloads
**project-attached skills** at startup — they carry the stack/auth/runtime
conventions for this project (e.g. `go`, `react-webapp`). They appear in your
context alongside this body; consult them whenever their concern is relevant.
When the issue body's Scope section says something like "Use
modernc.org/sqlite", that's a `go` requirement — the skills are the
authoritative source for those conventions, do not re-derive them from
training data.

## Find the task

Your prompt names the issue file (relative to your cwd). **Read it first.**
It is markdown with YAML frontmatter:

```markdown
---
issueNumber: 3
component: "user-service"
title: "Implement the user service"
dependsOn: ["auth-service"]
origin: "spec-plan"
derivedStatus: "ready"
---

> **Rationale:** one-line planner justification

<scope, acceptance notes, files to touch>
```

The body is the spec. The component's design lives at
`specs/design/components/<component>/design.json` (App Path, dependencies,
endpoint) and the wider context under `specs/requirements/` and
`specs/design/` — read what you need.

## Workflow

1. **Read the issue file** and the component's `design.json`. The issue body
   is the spec; the design carries the App Path and dependencies.
2. **If the project is a git repository**, you MAY `git commit` locally per
   logical step (free diffing for the developer). **Never push, never add a
   remote.** If it is not a git repo, just edit files.
3. **Apply the project's attached skills.** Stack patterns live in the
   per-skill bodies — see "Active project skills" above.
4. **Implement in place** under the component's App Path (see "Project
   structure" below).
5. **Build verification.** Run the local toolchain check for your stack
   (the exact commands are in the stack's project skill — e.g. the `go`
   skill's "Build verification" section). If it fails, read the error, fix,
   and rerun. Only proceed once the check exits 0. You have discretion to
   give up after a reasonable number of attempts (suggested: **3 tries** for
   a given root cause).
6. **Update the issue file** (this replaces the platform's PR + issue
   comments):
   - Append a `## Progress` section (create it if absent) with a short,
     dated note: what you built, how you verified it, anything left.
   - Set the frontmatter `derivedStatus`: `"deployed"` when the work is done
     and verified; `"failed"` when you exhausted your retry budget — in that
     case the Progress note MUST carry the last ~40 lines of the failing
     command output and what you tried.
   - Touch nothing else in the frontmatter (`issueNumber`, `component`,
     `title`, `dependsOn`, `origin`, `key` are the planner's).

## Project structure

Create a production-ready project structure under each component's
**App Path** (from the issue's Component Reference card). The App Path
is a **folder name** relative to the repo root (e.g. `user-api`,
`services/auth`) — it is NOT an HTTP route. All of that component's
files (source, `Dockerfile`, `workload.yaml`) must live under that
directory and nowhere else; the platform watches that path to decide
which component to rebuild on a push, so a file committed outside it
will not trigger its build.

Stack-specific layout, Dockerfile shape, and library choices live in
the relevant project skill (`go`, `react-webapp`, etc.) — do not
re-derive them.

Every component must have a `workload.yaml` at the root of its app
path (format below). The platform commits, pushes, builds, and deploys
for you.

## Constraints

- Implement the full API contract described in the issue. Every endpoint
  must be functional.
- The component must have a `Dockerfile` for containerized builds.
- The app must start with **no required environment variables** — use
  sensible hardcoded defaults for all config (JWT secrets, DB paths,
  API URLs, etc.). Env vars may override defaults but must never be
  required.
- No stubs or mocks. Write real, working implementations.
- Do not run, start, or execute the application server. Only write
  source files. The platform builds and deploys automatically; local
  execution causes port conflicts. Quick compile checks (`go build`,
  `tsc --noEmit`) are fine; never use `go run`, `npm start`,
  `node server.js`, or any command that starts a long-running process.
- **Never hand-write or guess dependency lockfile checksums.** Always
  regenerate the lockfile with your stack's dependency tool and commit
  the result — the exact command is in the relevant project skill's
  "Build verification" section (e.g. `go`, `react-webapp`). Hand-writing
  checksums causes the build pipeline to fail with
  `checksum mismatch ... SECURITY ERROR`.
- **Every service component with dependents MUST declare at least one
  HTTP endpoint with `visibility: external` in its `workload.yaml`** —
  this is what makes the deployed URL reachable for the dependent SPA's
  browser AND lets the BFF resolve the URL into `window._env_` for any
  sibling web-app that depends on this service (a `dependencies` entry of
  `kind: component`).

## Do not

- Touch, read, or even list anything outside the current working directory —
  never `~`, never other projects or repositories on this machine, never
  system paths. Do not probe whether such paths exist. Everything you need is
  inside the cwd (`specs/`, `issues/`, the app source) and your preloaded
  skills.
- Add a git remote, `git push`, or run any `gh` command. There is no remote.
- Install anything outside the project's own package manager (no `brew`,
  no `apt`, no global `npm -g`, no `pip install` outside a project venv).
- Delete or rewrite `.aep-playground/` (the playground's state dir).
- Start long-running processes (servers, watchers, containers).
- Renumber, delete, or bulk-rewrite issue files — only the `## Progress`
  section and `derivedStatus` of YOUR issue are yours to write.

## OpenChoreo Workload Configuration

Every component must have a `workload.yaml` at its root, even in local mode —
the file is part of the component's shape and what you hone here transfers to
the platform. This file uses the **flat WorkloadDescriptor** format — **not**
a Kubernetes CR. Do **not** use `kind: Workload`, `spec:`, `autoBuild`, or
`autoDeploy`.

### Format

```yaml
apiVersion: openchoreo.dev/v1alpha1
metadata:
  name: <component-name>        # logical name — no project prefix

endpoints:
  - name: http                  # MUST equal design.json `endpoint.name` (default
                                # `http` when it declares none). The managed-API
                                # gateway binds to THIS name; a mismatch fails deploy
                                # rendering (`workload.endpoints["<name>"]: no such key`).
    type: HTTP                  # HTTP | GraphQL | Websocket | TCP | UDP | gRPC
    port: <port>
    basePath: /                 # optional; root path for API services
    visibility:
      - external                # REQUIRED for v1 service components with dependents
```

When the component's `design.json` declares `dependencies`, wire consumer-side
env-var bindings the way the stack skill describes — read injected values from
env var **names**, never hardcode an upstream URL. In local mode there is no
platform to resolve real addresses; use the design's dependency names for the
env-var naming and sensible localhost defaults.

## ClusterResourceType authoring — rendering context rules

When a task asks you to author or edit an OpenChoreo `ClusterResourceType`
manifest (the YAML that defines a platform resource such as `postgres-cnpg`
or `thunder-app`), the template rendering context is **not** the same as the
component `ReleaseBinding` rendering context. Getting this wrong causes the
controller to fail with a CEL compilation error at reconcile time, which
leaves every `ResourceReleaseBinding` that references this type permanently
stuck in `Building`.

### Available variables in resource rendering context

These variables are in scope when templates inside a `ClusterResourceType`
are evaluated (e.g. `resourceTypeEnvironmentConfigs`, `includeWhen`
expressions, Helm value templates):

| Variable | What it holds |
|---|---|
| `metadata` | The `Resource`/`ResourceRelease` object metadata |
| `parameters` | The resource's static parameters (from the `Resource` spec) |
| `environmentConfigs` | The per-env values from the `ResourceReleaseBinding` |
| `applied` | The current applied state returned by the data-plane operator |
| `dataplane` | Data-plane-specific outputs (e.g. connection strings) |

### Variables that are NOT available

| Variable | Why it is absent |
|---|---|
| `gateway` | Component-level only — present in `ReleaseBinding` (workload rendering), never in `ResourceReleaseBinding` (resource rendering) |
| `workload` | Component-level only |
| `dependencies` | Component-level only |

**`gateway` is the most common mistake.** It exists in the component's
`ReleaseBinding` rendering context (where ingress, routes, and TLS are
available), but it is completely absent from the `ResourceReleaseBinding`
rendering context used by `ClusterResourceType` templates.

### `includeWhen` CEL is compiled at reconcile time

CEL expressions in `includeWhen` fields are **compiled and type-checked
when the controller reconciles the binding**, not when they evaluate to
`true`. A false-guarding condition does NOT prevent compilation:

```yaml
# WRONG — fails with CEL type error even though `adminEnabled` is false,
# because `gateway` is not in scope and CEL validates all references
includeWhen: '${environmentConfigs.adminEnabled && has(gateway.ingress.external)}'

# CORRECT — only reference variables that exist in resource rendering context
includeWhen: '${environmentConfigs.adminEnabled}'
```

If you need to conditionally include a resource that also requires gateway
routing (e.g. an admin UI), that admin UI must be implemented as a **separate
component** (with its own `workload.yaml` and `ReleaseBinding`) — it cannot
be embedded inside the `ClusterResourceType` template.
