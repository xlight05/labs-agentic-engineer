# ADR-0011: Usage persists tokens + a write-time USD stamp; model rates live in the DB

**Status:** Accepted (2026-07-17, amended 2026-07-21, superseded-in-part
2026-07-22) · **Issues:** [#245](https://github.com/wso2/labs-agentic-engineer/issues/245),
[#291](https://github.com/wso2/labs-agentic-engineer/issues/291)

> **2026-07-22 (#291):** the read-time-derivation decision below was reversed.
> USD is now **stamped at capture time** and model rates live in a **DB table**,
> not service config. The original decision and its rationale are kept under
> [History](#history-245-read-time-derivation) because the trade-off it names is
> real and re-proposable; the section headers below describe the **current**
> (shipped) architecture.

## Context

Agent work runs through two runtimes with different cost reporting: the coding
runner's Claude Agent SDK returns per-run token usage *and* a ready-made
`total_cost_usd`; the spec/design agent (Vercel AI SDK) returns token counts
only. Cost surfaces must reconcile across both, and Anthropic per-token prices
change over time.

The #245 design priced tokens at **read time** from a single env-configured
model rate. Two flaws drove the reversal:

1. **Rate changes rewrite history.** Read-time derivation reprices *all*
   past usage whenever a rate is edited — a completed cycle silently shows
   dollar amounts that were never actually spent. Cost incurred must be
   immutable.
2. **Single-model env config doesn't scale.** Rates lived in service config for
   exactly one model; a model change meant config surgery, not data.

## Decision

Every usage record persists **raw token counts (input, output, cache read,
cache write), the model id, and a `cost_usd` stamp** computed at capture time.

- **Rates are data.** A `model_rates` table (`model_id` → the four per-MTok
  USD rates) is the pricing source, seeded by migration and ops-managed. It
  replaces the `MODEL_PRICING_MODEL_ID` / `MODEL_RATE_*_PER_MTOK` env config.
- **Stamp at write.** When a record's usage is captured, `aep-api` resolves its
  `model_id` against `model_rates` and writes `cost_usd` onto the same row. A
  later rate edit affects only subsequent captures — history never moves.
- **Two capture surfaces, one per phase source.** The spec/design phase stamps
  onto `agent_turns`. Both delivery phases — build and validation — stamp onto
  `run_cycles`, because a build session's cycle IS one agent run: after the
  issue-driven flip (ADR-0011 of `docs/decisions`, *the milestone is the unit of
  execution*) every token-burning dispatch is a cycle, and the phase a cycle's
  spend belongs to is read from its kind (`validation` → validation, everything
  else → build). The `executions` table also carries usage columns and is still
  summed, but the only kind it still mints is `provision`, which stands up
  OpenChoreo resources and runs no model.
- **`cost_usd` is nullable.** A row whose model has no rate, or that predates
  stamping, stores `null`; aggregates sum the stamps and surface `null` when
  nothing is stamped. No read-time pricing formula remains.
- Provider-computed cost figures (the SDK's `total_cost_usd`) may be logged as
  a cross-check but are never displayed or persisted as truth.

Write-authority for capture + stamping is the `aep-api` backend tracked in
[#299](https://github.com/wso2/labs-agentic-engineer/issues/299); the console
reads stamped totals via `GET /usage/projects`.

## Consequences

- **Historical cost is immutable.** Cost views are an invoice-like record of
  what was actually spent, not a current-prices lens.
- A rate correction fixes only *future* work; a genuinely mis-stamped row is
  fixed by an explicit re-stamp migration, not silently on the next read.
- **No backfill** (#291): rows captured before stamping keep tokens with a
  `null` cost, so a project's total sums only its stamped rows. Honest
  under-reporting over fabricated as-of-spend prices.
- A multi-model future is a data concern (more `model_rates` rows), not a
  config or code change.

## Rejected

- **Read-time derivation** — the #245 approach; reprices history on every rate
  change (see [History](#history-245-read-time-derivation)).
- **Backfill old rows at current rates** — fabricates dollar amounts that were
  never the price when the work ran; the same immutability argument that killed
  read-time derivation kills the backfill.
- **Rates in service config** — one model only; a price change is a redeploy,
  not a data edit.
- **Display provider cost where available** — two irreconcilable sources (SDK
  dollars vs catalog math) across the two runtimes.

## History (#245): read-time derivation

The original decision (Accepted 2026-07-17): persist **only** tokens + model
id, and derive USD at read time from configured per-token-class rates
(checked-in defaults, env-overridable), rates never leaving the server. Its
rationale — *"a rate fix retroactively corrects all displayed history; nothing
stale is frozen in the database"* — is exactly the property #291 rejected once
"nothing frozen" was recognized as "history silently changes." The token
counts remaining the stored truth is preserved by the new decision; only the
USD half moved from read-derived to write-stamped.
