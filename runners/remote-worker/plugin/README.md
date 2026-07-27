# AEP Claude Code plugin

Defines the workflow contract an agent follows for a run dispatched by the AEP
platform. The dispatch prompt carries only a milestone reference; the whole
procedure lives here — discover the milestone's open `aep` issues, order them
by their dependency prose, derive the `aep/m<n>-c<k>` branch, commit one issue
at a time, and open ONE pull request listing `Resolves #N` per completed issue.
The platform merges it; no human does.

## What's inside

- `.claude-plugin/plugin.json` — plugin manifest.
- `.claude-plugin/marketplace.json` — marketplace metadata (for `claude plugin install`).
- `skills/aep/SKILL.md` — the milestone coding workflow.
- `skills/aep-validation/` — the validation workflow (issue-anchored).
- `skills/playwright-cli/` — vendored from `@playwright/cli` (Apache-2.0).

No MCP server. The agent uses `git` and `gh` directly inside the per-task
workspace; the platform observes the work via GitHub webhooks.

## Loaded by

- **Remote flow** — `remote-worker/src/lib/runner.ts` passes
  `plugins: [{ type: "local", path: <repo>/remote-worker/plugin }]` to the
  Claude Agent SDK at dispatch.
- **Local flow** — a developer can install it into their own Claude Code:
  ```bash
  claude plugin install /path/to/aep/remote-worker/plugin
  ```
  In local mode the developer authenticates with their own `gh auth login`;
  the skill is identical.

## Authentication

The skill never sees credentials. In remote mode the workspace is provisioned
with a git credential helper and a `gh` wrapper that fetch fresh tokens from
git-service on every call. In local mode `gh auth` and the developer's git
config supply credentials. Either way, the agent just runs `git` and `gh`.

See `docs/design/github-integration-phase0.md` §7 for the workspace credential
setup; `docs/design/github-integration-evolution.md` §6 for the per-org
credential model that takes over in Phase 2.
