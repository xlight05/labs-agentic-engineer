# AEP — Ubiquitous Language

Glossary of domain terms for the Agentic Engineer Platform. Implementation-free:
this file defines what terms *mean*, not how anything works.

## Agents service (`services/agents`)

**Skill**:
A unit of procedural guidance — a `SKILL.md` (frontmatter `name` + `description`,
markdown body) — that the main agent may follow while editing a spec bundle. The
caller passes the candidate Skills (`name`, `description`, `content`) in the turn
request payload. A Skill is *guidance*, not code: never executed, never uploaded
to a model provider, never read from disk by the service (the caller — the eval —
resolves Skills from the repo).
_Avoid_: plugin, capability, tool (a tool is the agent's executable action, e.g. `editFile`).

**Skill catalog**:
The list of available Skills — `name` + one-line `description` only, no bodies —
appended to the end of the agent's system prompt. It is the agent's index for
deciding which Skills to pull. Progressive disclosure: metadata is always visible,
the body is fetched on demand.

**`loadSkill`**:
The tool the agent calls to fetch a Skill's full `content` by name. The body enters
context only when loaded, and then persists as a tool result in message history.
This is the only way a Skill body reaches the model.

**Spec bundle**:
The in-memory set of files (a snapshot, keyed by path) the main agent reads and
mutates during a turn. Lives only in the service process; never sent to a sandbox.
_Avoid_: workspace, repo, project.

**Turn**:
One request→response cycle of the main agent: a user instruction plus the current
spec bundle in, a stream of file mutations out. One turn = one POST.
