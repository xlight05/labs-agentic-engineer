# AGENTS.md — services/aep-api

Go BFF: contract-first HTTP edge, GitHub webhooks, and the reactive execution funnel.
Vertical slices in `internal/feature/<name>`; wiring in `internal/app`; shared kernels in
`internal/platform` + `repositories/`. Where this is heading (proposal, not built):
`docs/design/aep-api-target-architecture.md`.

## Commands

| Command | Does |
|---|---|
| `make test` | `go test -short ./...` — unit + component tiers, no Docker |
| `make test-db` | adds the dbtest tier (real Postgres via testcontainers) |
| `make gen-api` | regenerate types/server after **any** contract edit (CI fails on drift) |

## Rules of thumb

**The edge**
- Org comes from the **verified token only**. Never add an org path/query/body param — its
  absence is what makes a cross-org request unrepresentable. Ops are org-gated by default;
  un-gating one means adding to `tenantGateCarveOuts` (one map, reflection-pinned).
- The contract is the source of truth. Never hand-edit `internal/api/gen`, `igen`, or
  `models_gen.go` — edit `packages/contracts/api/v1`, then `make gen-api`.

**Shape**
- Declare a **narrow consumer port** in your feature; wire the adapter at the composition
  root. Don't import another feature to borrow a type — a feature→feature edge needs an
  `internal/arch` allowlist entry and a stated reason.
- **One adapter = no interface.** Add a port only when two adapters exist (prod + test counts).
- Constructors take a `Deps` struct, one shot (`genai.ServiceDeps` is the model). No
  `Set*`/`With*` after construction.
- Prefer a small interface over a lot of behaviour. If deleting a type would scatter its
  logic across callers, it earns its keep; if it just forwards, inline it.

**Data, time, async**
- `gorm` stays in `repositories/`. Features take a repository port, never `*gorm.DB`.
- No `time.Now()` in logic — take a `now func() time.Time` field (see `credentials.Validator`).
- Background loop = exported `Sweep(ctx) error` + a thin ticker wrapper
  (`genai.TurnSweeper` is the model). Test the Sweep; never the ticker.
- Detaching a goroutine? Leave a synchronous core a test can call directly.

**Tests**
- Substitute, don't mock, what runs locally: stores → `dbtest`, git → `gittest`/`workspacetest`.
  Don't write in-memory DB fakes.
- New HTTP surface → a `*_component_test.go` via `componenttest` (real handler, real gate in
  ENFORCE, milliseconds).
- If only tests call it, delete it. Green tests on unreachable code are worse than no tests.
- If a rule matters, pin it with a test that fails on drift (`internal/arch`), not a comment.

**Config**
- Fail fast on a required dependency. Don't `slog.Warn` what is actually a hard failure.
