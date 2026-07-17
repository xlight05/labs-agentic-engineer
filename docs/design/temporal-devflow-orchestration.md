# Temporal devflow orchestration

Status: shipped on `aep-rewrite`. Tracks issue
[#67](https://github.com/wso2/labs-agentic-engineer/issues/67).

Durable, resumable orchestration of the user development flow using
[Temporal](https://temporal.io). Replaces the derived-from-artifacts "where are
we" model with an explicit, crash-resumable workflow, and makes human approval
gates first-class `await` points. The Temporal worker runs **in-process inside
`aep-api`** — there is no separate orchestrator service.

## Why in-process (and not a standalone service)

Issue #67 sketches a standalone `services/orchestrator` + a shared
`packages/contracts/orchestration` module (the shape on the `rewrite` branch).
This implementation deliberately keeps the worker in `aep-api`:

- Activities are thin adapters over services already wired in `app.Build()`
  (the execution funnel, genai turns, the plan service, the issue service).
  In-process, they call those services directly — no new RPC surface.
- Watchers are already in-process goroutines assembled the same way; the worker
  is just one more `app.Watcher`.
- Multi-replica later is fine: Temporal load-balances the task queue across
  every replica's worker.

Trade-off: the worker restarts with `aep-api` deploys (Temporal replays, so
this is safe) and cannot scale independently of the API. Acceptable at this
scale; revisit by extracting the `devflow` package into its own `main` if the
worker ever needs independent scaling.

## The workflows

`services/aep-api/internal/feature/devflow/`

### DevFlowWorkflow (`workflow_dev.go`)

One run per build/version. Steps, each gate auto by default:

1. record the run in the lookup index
2. **create version tag** (idempotent — `SaveRequirements` returns the existing
   tag when unchanged)
3. **design gate** → generate design, unless one already exists for the
   requirements tag. Generation starts a genai `design-generate` turn and waits
   for a `design-turn-done` signal, with a status-poll fallback on timeout.
4. **plan gate** → `RunPlan` (drains the plan turn; the tap writes the GitHub
   issues) → read back the planned tasks
5. fail fast on a dependency cycle
6. **execute** — dependency-aware task fan-out (below)
7. **validate** — quality bar (every planned task succeeded, checked BEFORE the
   gate so a doomed run never waits for approval) → **validate gate** →
   OpenChoreo consistency check (`Validate`: every design component has a Ready
   deployment) → spawn ONE `ValidationFlowWorkflow` child and await its result
8. **complete gate** → done

### TaskFlowWorkflow (`workflow_task.go`)

One child per task:

1. record the run
2. **start-coding gate** → `DispatchCoding` (through the funnel: admission +
   gating + coding executor)
3. await `pr-opened` (success — the coding agent's completion IS the PR) or
   `job-status` failed
4. **merge-pr gate** — auto squash-merges; manual waits for an approve decision
   (an external human merge on GitHub also releases it)
5. await `pr-merged` webhook confirmation (`pr-rejected` fails the task)
6. await `build-status` (the build is spawned by the merge webhook via the
   funnel — the workflow does not dispatch it)
7. await `deploy-status`
8. report the outcome to the parent

### ValidationFlowWorkflow (`workflow_validation.go`)

The validating phase's orchestrator — one child per dev run, built for N
parallel validation lanes over ONE issue and ONE PR:

1. `ResolveValidationTask` (idempotent ensure + find of the project's
   `aep:validation` issue); 0 ⇒ return a `skipped` result, record no row
2. record the run (`kind=validation`, the issue number, parented to the DEV
   run) — this row makes the orchestrator the **issue's webhook-signal owner**
3. **start-coding gate** → per lane: `DispatchCoding` + spawn a
   `ValidationTaskWorkflow` child (parallel, `TERMINATE` close policy)
4. **signal pump** — routes the issue's signals to the lanes until every lane
   child returned: a `job-status` is forwarded to its lane by `ExecutionID`;
   `pr-opened` records the PR number and (today) completes the e2e lane, whose
   runner opens the validation PR at job end
5. any lane failed ⇒ the phase fails (failures are data, not workflow errors)
6. all lanes succeeded ⇒ **merge-pr gate** → merge the single validation PR
   (shared `runMergePhase`); merge is terminal — a validation issue spawns no
   post-merge build

### ValidationTaskWorkflow (`workflow_validation.go`)

One child per validation lane (e2e only today). Deliberately thin: it awaits
the terminal state the orchestrator forwards via `lane-status` (or a wait
timeout) and reports it back — the extension seam for lane-specific steps
(scenario judging loops, retries) later. Adding a lane = another entry in the
orchestrator's lane slice; deferred platform work for a second lane: a
job-succeeded watcher signal (success == PR today), lane-qualified funnel
admission (`TryAdmit` is one-active-execution-per-issue), and a finalize step
combining lane branches + reports into the single PR.

### Dependency-aware scheduling

`depgraph.go` + `scheduleTasks` in `workflow_dev.go`. Cycle detection is the
funnel's DFS, lifted. The scheduler is deterministic (iterates the stable task
slice, never a map): a task starts once all its dependencies have succeeded;
independent tasks run as parallel child workflows; a task whose dependency
failed is skipped (`skipped-dep-failed`). Children use the `TERMINATE`
parent-close policy so canceling a dev run tears down its tasks.

## Events → signals (watchers/webhooks kept)

The existing watchers and webhook handlers were **not** replaced. They translate
their events into Temporal signals via a nil-safe `Signaler` (`signaler.go`):

| Source | Signal |
|---|---|
| `execution.Events` (PR webhooks) | `pr-opened`, `pr-merged`, `pr-rejected` |
| `codingagent.ExecWatcher` | `build-status`, `deploy-status` (keyed by the build row's issue) |
| `codingagent.JobWatcher` | `job-status` (coding-job failure) |
| `genai` finish hook | `design-turn-done` |
| `/devflows/.../gates/{gate}` API | `gate-decision` |
| `ValidationFlowWorkflow` (internal) | `lane-status` → its lane children |

An issue's signals go to whichever running row `RunningTaskByIssue` resolves —
a coding task's `kind=task` row, or the validation orchestrator's
`kind=validation` row for the project's validation issue.

Best-effort: no `workflow_runs` row (the old-console flow, or Temporal down) is a
debug-log no-op, so a webhook handler never fails because a workflow could not be
signaled. This is what keeps the **old console flow working unchanged** — the new
path is purely additive.

## Human-in-the-loop gates

`gates.go`. `GateConfig{ Auto map[string]bool, ApprovalTimeoutSeconds }` — a gate
absent from `Auto` runs auto (the default); `Auto[name]=false` pauses until a
`gate-decision` signal (or the timeout, treated as a reject). The paused gate is
visible as `PendingGate` in the status query. Gate names: dev `design`, `plan`,
`validate`, `complete`; task `start-coding`, `merge-pr`, `retry-build`.

## IDs, lookup, and status

- Dev workflow ID: `devflow-<org>-<project>-<tag>` (the tag makes it unique per
  build; a completed version re-runs under the same id via `AllowDuplicate`)
- Task workflow ID: `taskflow-<org>-<project>-<tag>-<issueNumber>`
- Validation orchestrator ID: `validationflow-<org>-<project>-<tag>`; lane
  child ID: `valtask-<org>-<project>-<tag>-<lane>-<issueNumber>`
- **Lookup**: a Postgres `workflow_runs` table (`models.DevflowRun`, partial
  unique index `(repo, issue_number) WHERE kind='task' AND status='running'`) is
  the signaler's point-lookup index and the list endpoint's source. Temporal
  stays the source of truth; the table is a denormalized index written by the
  workflows' own activities (replay-safe). Chosen over Temporal Search
  Attributes to avoid the server-registration ops burden for what is a point
  lookup on an app that already runs on Postgres.
- **Status**: read live via `QueryWorkflow(status)` — `DevFlowStatus` /
  `TaskFlowStatus` carry the phase, tag, `PendingGate`, and per-task summaries.

## API

Code-first Huma (`devflow_huma.go`, registered in `internal/api/huma_register.go`).
Org is derived from the JWT; the project is the path slug.

| Method | Path | Purpose |
|---|---|---|
| POST | `/projects/{p}/devflows` | start (200; 409 if one running; 503 if Temporal down) |
| GET | `/projects/{p}/devflows` | list this project's runs |
| GET | `/projects/{p}/devflows/{workflowId}` | live status via `QueryWorkflow` |
| POST | `/projects/{p}/devflows/{workflowId}/gates/{gate}` | send a gate decision (dev, or a task child via `taskIssue`) |

## Graceful degradation

Temporal is enabled iff `TEMPORAL_HOSTPORT` is set. Unset ⇒ the worker watcher
is never registered, the runtime never dials, and the `/devflows` endpoints
answer `503 temporal_unavailable`. The dial happens in the worker watcher's
retry loop, never at `Build` time, so `aep-api` boots and serves everything else
even when the Temporal server is down.

## Local deployment

Temporal runs **in-cluster as a single-pod dev server** in the k3d dev stack:

- `deployments/scripts/setup-temporal.sh` applies one Deployment + Service
  running `temporal server start-dev` (temporalio/admin-tools image): in-memory
  store, frontend on `:7233`, Web UI on `:8233`, `default` namespace
  auto-registered. No Cassandra, no schema job, no PVC — fast and reliable on a
  resource-tight k3d already running OpenChoreo. Wired into `setup.sh`.
  (The temporalio Helm chart was evaluated first but bundles only a heavy,
  flaky multi-node Cassandra + schema jobs; the dev server is the right fit for
  a labs demo. Trade-off: workflow state is lost if the pod restarts.)
- `start.sh` port-forwards the frontend to `localhost:7233` and the Web UI to
  `localhost:8233` (mirroring the OpenBao bridge); `stop.sh` tears them down.
- `deployments/docker-compose.yml` gives `aep-api`
  `TEMPORAL_HOSTPORT=host.docker.internal:7233`.

## Testing

Workflows are testable in isolation with `go.temporal.io/sdk/testsuite` — no
Temporal server, DB, or aep-api needed (activities mocked, signals delivered via
`RegisterDelayedCallback`). Covered: happy paths, coding-fail, PR-rejected,
manual gate approve/reject/timeout, pending-gate query, design-skip, cycle
fast-fail, failed-dep-skips-dependent, the strict ID formats, and the
validation tree (skip-on-no-criteria, pump forwarding to a real lane child,
lane failure, merge rejection — `workflow_validation_test.go`).

## Key files

- Workflows/activities/gates/signals: `internal/feature/devflow/`
- Composition-root adapters: `internal/app/devflow_adapters.go`, `app.go`
- Signal hooks: `internal/feature/execution/events.go`,
  `internal/feature/codingagent/{exec_watcher,watcher}.go`,
  `internal/feature/genai/{genai_service,turn_runner}.go`
- Persistence: `models/workflow_run.go`,
  `internal/migrate/workflow_runs.go`,
  `repositories/workflow_run_repository.go`
- Old-console demo: `console-legacy/console/src/components/devflow/DevflowPanel.tsx`
