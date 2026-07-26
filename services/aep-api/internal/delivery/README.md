# delivery — Delivery Pipeline

> **L2 · a domain.** Part of the [aep-api architecture](../../README.md).

Take a versioned Spec end-to-end: plan the Tasks, route every Execution through the ONE funnel, dispatch
coding agents, build/deploy the components, and run validation. **Single write-authority over the
executions store and the Temporal dev/task/validation workflows.**

```mermaid
flowchart LR
  API(["/api/v1"]) --> HTTP
  INT(["/internal/v1"]) -.-> VAL
  subgraph delivery
    HTTP["httpapi — build · task · execution handlers"]
    subgraph ROOT["root (shared kernel — types + ports + Temporal infra, no gorm)"]
      K1["Executor · DispatchRequest · TaskFacts · TaskStreamHub"]
      K2["Runtime · Signaler · signals · workflow I/O vocab (DevFlowInput/Status, DevPhase*…)"]
      K3["read DTOs — TaskView · ExecutionView · Lineage · TaskDetail"]
    end
    BUILD["build (buildpipe)"] --> ROOT
    TASK["task (taskflow)"] --> ROOT
    EXEC["execution — funnel/registry/sweep/TaskStreamService"] --> ROOT
    CODE["codingagent"] --> ROOT
    DEV["devflow — Temporal workflows/activities/worker"] --> ROOT
    VAL["validation — S2S context/credentials"] --> ROOT
    HTTP --> BUILD & TASK & EXEC
  end
  EXEC --> EXECS[("executions · workflow_runs")]
  DEV --> TMPRL[["Temporal"]]
  BUILD -->|SpecTagger · SaveSpec| SPEC[[spec]]
  BUILD -->|repo full-name| SC[[sourcecontrol]]
  CODE -->|org keys · git tokens| SEC[[platform/secrets]]
  EXEC -->|ExecutionReader| OPS[[ops]]
```

## Internal shape — kernel-root + feature sub-packages

Delivery is **not** the flat-root-of-services layout spec/organization/ops use, and **not** per-op slices.
Its absorbed features are densely cross-coupled AND carry the load-bearing `task ⊥ execution` split, which
the ordinary rules (`root ⊥ slice`, `slice ⊥ sibling`) cannot satisfy flat. The resolution: **anything
referenced across a feature boundary is a TYPE or PORT that lives in the ROOT; the feature logic that uses
it lives in a sub-package importing only the root.** `task` and `execution` are then peer sub-packages that
never import each other (the split survives as an internal boundary, re-asserted by `TestTaskExecutionSplit`),
and every former feature→feature edge becomes a legal slice→root type reference.

| Sub-package | Owns | Reaches the root for |
|---|---|---|
| `build` (buildpipe) | the whole-spec gate + `v<N>` tag cut, dev-workflow start/status, builds history, dep-drawer preflight | `Runtime`, the workflow I/O vocab, `TaskView` (via `TaskReader` port) |
| `task` (taskflow) | GitHub-native Task Commands/Reads/Plan + list/get/promote handlers | the read DTOs; reaches the funnel via the `Dispatcher` port |
| `execution` | the ONE funnel (admit/finish/reevaluate), registry, sweep, `TaskStreamService`, `OpsExecutionReader` | `Executor`/`DispatchRequest`/`TaskFacts`, `Signaler`, `TaskStreamHub` |
| `codingagent` | the CodingExecutor + dispatcher + job watchers + templates | `Executor`/`DispatchRequest`/`TaskStreamHub`, `Signaler` |
| `devflow` | the Temporal dev/task/validation workflows, activities, worker | `Runtime`, `Signaler`, the workflow I/O vocab |
| `validation` | the two S2S validation runner callbacks (context / test-credentials) | — (no cross-edges; least entangled) |
| `httpapi` | the aggregator: embeds build/task/execution handlers; **holds `Deps`** (see below) | imports the sub-packages (the exempt aggregator) |

**`Deps` lives in `httpapi`, not the root.** Every other domain keeps its `Deps` in the domain root, but
delivery's services live in sub-packages the root may not import (`root ⊥ slice`). The `httpapi` aggregator
is the one package allowed to name them, so `httpapi.Deps` + `httpapi.New` is where composition sits.

## Ports
| Port | Dir | Peer · contract |
|---|---|---|
| `Dispatcher` / `Reevaluator` | offers | `task` → the funnel's single dispatch door (root type, satisfied by `execution`) |
| `TaskReader` (returns root `TaskView`) | needs | `build` → the durable GitHub⋈executions read (satisfied by `*task.Reads` at the root) |
| `SpecTagger` (`*spec.SpecSaveResult`) · `SpecCollector` · `AuthDeriver` | needs | `spec` — the whole-spec gate + tag cut + design reads |
| `RepoLookup` (`owner/name`) | needs | `sourcecontrol` — repo full-name resolution |
| org-credential reads · `AnthropicKeyResolver` | needs | `platform/secrets` / P3a org repositories — coding-agent runner secrets |
| `ExecutionReader` (`ops.ExecutionFact`) | offers | `ops` — latest-execution-per-kind correlation (`execution.OpsExecutionReader`, P6-retired the app bridge) |
| `ValidationContext` · `ValidationCredentials` | offers | the S2S runner callbacks (`/internal/v1`, via the internalServer — not the public edge) |

