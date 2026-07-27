# ADR-0004 — Dependency wiring is authored by the coding agent, never patched by the platform

A component's `workload.yaml` is written entirely by the coding agent, including
the consumer-side `dependencies:` block that names the services and resources it
consumes. The platform never patches a deployed `Workload` CR.

The alternative — the platform patching `spec.dependencies` into the CR after
deploy — worked, but it split ownership of the workload between the repo and the
platform: `git checkout` of the repo no longer reproduced the running system.
Declarative wiring keeps the repo the single source of truth for the workload, at
the cost of one round trip through the coding agent whenever dependencies change.

## Telling the agent what to write

The agent cannot invent an address, so the platform resolves the targets — catalog
names, endpoint visibility, resource outputs, env-var bindings — and **posts them
as a "Platform-resolved dependencies" comment for the agent to copy verbatim**.
Everything the agent needs to write the block is in that comment, and the fetching
of provider API contracts it also carries ([ADR-0010][adr10]) hangs off the same
resolution.

**The comment goes up when the dependency's provisioning gate resolves.** That is
the first moment the address exists: a platform resource has no host until its
OpenChoreo binding reports Ready, and an org-service has no cross-project target
until its provider publishes. The gate's resolution path already finishes the
provisioning run, closes the gate issue with the binding reference, and
re-evaluates dispatch; posting the comment there means the wiring lands before any
agent the same resolution releases starts work. Resolving earlier, when the version
is planned, would have nothing to say. Resolving later, once a pull request has
merged, would be after the agent has already written the file — which is how a
generated service ends up hardcoding `localhost` and crash-looping against a
database that was sitting in its own namespace all along.

The comment lands on the run's **working set** — the milestone's open `aep` issues,
minus the gates and the validation issue ([ADR-0011][adr11]). A dependency is
project-level and nothing platform-side attributes an *issue* to a component: issue
bodies are prose and titles are renamable, so there is no sound index from a
component to the issue that builds it. The recipient set is therefore the whole
working set, and the **content** is keyed by component instead — one block per
design component that consumes the dependency, each naming the `workload.yaml` it
belongs in. One agent works the whole milestone in a cycle, so the block reaches
its reader either way.

Posting is idempotent on an `aep:wired/<slug>` label stamped on each issue that has
received a dependency's comment, because the resolution path re-runs: a re-build
re-mints and re-settles a gate for a dependency that is already provisioned. Each
block carries the component's **whole** resolved set rather than just the
dependency that triggered the post, so the most recent comment for a component is
always the complete answer.

## Consequences

- **A comment on a sibling issue.** An agent working issue #21 may find the block
  for a component it is not building; the block's heading names its owner, and the
  agent applies it to that component's file.
- **A dependency resolved before the version is planned needs another route.** The
  build's dependency step runs before the planning turn, so a dependency that
  resolves synchronously there — an external connection whose values were already
  collected, or one already provisioned from an earlier version — resolves into an
  empty working set and posts nothing. The async resolutions (a platform resource
  reaching Ready, an org-service publishing) land after the plan and are unaffected.
  Closing it means the run supervisor asking for the already-resolved wiring once
  the plan exists, at the cycle's first dispatch.
- **A partially resolved block is normal.** Anything unresolvable is omitted rather
  than guessed at, and the next resolution re-posts the fuller block.

The mechanism lives where it is enforced:
[`internal/dependencies/README.md`][deps] (L2) under the README ladder
([ADR-0008][adr8]).

[adr8]: ADR-0008-architecture-in-readme-ladder.md
[adr10]: ADR-0010-coding-agent-researches-external-contracts.md
[adr11]: ADR-0011-milestone-is-the-unit-of-execution.md
[deps]: ../../services/aep-api/internal/dependencies/README.md
