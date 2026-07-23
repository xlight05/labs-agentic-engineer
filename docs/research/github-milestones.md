# Research: GitHub milestone API capabilities

Resolves [wso2/labs-agentic-engineer#313](https://github.com/wso2/labs-agentic-engineer/issues/313).
Verifies the API facts the milestone-keyed execution model ([#312](https://github.com/wso2/labs-agentic-engineer/issues/312)) leans on:
one milestone per spec version (title == tag `v<N>`), planner-created task issues assigned at creation, a dispatch
predicate of "zero open `aep:provision` issues in M AND ≥1 open task issue in M", webhook-driven transitions, and
`GET /tasks?tag=` implemented as milestone-membership filtering.

**Evidence classes** used per claim:

- **probe** — verified by live API calls on 2026-07-23 against the sandbox `xlight05/aep-issue-graph-sandbox`
  (github.com, `gh` 2.86.0). All probe artifacts (3 milestones, 4 issues, 1 PR, 1 branch) were labeled
  `research-probe`, closed, and deleted afterwards.
- **doc** — cited from a primary source (docs.github.com, GitHub's machine-readable webhook schemas, cli.github.com
  manual, go-github source).
- **inferred** — a conclusion the sources support but do not state outright; flagged as such.

---

## 1. Create + dedupe

- `POST /repos/{owner}/{repo}/milestones` takes `title` (required), `state`, `description`, `due_on`. Documented
  responses: 201, 404, and 422 "Validation failed, or the endpoint has been spammed". The docs do **not** document
  duplicate-title behavior. — https://docs.github.com/en/rest/issues/milestones#create-a-milestone (doc)
- Creating a milestone with an exact-duplicate title returns **422** with body
  `{"message":"Validation Failed","errors":[{"resource":"Milestone","code":"already_exists","field":"title"}]}`.
  So titles ARE unique per repo and create-then-catch is viable: catch 422, check `errors[0].code == "already_exists"`,
  then list-and-match by exact title to recover the number. — probe (undocumented; treat the error shape as stable-ish,
  it is GitHub's standard validation-error envelope per https://docs.github.com/en/rest/using-the-rest-api/troubleshooting-the-rest-api#invalid-fields) (probe)
- Uniqueness is **case-sensitive at create time**: with `probe-milestones-v901` existing, creating
  `PROBE-MILESTONES-V901` succeeded and produced a second milestone (number 2). — probe
- BUT case-twin titles are hazardous downstream: the REST issues-list milestone filter matches **case-insensitively
  by title** (see §4), and gh CLI name resolution is case-insensitive (see §7). The platform should treat titles as
  case-insensitively unique. With the `v<N>` scheme this is automatic. — probe + inferred

## 2. Title mutability

- `PATCH /repos/{owner}/{repo}/milestones/{milestone_number}` accepts `title` as an optional body parameter —
  milestone titles are freely renameable by anyone with push access (unlike git tags).
  — https://docs.github.com/en/rest/issues/milestones#update-a-milestone (doc)
- Renaming onto an already-taken exact title also 422s with `already_exists` — the uniqueness guard applies to
  renames too. — probe
- The stable key is the milestone **`number`**: it is the immutable REST path key (`GET/PATCH/DELETE
  /milestones/{milestone_number}`), it is embedded in `issue.milestone.number` on every issue read, and GraphQL looks
  milestones up by it (`Repository.milestone(number:)`, introspected description: "The number for the milestone to be
  returned."). `id`/`node_id` are likewise stable. **The run row must store the number (plus `node_id` if GraphQL is
  used), never the title.** — https://docs.github.com/en/rest/issues/milestones#get-a-milestone,
  https://docs.github.com/en/graphql/reference/objects#repository (doc)
- Renames are observable: the `milestone` webhook `edited` action carries `changes` with `title.from`
  (also `description.from`, `due_on.from`), so a title→number cache can be resynced on rename.
  — https://github.com/octokit/webhooks/blob/main/payload-schemas/api.github.com/milestone/edited.schema.json (doc)

## 3. Assignment

- `POST /repos/{owner}/{repo}/issues` sets the milestone at creation — **one call, no second write**. Docs on the
  `milestone` param: "The number of the milestone to associate this issue with. NOTE: Only users with push access can
  set the milestone for new issues. The milestone is silently dropped otherwise." Note the silent-drop failure mode
  for under-privileged tokens. — https://docs.github.com/en/rest/issues/issues#create-an-issue (doc, confirmed by probe)
- The param takes the **number only**: passing the milestone title string to issue-create 422s with
  `{"resource":"Issue","field":"milestone","code":"invalid"}`. — probe
- An issue **can** be assigned to a **closed** milestone via the API, both at creation (`POST /issues` with the closed
  milestone's number succeeded) and by update (`PATCH /issues/{n}` succeeded). — probe
  - UI: docs describe the milestone picker but are silent on closed-milestone selectability
    (https://docs.github.com/en/issues/using-labels-and-milestones-to-track-work/associating-milestones-with-issues-and-pull-requests);
    not verified in a browser. gh CLI **cannot** do it (see §7). — doc-absence, unverified for UI
- Closing a milestone has **no side effect on its issues**: after `PATCH state=closed`, the member issue stayed
  `open` and kept its milestone (`issue.milestone.state` merely reads `closed`). New issues could still be added.
  — probe (docs document no cascade either: https://docs.github.com/en/rest/issues/milestones#update-a-milestone)
- Deleting a milestone detaches its issues but does not touch them otherwise (member issue survived with
  `milestone: null`, state unchanged). Docs say only "Deletes a milestone", no cascade documented.
  — probe + https://docs.github.com/en/rest/issues/milestones#delete-a-milestone (doc)

## 4. Reads (dispatch predicate and `?tag=` filter)

- `GET /repos/{owner}/{repo}/issues?milestone=` — documented semantics: "If an integer is passed, it should refer to
  a milestone by its number field. If the string `*` is passed, issues with any milestone are accepted. If the string
  `none` is passed, issues without milestones are returned." A title string 422s (`field: milestone, code: invalid`),
  as does a nonexistent number. **Number only.** — https://docs.github.com/en/rest/issues/issues#list-repository-issues
  (doc, confirmed by probe)
- Combined `milestone=&labels=&state=` filtering in one call works (`milestone=1&labels=research-probe&state=open`
  returned exactly the labeled member issue). `labels` is AND-semantics ("A list of comma separated label names").
  — probe + https://docs.github.com/en/rest/issues/issues#list-repository-issues (doc)
- **GOTCHA (undocumented): the milestone filter resolves the number to its title and matches case-insensitively.**
  With case-twin milestones `probe-milestones-v901` (1) and `PROBE-MILESTONES-V901` (2), `?milestone=1` and
  `?milestone=2` returned the identical union of both milestones' issues. A distinctly-titled milestone filtered
  cleanly. Case-insensitive title uniqueness must be enforced platform-side (automatic under `v<N>`). — probe
- **GOTCHA: the milestone-filtered issues list omitted an open PR that was a member of the milestone**, checked
  repeatedly over ~10 minutes and with `state=all`, while the same PR appeared in the plain unfiltered issues list
  (`is_pr: true`). The general doc rule is "Issues endpoints may return both issues and pull requests in the
  response" (https://docs.github.com/en/rest/issues/issues) — the milestone-filter exclusion is undocumented
  behavior; do not rely on it either way. — probe
- **Milestone object counts include PRs**: a milestone whose only member was an open PR reported `open_issues: 1`;
  with one open issue + one open PR it reported `open_issues: 2`. The REST docs do not specify what the counts
  include. GraphQL `openIssueCount` behaved identically (2). **Never use `open_issues` as the dispatch-predicate
  counter** if PRs can ever be milestoned. — probe (docs silent:
  https://docs.github.com/en/rest/issues/milestones#get-a-milestone)
- GraphQL lookup: `Repository.milestone(number: Int!)` for direct fetch; there is **no exact lookup by title** —
  `Repository.milestones(query: String)` is a title **search** ("Filters milestones with a query on the title",
  introspected schema description) that matches case-insensitively and by substring (query with the exact
  lower-case title returned both case-twins), so exact-match needs a client-side `title ==` filter on the nodes.
  — https://docs.github.com/en/graphql/reference/objects#repository (doc) + probe
- **Cheapest single query for the dispatch predicate** (verified live; `Milestone.issues` is a pure-issue connection
  with `states`/`labels`/`filterBy` args, PRs live on a separate `Milestone.pullRequests` connection — introspected):

  ```graphql
  query($owner: String!, $repo: String!, $m: Int!) {
    repository(owner: $owner, name: $repo) {
      milestone(number: $m) {
        provision: issues(states: [OPEN], labels: ["aep:provision"], first: 1) { totalCount }
        allOpen:   issues(states: [OPEN], first: 1) { totalCount }
      }
    }
  }
  ```

  Dispatch iff `provision.totalCount == 0 && allOpen.totalCount > 0` (task count = `allOpen - provision` when
  `aep:provision` partitions the set). One call, counts came back exact (2/3 split matched ground truth), PRs
  excluded by construction. — probe + https://docs.github.com/en/graphql/reference/objects#milestone (doc)

## 5. Webhooks

- `milestone` event actions: `created`, `closed`, `opened`, `edited`, `deleted`. Payload embeds the **full milestone
  object** (required), plus `repository`/`sender`/`organization`/`installation`; `edited` additionally carries
  `changes` (`title.from`, `description.from`, `due_on.from`).
  — https://docs.github.com/en/webhooks/webhook-events-and-payloads#milestone +
  https://github.com/octokit/webhooks/tree/main/payload-schemas/api.github.com/milestone (doc)
- `issues` event action list includes `milestoned` and `demilestoned` (alongside `opened`, `closed`, `reopened`,
  `labeled`, `unlabeled`, `edited`, `deleted`, `transferred`, ...).
  — https://docs.github.com/en/webhooks/webhook-events-and-payloads#issues (doc)
- `issues.milestoned` payload (per GitHub's machine-readable webhook schema): required top-level
  **`milestone`** (the milestone that was added) AND required `issue`, whose embedded `issue.milestone` is required
  non-null. — https://github.com/octokit/webhooks/blob/main/payload-schemas/api.github.com/issues/milestoned.schema.json (doc)
- `issues.demilestoned` payload: required top-level **`milestone`** (the milestone that was removed) and `issue`
  with `issue.milestone` forced to `null`. So the removed-from milestone number is always recoverable from the
  top-level object even though the issue no longer references it.
  — https://github.com/octokit/webhooks/blob/main/payload-schemas/api.github.com/issues/demilestoned.schema.json (doc)
- Every `issues` event payload embeds the full issue ("The issue itself"), and the issue object carries its
  (nullable) `milestone` — so `issues.closed` / `issues.reopened` / `issues.labeled` / `issues.unlabeled` all tell
  the supervisor which milestone to re-evaluate without an extra read.
  — https://docs.github.com/en/webhooks/webhook-events-and-payloads#issues (doc) + probe (issue reads always
  include `milestone`)
- Signal set for the wait state (inferred): recompute the predicate for milestone M on
  `issues.{opened,closed,reopened,labeled,unlabeled,milestoned,demilestoned,deleted,transferred}` where the payload's
  issue/top-level milestone number == M, plus `milestone.{edited,deleted}` for key hygiene. — inferred

## 6. Limits & rate

- **No documented cap** on milestones per repo or issues per milestone. The repository-limits page enumerates size,
  push, diff, PR and commit-listing limits and never mentions milestones or issue counts.
  — https://docs.github.com/en/repositories/creating-and-managing-repositories/repository-limits (doc-absence)
- Secondary rate limits (exact wording): "no more than 80 content-generating requests per minute and no more than
  500 content-generating requests per hour"; "Content creation limits include actions taken on the GitHub web
  interface as well as via the REST API and GraphQL API"; "Some endpoints have lower content creation limits."
  Also: ≤100 concurrent requests, ≤900 REST points/min.
  — https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api#about-secondary-rate-limits (doc)
- The docs do not enumerate which endpoints are "content-generating"; milestone create is a content-creating POST of
  the same class as issue create, so budget it identically. — inferred (safe assumption)
- Plan cost holds at **`1 + N` content-generating calls** (1 milestone + N issues, milestone set at issue creation,
  §3 — no second write per issue). Fits the 80/min window for N ≤ 79; a 500/hr budget bounds ~499 issues/plan-hour.
  Serial issuance with ~1 s spacing recommended (same conclusion as the #278 research). — inferred from doc limits

## 7. Clients & availability

- **gh CLI**: `gh issue create --milestone <name>` — "Add the issue to a milestone by **name**"
  (https://cli.github.com/manual/gh_issue_create); `gh issue list --milestone` — "Filter by milestone **number or
  title**" (https://cli.github.com/manual/gh_issue_list); `gh issue edit --milestone <name>` likewise by name. (doc)
- gh gotchas (probed on gh 2.86.0): name resolution is **case-insensitive** (with case-twins it silently picked the
  other milestone) and **only sees open milestones** — `gh issue edit --milestone <closed-milestone-name>` fails with
  "'…' not found" even though the REST API accepts the assignment. — probe
- There is no native `gh milestone` command group (not in the manual's command list; community fills the gap with the
  `gh-milestone` extension). `gh api` covers all milestone CRUD — every probe in this doc used it.
  — https://cli.github.com/manual/ (doc-absence) + probe
- **go-github**: `IssuesService` has `ListMilestones`, `GetMilestone`, `CreateMilestone`, `EditMilestone`,
  `DeleteMilestone` (current master, `github/issues_milestones.go`); `IssueRequest.Milestone *int` (create/edit),
  `IssueListByRepoOptions.Milestone string` ("a milestone number, `none`, `*`"), plus a `RemoveMilestone` helper.
  Milestone support landed 2014-04-08 ("Add support for the milestone API") — effectively every tagged release ever.
  — https://github.com/google/go-github/blob/master/github/issues_milestones.go (doc/source)
- **GHES**: currently supported line is 3.17–3.21 (oldest = 3.17, EOL 2026-08-25;
  https://docs.github.com/en/enterprise-server@latest/admin/all-releases). Full milestones REST CRUD is documented on
  GHES 3.17 (https://docs.github.com/en/enterprise-server@3.17/rest/issues/milestones), as are the `milestone`
  webhook event (all five actions) and the `issues` event with `milestoned`/`demilestoned`
  (https://docs.github.com/en/enterprise-server@3.17/webhooks/webhook-events-and-payloads). Milestone docs exist even
  on long-EOL versions (e.g. 3.9/3.10) — the feature is ancient and universal. (doc)

---

## Design implications for #312

**Holds as designed:**

- Idempotent create: create-then-catch works — duplicate title is a deterministic 422 `already_exists`; recover the
  number by list-then-exact-match on title.
- One-call planner writes: `POST /issues` with `milestone: <number>` at creation; plan cost stays `1 + N`.
- Closed milestones accept issues via the API (create and update), and closing a milestone has zero side effects on
  its issues — the "close M when spec vN retires" lifecycle is safe.
- Webhook signal set is sufficient: `milestoned`/`demilestoned` both carry the milestone number at top level, and
  every `issues` payload embeds the full issue with its milestone — the supervisor can key every event to a run
  without extra reads.
- The dispatch predicate is one GraphQL call (aliased `Milestone.issues` counts, §4) — pure issue counts, PRs
  structurally excluded.

**Needs adjustment / hardening:**

- **Store the milestone number** (and `node_id`), never the title — titles are renameable, and renames should be
  absorbed by watching `milestone.edited` `changes.title.from`. (The design already intends this; confirmed correct.)
- **Do not build the predicate on REST `open_issues` counts** — they include PRs (probe-verified) and would break the
  moment a release PR is milestoned. Use the GraphQL `issues` connection (or REST list + labels) instead.
- **REST issues-list milestone filter is title-based and case-insensitive under the hood** (probe): case-twin titles
  cross-contaminate results, and the filter also silently omitted a member PR. The `v<N>` title scheme avoids the
  case hazard naturally, but the platform should still reject case-insensitive title collisions on any human-editable
  path, and must not expect PRs in `?milestone=` results.
- **gh CLI is not a fallback for milestone assignment**: it cannot see closed milestones by name and resolves names
  case-insensitively. All platform writes should stay on REST/GraphQL (they do); document this for operators using
  `gh` by hand.
- Issue-create silently drops the milestone for tokens without push access — treat a created-but-unmilestoned issue
  as a hard error at plan time (assert `milestone` non-null in the create response).
