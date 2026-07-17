# aep-api — testability plan

**Status: steps 1–2 landed on `api-test`; the rest partly landed.** Measured
against `services/aep-api` @ branch `contract-frist-backend`, 2026-07-16. Every
factual claim below was verified by direct inspection of that code.

> **Paths below are PRE-migration.** This plan was written and executed against the
> layout as it then stood; the domain migration
> ([domain-oriented-architecture.md §19](./domain-oriented-architecture.md#19-phased-migration-plan))
> has since moved several of the packages it names — `internal/credentials` →
> `internal/platform/secrets`, `internal/database` → `internal/platform/database`
> (mechanism) + `internal/migrate` (the ordered list), `internal/api/apigen` →
> `internal/gen`. They are left as written rather than rewritten: this is a record
> of what was done, not a map of what is there now. That plan's steps 6 and 11 are
> absorbed into the domain phases (§19.2), not run separately.

## Goal

`app.go:17-22` promises *"a component test can import it and assemble the same real
handler with faked deps (the harness IS Build)."* That is false today:
`componenttest` builds the handler through `api.NewHandlerForTest`, and
`grep -rl 'app.Build('` returns `cmd/aep-api/main.go` alone. `Build`'s ~1,000 lines
— ~105 constructors, 7 anonymous closures, 14 config-gated degraded modes — are
exercised by nothing but a live process boot.

The plan: make that sentence true, then delete everything that only pretends to be
tested.

**Not a rewrite.** These stay verbatim and are out of scope: the contract-first edge
(`internal/api/gen` from `packages/contracts/api/v1`), the deny-by-default tenant
gate + its reflection-pinned carve-out list, `componenttest` and its `InboundAuth`
seam, `dbtest`/`gittest`, `execution.Funnel`, the hand-ordered migration slice
(order is load-bearing), `orgensure`'s fail-open behaviour, the webhook
payload-derived-org trust model, and `api.Deps` as one ~30-field struct (splitting
it per-surface is churn with no depth gain).

## The work — ordered; each step independently shippable and green

### 1. Go green + set the ratchets (no behaviour change)

- Fix the RED suite: `internal/feature/skills/flat_layout_test.go`
  (embedded-catalog count drift, 9→10).
- `internal/arch`: add a **gorm-import allowlist** frozen at today's importers
  (8 features + `internal/api`, `internal/app`, `internal/credentials`,
  `internal/database`, `repositories`, `dbtest`, `componenttest`). Stale entries
  fail, and the list may only shrink.
- Migration-step registration guard: a plain test pinning that every step file in
  `internal/database/migrations` registers into `run_all.go` — a count/convention
  check, **not an AST census**. (The registry is complete today — all 27 exported
  `Run*` are referenced — but this failure mode has shipped before; guard it.)
- `database.BaseModels()` as the single AutoMigrate source.

### 2. Split `app.Build` → `infra.Resolve` (impure) + `app.Assemble` (pure)

The centerpiece; everything else leans on it.

- `infra.Resolve(ctx, cfg) (Infra, error)`: a mechanical hoist of every boot side
  effect out of `Build` — the OpenBao app-key / client-secret / bot-identity loads
  with their hand-rolled timeouts (`app.go:260-313`), the dev seed, k8s in-cluster
  init, workspace-root fsck. One struct hides all boot I/O; errors on required
  infra, warns on optional.
- `app.Assemble(cfg, Infra) (*App, error)`: the remaining wiring, now pure — no
  network, no clock, no filesystem. Deterministic, milliseconds.
- `infra.Fake()`: zero-I/O bundle, created together with the first assembly test.
- Assembly tests call `app.Assemble(cfg, infra.Fake())` **directly**. No `apptest`
  wrapper package — extract a shared helper only if real boilerplate accumulates
  across several tests.
- `main.go` becomes `config.Load` → `infra.Resolve` → `app.Assemble` → serve.

The new assembly tier proves, in milliseconds: the graph assembles under each
config-gated mode, watcher registration counts, the cycle-closing setters ran, and
the degraded-mode matrix of step 3.

### 3. Fail-fast config + degradations as data

- Boot-fail on missing **required** config: `JWKSURL`, `TaskTokenSigningKey`.
  Today both soft-warn and the failure surfaces later at runtime.
- `App.Degradations()` — the assembled app reports which optional capabilities are
  off and why; one table-driven assembly test walks the 14 config-gated degraded
  modes (including the undocumented no-dispatch-path state).
- **No capability/Profile type system.** The `if cfg.X != ""` gates stay as they
  are — greppable two-liners. The assembly test tier, not an abstraction, is what
  makes the matrix enumerable.
- Delete the dead `buildStager != nil` guard and arms (`app.go:491-495,679,723`)
  — verified: `NewBuildCredentialsService` never returns nil.

### 4. Delete the dead code (each verified dead)

- `design.SaveAndProceed` (~220 LOC = 33% of `design_service.go`, plus 24 test call
  sites across 4 files): the only non-test reference is its own definition. Its
  tests are green against a codepath production cannot reach. Retarget the
  pure-function tests (`deriveEndUserAuth`, `attachAnnotatedSkills`); delete the
  rest. Deleting it also dissolves the design↔provisioning cycle.
- `secretmanagersvc` `Register`/`GetProvider` registry (63 LOC, zero production
  callers — `app.go` constructs the provider directly).
- `obs.RequestLogger` + `GetLogger` (`GetLogger` has zero callers ⇒ the output is
  discarded; it also re-reads the raw header instead of the ctx value
  `AddCorrelationID` just set). Keep `AddCorrelationID`; give `obs` its first tests.
- The exported `project.ProjectService` / `genai.GenAIService` interfaces (one impl
  each; the only substitution is `componenttest/smoke_test.go`'s
  `fakeProjectService`, which dies with them). `api.Deps` holds concrete pointers,
  as build/task/design already do.

### 5. Name the 7 in-Build closures

- Promote the 5 logic-bearing closures to named, tested types in their owning
  features; the 2 raw-GORM closures (e.g. `orgUUIDResolver`'s side-car query)
  become repository methods.
- Dedupe `repoFullNameLookup`/`repoNamer`; move the 4 rule-bearing inline adapters
  into the features they serve.

### 6. Kill the setter chains

- One-shot `New(Deps)` constructors; the model is the existing `genai.ServiceDeps`.
- `codingagent` first (worst: 15+ `With*` setters across `CodingExecutor`,
  `ExecWatcher`, `JobWatcher`, `Dispatcher`), then project/design.
- Arch-grep in `internal/arch` bans `.Set*`/`.With*` calls in `internal/app`, with
  one sanctioned, named cycle-closer — Go cannot express the remaining object-graph
  cycle in constructors; be honest about exactly one.

### 7. Watcher recovery + `Sweep()` convention

- A panicking watcher currently kills the process — `main.go:103` launches each
  with a bare `go w.Run(ctx)`. Fix: a small recover-and-log wrapper at the launch
  site. **No supervisor framework, no jitter/interval knobs** — add those only when
  a watcher demonstrably needs them.
- Convention: every watcher's tick body is an exported `Sweep(ctx) error`; tests
  call `Sweep` directly, never the loop. Five of the nine watchers already work
  this way; the dark ones get their first tests through it. The two that need
  out-of-process fakes (`JobWatcher` → cluster-gateway-proxy, devflow worker →
  Temporal) take their narrow ports from step 11 at that moment, not before.

### 8. One spawn helper for fire-and-forget work

- `async.Go(ctx, name, fn)` — detached, panic-recovered, logged. Prod adapter
  detaches; test adapter runs inline, deleting the hand-built signal-channel
  plumbing at the ~7 fire-and-forget sites and in their feature-fake tests.
- Signature stays minimal — no budget/options parameters until a real need exists.

### 9. Inject time where TTLs live

- A `now func() time.Time` **field** (not a Clock interface) on the types with TTL
  logic; zero value means `time.Now`.
- Delete the three real-sleep tests in favour of fixed-time assertions:
  `webhook/routing_key_test.go:221`, `webhook/secrets_test.go:83`,
  `webhook/refetch_limiter_test.go:58`.

### 10. One error envelope

- One `problem.Write(w, status, code, msg, details)` **helper function** — not an
  injected interface. `httptest`'s recorder already observes every body; no
  "recording adapter" is needed to assert rejection shapes.
- Rewire the off-envelope writers onto it: `dev.go`'s hand-rolled `http.Error`
  JSON and the bare-status paths on the webhook/connect surfaces.
- Add the component-tier rejection-shape table: every surface × every rejection
  class asserts the one envelope.

### 11. Opportunistic tail — each seam created *with* its first test, never ahead

- **GORM ratchet payoff**: migrate raw gorm usage feature-by-feature into
  `repositories/` (idp is worst with 10 chains; then organization, orgcreds,
  webhook, codingagent, component, runtimeconfig), shrinking the step-1 allowlist
  every PR. The test adapter for a repository is **the real repository over
  `dbtest`** — do not grow in-memory DB fakes; fold existing SQL-adjacent fake-repo
  tests into `dbtest` as they're touched.
- **Narrow ports + hand fakes** for: `clustergatewayproxy` (unlocks
  JobWatcher/GetProgress tests), k8s Job/Secret apply (an in-memory fake instead of
  holding the 30+-method controller-runtime interface raw at 3 sites), a Temporal
  dialer (unlocks devflow's zero tests; workflow *definitions* stay on the SDK
  testsuite, already green), and an `HttpDoer` on oauth/oidc (httptest server).
- **componenttest `Options` extension** (webhook / OrgGitHub / InternalDeps) →
  `OrgGitHubController`'s first tests ever (248 LOC, currently structurally
  unreachable) + build/artifacts component tests.
- **Thin live smoke** (~5 cases): real Thunder RS256 end-to-end, plus the
  non-org-scoped routes the component tier structurally cannot prove.

### 12. Repair the dangling doc references

Code comments across the service cite `docs/design/{aep-api-target-structure,
bff-component-testing,internal-s2s-api,shared-volume-clone-architecture}.md` — none
of which exist on this branch (dropped at `bf418a51`) — and `componenttest.go`'s
package comment still describes a "code-first Huma API" that is gone. Fix or delete
each reference. (`bff-component-testing.md` is recoverable via
`git show 8f9d2ef2:docs/design/bff-component-testing.md` if worth restoring.)

## Deliberately not doing

- **No DI framework** — wiring stays explicit Go inside `Assemble`.
- **No `Clock` interface** — a field (step 9).
- **No unit-of-work / `TxRunner` port** — exactly one manual transaction exists
  (`credential_connect.go`); pin its behaviour at the dbtest tier.
- **No unified `OrgBinder`** over the three auth gates — three genuinely different
  fence semantics; a union interface would be as complex as each implementation.
- **No config capability/Profile abstraction** — fail-fast + `Degradations()` data
  covers it (step 3).
- **No reflection lock over the internal exec-id switch** (`internal.go:97`) — it
  is 3 cases and fails **closed**: a new internal op without extraction 401s, the
  safe direction, caught on first integration. A table + reflection arch-lock is
  machinery a loud failure already covers.
- **No `problem.Writer` interface or recording adapter** — a helper function
  (step 10).
- **No `apptest` harness package up front** — call `Assemble` directly (step 2).
- **No in-memory DB fakes** — the DB seam stays internal to `repositories/`;
  `dbtest` (real PG17, template-clone, the real migrator, single-digit ms/test)
  *is* the substitution.
- **No event bus** for the design↔provisioning cycle — deleting dead
  `SaveAndProceed` dissolves it; if it ever revives, use the one sanctioned
  cycle-closer of step 6.
