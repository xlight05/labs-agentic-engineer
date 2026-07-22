# Prototype: parent/sub-issue graph with native dependencies

Asset for [wso2/labs-agentic-engineer#279](https://github.com/wso2/labs-agentic-engineer/issues/279)
(wayfinder map [#272](https://github.com/wso2/labs-agentic-engineer/issues/272)).
Prototyped 2026-07-22 against the live sandbox repo
[`xlight05/aep-issue-graph-sandbox`](https://github.com/xlight05/aep-issue-graph-sandbox).

## What was built

The planner's exact output shape, via the same REST calls the aep-api backend
would make ([`plan-graph.sh`](plan-graph.sh)):

- Parent [sandbox#1 "Orders: capture and list customer orders"](https://github.com/xlight05/aep-issue-graph-sandbox/issues/1)
  — labels `aep`, `aep:spec/v1`, planner-template prose body (no machine block, per
  [Issue anatomy #283](https://github.com/wso2/labs-agentic-engineer/issues/283)).
- 4 sub-issues in a diamond: S1 domain model → S2 endpoint, S3 webhook consumer → S4 console page
  (S2, S3 blocked by S1; S4 blocked by S2 and S3), label `aep`.
- A repo webhook subscribed to `issues`, `sub_issues`, `issue_dependencies`, pointed at a
  dead-drop URL — GitHub records every delivery's full payload retrievably via the
  hook-deliveries API, so no listener infrastructure is needed
  ([`capture-deliveries.sh`](capture-deliveries.sh)).

Then a simulated agent run ([`simulate-run.sh`](simulate-run.sh)): sub-issues closed in
dependency order with `state_reason=completed` (what the merged PR's `Resolves` list does),
reading back the signals between closes ([`readback/simulate-run.log`](readback/simulate-run.log)).
Finally the graph was re-staged to a mid-run state (S1 closed, S4 blocked) for UI review.

## Findings

**Creation cost.** Parent + 4 subs + 4 links + 4 edges = **13 content-generating calls**
(matches the research formula `1 + 2N + M`). With the recommended ≥1 s spacing the whole
plan lands in ~15 s. Sub-issue links and dependency edges both take the blocker's numeric
**database id**, not the issue number — the planner must capture ids at creation.

**One GraphQL call recovers the whole graph.** `Issue.subIssues` +
`issueDependenciesSummary` + `blockedBy` nest in a single query
([`readback/graphql-one-shot.json`](readback/graphql-one-shot.json)): parent state,
progress summary, every sub-issue's state, and every blocked-by edge with the blocker's
state. This is the cheap read for both the coding agent (via `gh api graphql`) and any
backend read model. REST needs 2 calls for the workable set (parent + sub-issues list;
each list item carries `issue_dependencies_summary`) but +1 call per sub-issue for edge
detail, and the REST issue object has **no `parent` field** (verified).

**"Unblocked" is a live, derived signal.** Closing S1 dropped S2/S3's
`issue_dependencies_summary.blocked_by` 1→0 instantly (`total_blocked_by` keeps the edge
history); reopening a blocker pushes the count back up. The summary is never stale —
`workable = state:open && blocked_by==0` is safe to derive server-side at any time.

**No auto-close, in either direction.** With all 4 subs closed the parent stayed `open`
at `completed: 4/4, percent_completed: 100`. The supervisor workflow must close the
parent itself — consistent with the
[Agent execution contract #280](https://github.com/wso2/labs-agentic-engineer/issues/280).

**Webhook payload capture is blocked on token scope** (status, not a finding): reading
hook deliveries requires `admin:repo_hook`, which the session token lacks
(`repo, workflow, gist, read:org`). Deliveries are retained by GitHub; `payloads/` lands
here after `gh auth refresh -h github.com -s admin:repo_hook` and a
`capture-deliveries.sh` drain.

## Reproduce

```sh
export SANDBOX_REPO=<owner>/<sandbox>
./plan-graph.sh                     # writes graph.env
./simulate-run.sh graph.env
HOOK_ID=<hook id> ./capture-deliveries.sh   # needs admin:repo_hook scope
```