## Owns
- The **executions** write-API (admit/finish/reevaluate through the funnel) and **workflow_runs**; the
  Temporal `Runtime`, `Signaler`, and the dev/task/validation workflows.
- The **milestone run** store: a run row per (org, project, milestone) — origin, small state, terminal
  reason, budget counters, validation verdict — and one **cycle record per dispatch** under it (kind,
  attempts, Job ref, branch, PR number, merge SHA). The milestone **number** is the key; the title is kept
  only as the `v<N>` tag a `?tag=` query resolves through. Loop position is read from the latest cycle, and
  per-component build/deploy status is derived from OpenChoreo on read — neither is stored.
- **Persistence**: every gorm in this domain sits at the ROOT (the fence `TestGormFencedToDomainRepository`
  draws), as single write-authority — `repository_execution.go` · `repository_workflow_run.go` ·
  `repository_coding_agent_log.go` · `repository_run.go` · `repository_cycle.go` over the `execution.go` /
  `workflow_run.go` / `coding_agent_log.go` / `milestone_run.go` / `run_cycle.go` entities. Their tables are
  `executions` · `workflow_runs` · `coding_agent_logs` · `milestone_runs` · `run_cycles`.

## Invariants — don't break
- **`task ⊥ execution`.** The GitHub-facing half (`task`) and the platform-owned half (`execution`) are
  peer sub-packages that never import each other; `task` reaches the funnel only through the root
  `Dispatcher` port. `TestTaskExecutionSplit` + `slice ⊥ sibling` both enforce it.
- **One active spec run per project.** At most one non-terminal (`waiting`/`running`) `spec-build` milestone
  run exists per (org, project) — a partial unique index (`ux_milestone_runs_spec_active`, created by the
  `milestone_runs` migration; AutoMigrate cannot express one) that admission hits with
  `INSERT … ON CONFLICT DO NOTHING`, so the invariant holds under concurrency and not merely under the
  endpoint's pre-check. `incident-adoption` runs sit deliberately outside the index and execute
  concurrently on their own milestones.
- **Settled rows are never resurrected.** Every run and cycle mutator is a guarded update — fenced on the
  run not being terminal, or the cycle not being closed — and returns `(nil, nil)` when it changes no row,
  so a duplicate webhook or signal is a no-op rather than a rewrite of a recorded outcome.
- **One funnel door.** Every Execution (coding, build, validation, provisioning) is admitted/finished/
  reevaluated through `execution`'s funnel — the single place org-fencing, dedup, and dep-gating live.
- **The kernel names no feature.** The root holds only types/ports/Temporal infra; it never imports a
  sub-package (`root ⊥ slice`), and the domain never imports `internal/feature/*`.
- **`Signaler` stays a nil-safe concrete type**, not an interface — its nil-safety (no-op when Temporal is
  unavailable) is load-bearing for tests and degraded runs.
- **The task-log stream is one connection, no server cursor.** `stream-task-log`
  (`GET .../tasks/{issueNumber}/log`, `text/event-stream`) carries a Task's whole live state — `task` /
  `execution` / `line` / `done` frames, the frame kind in a `type` field inside the `data:` payload so it
  rides the shared agent-stream parser. The server buffers no history and keeps no cursor; the client
  dedups and a reconnect re-derives. It settles/closes only on `deployed` — a transient `abandoned` during
  the merge→build handoff keeps it open.
- **Validation is the last devflow phase and never builds.** It starts only after every coding task
  succeeds and an OpenChoreo Ready-deployment check passes, then spawns `ValidationFlowWorkflow` → one
  `ValidationTaskWorkflow` lane per method (e2e only today). A validation Task is project-scoped, swaps to
  the Playwright runner image (`VALIDATION_RUNNER_IMAGE`), and its merged PR spawns **no build** — a
  completed validation derives to `deployed`/"Done". The acceptance oracle
  `specs/validation/validation-criteria.json` is read-only input authored in the design phase (spec domain).
- **Task LIST reads exclude the validation task** (`task/reads.go` `ListByTag`, the read-model boundary):
  it is a phase of the run, not an implementation task, so the console tasks page, the build's per-version
  task list, and the devflow planned-task graph never show it. Its state rides `deploy.validation`
  (+ `validationIssue`, `validationUrl`); `get-task` and `stream-task-log` still serve it by issue number —
  the pair the console validation log page consumes. `TaskView.prUrl` (recovered from the succeeded coding
  Execution's `pr#N` reason, no live PR query) links each Task's PR.
- Platform-wide rules (tenant gate, secrets fence, persistence-in-domain) → [../../README.md](../../README.md).
