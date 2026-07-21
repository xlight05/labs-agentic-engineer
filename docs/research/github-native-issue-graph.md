# GitHub native issue graph: sub-issues & issue dependencies — API capabilities

Research for [wso2/labs-agentic-engineer#278](https://github.com/wso2/labs-agentic-engineer/issues/278).
Date: 2026-07-21.

Sources: primary only — docs.github.com REST/GraphQL/webhook reference, the GitHub
changelog (github.blog/changelog), GitHub's machine-readable API descriptions
([github/rest-api-description](https://github.com/github/rest-api-description) OpenAPI files per
version, [github/docs](https://github.com/github/docs) GraphQL schemas per GHES version) — plus
harmless read-only `gh api` probes against the live specimen graph in
wso2/labs-agentic-engineer (#272 parent, #273–#288 sub-issues with blocked-by edges) and live
GraphQL schema introspection against api.github.com. Empirical findings are marked as such.

## Summary

| Capability | Limit / shape | GA (github.com) | GHES availability |
|---|---|---|---|
| Sub-issues (parent/child hierarchy) | **100 sub-issues per parent**, **8 nesting levels**; sub-issue must have the **same repository owner** as parent (cross-repo within an owner/org OK, cross-owner no) | GA **2025-04-09** (REST API since 2024-12-12) | UI + **GraphQL from 3.18** (schema already in 3.17); **REST endpoints and `sub_issues` webhook absent through 3.21** |
| Sub-issues REST | 5 endpoints: list / add / remove / reprioritize / get-parent; body params use the issue **id** (not number) | GA | **Not in GHES ≤ 3.21** |
| Sub-issues GraphQL | `addSubIssue` / `removeSubIssue` / `reprioritizeSubIssue`; `Issue.parent`, `Issue.subIssues`, `Issue.subIssuesSummary` | GA | GHES 3.17+ (schema), feature announced with 3.18 |
| Issue dependencies (blocked_by / blocking) | **50 issues per relationship type**; **advisory-only** — nothing is enforced | GA **2025-08-21** | **GHES 3.19+** (REST + GraphQL + webhooks) |
| Dependencies REST | 4 endpoints; **`blocked_by` is the only writable side** (add/remove); `blocking` is read-only | GA | 3.19+ |
| Webhooks | `sub_issues` event: 4 actions; `issue_dependencies` event: 4 actions; `issues` event has **no** parent/dependency actions | GA | `issue_dependencies` 3.19+; `sub_issues` absent ≤ 3.21 |
| Issue-object fields | `sub_issues_summary{total,completed,percent_completed}` and `issue_dependencies_summary{blocked_by,total_blocked_by,blocking,total_blocking}` on the REST issue; **no `parent` field** on the REST issue object | live | summary schemas present in GHES OpenAPI 3.17+ |
| Parent auto-close | **None.** Closing all sub-issues does not close the parent; closing a parent does not close sub-issues. `Closes #N` closes only issue N | — | — |
| gh CLI | full flag support in **v2.94.0+** (2026-06-10); any version via `gh api` | — | — |
| go-github | sub-issues since **v73.0.0** (2025-06-24); dependencies since **v89.0.0** (2026-07-06) | — | — |
| Rate limits (plan-time creation) | parent + N subs + M edges = **1 + 2N + M content-generating calls**; secondary caps: **80/min and 500/hr** content-generating, 900 pts/min, serial + ≥1 s spacing recommended | — | — |

---

## 1. Sub-issues: endpoints, limits, cross-repo, GA status

### REST endpoints

Reference: [REST — sub-issues](https://docs.github.com/en/rest/issues/sub-issues?apiVersion=2022-11-28)

| Method + path | Purpose |
|---|---|
| `GET /repos/{owner}/{repo}/issues/{issue_number}/parent` | Get the parent of a sub-issue |
| `GET /repos/{owner}/{repo}/issues/{issue_number}/sub_issues` | List sub-issues (paginated, `per_page` max 100 → one page covers the 100 cap) |
| `POST /repos/{owner}/{repo}/issues/{issue_number}/sub_issues` | Add a sub-issue; body `{sub_issue_id, replace_parent?}` |
| `DELETE /repos/{owner}/{repo}/issues/{issue_number}/sub_issue` | Remove a sub-issue; body `{sub_issue_id}` (note singular `sub_issue` path) |
| `PATCH /repos/{owner}/{repo}/issues/{issue_number}/sub_issues/priority` | Reorder; body `{sub_issue_id, after_id | before_id}` (exactly one of after/before) |

Gotcha: `sub_issue_id` is the issue's global numeric **`id`** (e.g. `4939159074` for #272), not
its issue number. The create-issue response returns `id`; keep it when building a graph.
(Verified empirically: `gh api .../issues/274/parent` returns `{number: 272, id: 4939159074}`.)

### Limits

- "You can add up to 100 sub-issues per parent issue" and "create up to eight levels of nested
  sub-issues" — [Adding sub-issues (docs)](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/adding-sub-issues).

### Cross-repo

- "The sub-issue must belong to the same repository owner as the parent issue" — add sub-issue
  operation description, [REST — sub-issues](https://docs.github.com/en/rest/issues/sub-issues?apiVersion=2022-11-28).
  So: cross-repository parent/child edges are allowed **within the same owner/org**; cross-owner is not.

### GraphQL

Verified live via schema introspection against api.github.com (2026-07-21); documented in the
[GraphQL mutations reference](https://docs.github.com/en/graphql/reference/mutations).

- Mutations: `addSubIssue(input: {issueId, subIssueId, subIssueUrl, replaceParent})` (either
  `subIssueId` or `subIssueUrl`), `removeSubIssue(input: {issueId, subIssueId})`,
  `reprioritizeSubIssue(input: {issueId, subIssueId, afterId | beforeId})`.
- `Issue` fields: `parent`, `subIssues(first: …)` connection,
  `subIssuesSummary { completed, percentCompleted, total }`.

### GA status

- Public beta [2024-10-01](https://github.blog/changelog/2024-10-01-evolving-github-issues-public-beta/),
  REST API added [2024-12-12](https://github.blog/changelog/2024-12-12-github-issues-projects-close-issue-as-a-duplicate-rest-api-for-sub-issues-and-more/),
  public preview [2025-01-12](https://github.blog/changelog/2025-01-12-evolving-github-issues-public-preview/),
  **GA 2025-04-09** together with issue types and advanced search
  ([changelog](https://github.blog/changelog/2025-04-09-evolving-github-issues-and-projects/)).
  No preview headers required anywhere.

## 2. Issue dependencies (blocked_by / blocking)

### REST endpoints

Reference: [REST — issue dependencies](https://docs.github.com/en/rest/issues/issue-dependencies?apiVersion=2022-11-28)

| Method + path | Purpose |
|---|---|
| `GET /repos/{owner}/{repo}/issues/{issue_number}/dependencies/blocked_by` | List issues blocking this issue |
| `POST /repos/{owner}/{repo}/issues/{issue_number}/dependencies/blocked_by` | Add a blocker; body `{issue_id}` (global issue **id**) |
| `DELETE /repos/{owner}/{repo}/issues/{issue_number}/dependencies/blocked_by/{issue_id}` | Remove a blocker |
| `GET /repos/{owner}/{repo}/issues/{issue_number}/dependencies/blocking` | List issues this issue blocks (read-only) |

The **blocked side is the only writable side** in REST; to say "A blocks B" you POST A's id to
B's `blocked_by`. (gh CLI's `--add-blocking` simply inverts this for you.) GraphQL mirrors this:
only `addBlockedBy(input: {issueId, blockingIssueId})` and `removeBlockedBy` exist (verified via
introspection); reads are `Issue.blockedBy` and `Issue.blocking` connections.

### Limits & GA

- **"You can link up to 50 issues for each relationship type."** GA **2025-08-21**:
  "Issue dependencies are fully supported in the API and webhooks" —
  [changelog: Dependencies on issues](https://github.blog/changelog/2025-08-21-dependencies-on-issues/).

### Advisory-only — confirmed

- Nothing in [Creating issue dependencies (docs)](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/creating-issue-dependencies)
  or the GA changelog describes any enforcement. The documented effect is purely visual:
  "Blocked issues are marked with a 'Blocked' icon on your project boards or repository's Issues
  page, so you can easily identify bottlenecks."
- Dependencies appear nowhere in branch protection / rulesets docs — they cannot block a PR merge.
- Empirical: wso2/labs-agentic-engineer#278 was closed (as part of resolving this ticket) while
  still marked as **blocking two open issues** — GitHub raised no error and required no override.
  Blocked issues can likewise be closed at any time; the relationship is informational.

### Cross-repo

- Not explicitly pinned down in the docs. Structural evidence says cross-repo edges are modeled:
  the REST body takes a bare global `issue_id`, the webhook payload carries a
  `blocking_issue_repo` object, and GraphQL `blockedBy` nodes expose `repository`
  ([webhook reference](https://docs.github.com/en/webhooks/webhook-events-and-payloads#issue_dependencies)).
  GitHub staff in the [public-preview feedback discussion](https://github.com/orgs/community/discussions/165749)
  describe picking issues from other repositories in the dependency dialog. **Treat cross-org
  edges as unverified** — verify empirically before designing for them.

## 3. Webhook events

Reference: [Webhook events and payloads](https://docs.github.com/en/webhooks/webhook-events-and-payloads).

### `sub_issues` event ([#sub_issues](https://docs.github.com/en/webhooks/webhook-events-and-payloads#sub_issues))

- Actions: `parent_issue_added`, `parent_issue_removed`, `sub_issue_added`, `sub_issue_removed`.
- Payload keys: `parent_issue_id`, `parent_issue` (full issue object), `parent_issue_repo`
  (repository object), `sub_issue_id`, `sub_issue` (full issue object).

### `issue_dependencies` event ([#issue_dependencies](https://docs.github.com/en/webhooks/webhook-events-and-payloads#issue_dependencies))

- Actions: `blocked_by_added`, `blocked_by_removed`, `blocking_added`, `blocking_removed`.
- Payload keys: `blocked_issue_id`, `blocked_issue`, `blocking_issue_id`, `blocking_issue`,
  `blocking_issue_repo`.

### `issues` event

- Action list: `assigned, closed, deleted, demilestoned, edited, field_added, field_removed,
  labeled, locked, milestoned, opened, pinned, reopened, transferred, typed, unassigned,
  unlabeled, unlocked, unpinned, untyped`.
- There is **no** `parented`/`unparented`/`blocked` action on `issues` — hierarchy and dependency
  changes arrive **only** via the two dedicated events above. To derive "became unblocked"
  server-side, subscribe to `issue_dependencies` (removals) **plus** `issues.closed` (a blocker
  closing does not emit an `issue_dependencies` event; the docs list no such action).
- The docs do not state whether one dependency add produces both a `blocked_by_added` and a
  `blocking_added` delivery to the same hook; verify with a live hook when wiring consumers.

## 4. Issue-object fields for deriving "unblocked" server-side

Verified empirically on the specimen (GET `/repos/wso2/labs-agentic-engineer/issues/{n}`):

```json
// #272 (parent):
"sub_issues_summary": { "total": 16, "completed": 5, "percent_completed": 31 }
// #288 (leaf blocked by 6):
"issue_dependencies_summary": { "blocked_by": 6, "blocking": 0, "total_blocked_by": 6, "total_blocking": 0 }
```

- Per the GraphQL field descriptions (live introspection), the `total*` variants are the
  "Total count … **(open and closed)**", while plain `blockedBy`/`blocking` count the rest —
  i.e. **`blocked_by` counts open blockers only**. So an issue is workable iff
  `state == "open" && issue_dependencies_summary.blocked_by == 0`. One GET per issue, or batch
  via GraphQL (`issues(first:100) { nodes { number issueDependenciesSummary { blockedBy } } }`).
- The REST issue object has **no `parent` field** (verified: `has("parent")` is false on GET).
  Parent lookup requires `GET …/issues/{n}/parent` (one call per issue) or GraphQL
  `Issue.parent` (batchable).
- Equivalent GraphQL summaries: `subIssuesSummary { completed, percentCompleted, total }`,
  `issueDependenciesSummary { blockedBy, totalBlockedBy, blocking, totalBlocking }`.

## 5. Client support: gh CLI, go-github, GraphQL clients

- **gh CLI ≥ v2.94.0** ([changelog 2026-06-10](https://github.blog/changelog/2026-06-10-manage-sub-issues-types-and-dependencies-from-github-cli/)):
  `gh issue create/edit` gained `--type`, `--parent`, `--set-parent`, `--remove-parent`,
  `--blocked-by`, `--blocking`, `--add-blocked-by`, `--add-blocking`, `--remove-blocked-by`,
  `--remove-blocking`; `gh issue view/list` expose parent, sub-issue, type, and dependency data
  as JSON fields. Older gh (verified locally on 2.86.0: none of these flags) can still do
  everything via `gh api` against the REST/GraphQL endpoints above.
- **go-github**: sub-issues API since **v73.0.0** (2025-06-24; `github/sub_issue.go`, added by
  [PR #3580](https://github.com/google/go-github/pull/3580), commit 2025-05-26 — verified absent
  at v72.0.0, present at v73.0.0 via tag probes). Issue-dependencies API since **v89.0.0**
  (2026-07-06, the latest release at research time; `github/issues_dependencies.go`, added by
  [PR #4130](https://github.com/google/go-github/pull/4130) — verified absent at v88.0.0).
  Both `SubIssuesSummary` and `IssueDependenciesSummary` structs exist on the Issue type.
- **GraphQL clients** (shurcooL/githubv4, octokit/graphql.js, `gh api graphql`): schema-agnostic —
  all five mutations and the Issue fields are usable today; no client-release dependency.

## 6. GHES availability

Verified against GitHub's own versioned artifacts (docs pages per `enterprise-server@` version,
OpenAPI descriptions in [github/rest-api-description](https://github.com/github/rest-api-description),
GraphQL schemas in [github/docs](https://github.com/github/docs) `src/graphql/data/`):

| Surface | 3.17 | 3.18 | 3.19 | 3.20 | 3.21 |
|---|---|---|---|---|---|
| Sub-issues UI + user docs (100/8 limits) | – | ✅ | ✅ | ✅ | ✅ |
| Sub-issues GraphQL (`addSubIssue`, `subIssuesSummary`) | ✅ (schema) | ✅ | ✅ | ✅ | ✅ |
| Sub-issues REST endpoints | ❌ | ❌ | ❌ | ❌ | ❌ |
| `sub_issues` webhook | ❌ | ❌ | ❌ | ❌ | ❌ |
| Dependencies REST + `addBlockedBy` GraphQL + `issue_dependencies` webhook | ❌ | ❌ | ✅ | ✅ | ✅ |
| `sub_issues_summary` / `issue_dependencies_summary` schemas in OpenAPI | ✅ | ✅ | ✅ | ✅ | ✅ |

- Sub-issues (with issue types + advanced search) shipped in **GHES 3.18**
  ([GA changelog 2025-10-14](https://github.blog/changelog/2025-10-14-github-enterprise-server-3-18-is-now-generally-available/));
  the [adding-sub-issues docs page](https://docs.github.com/en/enterprise-server@3.18/issues/tracking-your-work-with-issues/using-issues/adding-sub-issues)
  exists from 3.18 (404 at 3.17) with the same 100/8 limits.
- **Key caveat for a GHES-portable design**: through **GHES 3.21** (GA
  [2026-06-11](https://github.blog/changelog/2026-06-11-github-enterprise-server-3-21-is-now-generally-available/), newest at research time)
  the GHES OpenAPI contains **no `/sub_issues` REST paths and no `sub_issues` webhook** —
  on GHES, sub-issue automation is **GraphQL-only**, and there is no push signal for hierarchy
  changes. (Grep of `ghes-3.17`…`ghes-3.21` OpenAPI: 0 hits for `{issue_number}/sub_issues`,
  `{issue_number}/parent`, `webhook-sub-issues-*`; dotcom has all of them.)
- Issue dependencies are complete (REST + GraphQL + webhooks) from **GHES 3.19**
  ([REST docs exist at @3.19](https://docs.github.com/en/enterprise-server@3.19/rest/issues/issue-dependencies), 404 at @3.18;
  `issue-dependencies-*` webhook schemas present in the 3.19+ OpenAPI).

## 7. Auto-close semantics & "Closes #N" interplay

- **No auto-close in either direction.** Neither the
  [sub-issues docs](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/adding-sub-issues)
  nor any changelog entry documents closing a parent when all sub-issues close (or closing
  sub-issues when the parent closes). Sub-issue completion only drives the progress summary
  (`sub_issues_summary` / the UI progress bar). Empirical: #272 sits open at 5/16 completed.
  If parent auto-close is wanted, it must be built (e.g. on `issues.closed` + a
  `sub_issues_summary` check) — on GHES, by polling GraphQL, since the `sub_issues` webhook
  doesn't exist there.
- **`Closes #N` is unchanged**: "When you merge a linked pull request into the default branch of
  a repository, its linked issue is automatically closed" — and the
  [linking docs](https://docs.github.com/en/issues/tracking-your-work-with-issues/linking-a-pull-request-to-an-issue)
  contain **zero** mention of sub-issues, parents, or dependencies. A closing keyword closes
  exactly the referenced issue: if it's a sub-issue, the parent just gains a completed count;
  if it's marked blocked, it closes anyway (advisory-only, §2).

## 8. Rate-limit implications of creating parent + N subs + M edges

Sources: [Rate limits for the REST API](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api),
[Best practices for using the REST API](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api).

- Primary: 5,000 req/hr per authenticated user (PAT); GitHub App installations min 5,000/hr,
  15,000/hr on Enterprise Cloud orgs; Actions `GITHUB_TOKEN` 1,000 req/hr/repo.
- Secondary (the binding ones for graph creation — the sub-issue and dependency POST docs both
  carry the "Creating content too quickly … may result in secondary rate limiting" warning):
  - ≤ **80 content-generating requests/min** and ≤ **500 content-generating requests/hr**;
  - ≤ 900 points/min for REST (writes cost 5 points → ≤ 180 writes/min on points alone);
  - ≤ 100 concurrent requests; best practice: **serial requests, ≥ 1 s between mutative calls**,
    honor `retry-after` on 403/429.
- Cost of a plan: creating the graph is `(1 + N)` issue creations + `N` add-sub-issue POSTs +
  `M` add-blocked-by POSTs = **`1 + 2N + M` content-generating calls** (edges cannot be attached
  at creation time via REST; gh CLI's create-time flags issue the same follow-up calls).
  - Specimen-sized plan (N=16, M=17): 50 calls ≈ **50 s** at the recommended 1 req/s — under the
    80/min cap even unpaced, trivial against 500/hr.
  - Worst case per parent (N=100 max, M up to 50/issue): the **500/hr content-generating cap** is
    the real ceiling — a 100-sub plan with dense edges (e.g. M≈150 → 351 calls) burns most of an
    hour's budget; batch plans across hours or an App identity, always serially.
- GraphQL mutations don't bypass this: secondary limits (points, content-generation) apply across
  the API; there is no batch mutation for issue creation.

---

## Appendix: read-only probes used (reproducible)

```bash
# Issue-object fields (summaries present; no `parent` key):
gh api repos/wso2/labs-agentic-engineer/issues/272 --jq '{sub_issues_summary, issue_dependencies_summary, has_parent: has("parent")}'

# Hierarchy + edges of the live specimen:
gh api repos/wso2/labs-agentic-engineer/issues/272/sub_issues --paginate --jq '.[].number'   # 273..288
gh api repos/wso2/labs-agentic-engineer/issues/274/parent --jq '{number, id}'               # -> 272
gh api repos/wso2/labs-agentic-engineer/issues/288/dependencies/blocked_by --jq '.[].number' # 279 282 284 285 286 287

# Live GraphQL schema introspection (mutations + Issue fields + summary-type descriptions):
gh api graphql -f query='{ __type(name:"Mutation"){ fields{ name } } }' \
  --jq '.data.__type.fields[].name' | grep -iE 'subissue|blocked'
gh api graphql -f query='{ __type(name:"IssueDependenciesSummary"){ fields{ name description } } }'

# GHES surface checks (OpenAPI + GraphQL schema artifacts):
curl -sL https://raw.githubusercontent.com/github/rest-api-description/main/descriptions/ghes-3.21/ghes-3.21.json \
  | grep -c '{issue_number}/sub_issues'        # 0  (dotcom: 2)
curl -sL https://raw.githubusercontent.com/github/docs/main/src/graphql/data/ghes-3.19/schema.docs-enterprise.graphql \
  | grep -cE 'addBlockedBy'                    # 1  (3.18: 0)

# go-github introduction points:
gh api 'repos/google/go-github/contents/github/sub_issue.go?ref=v73.0.0' --jq .name           # exists (v72: 404)
gh api 'repos/google/go-github/contents/github/issues_dependencies.go?ref=v89.0.0' --jq .name # exists (v88: 404)
```
