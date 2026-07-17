## Why codebase design matters for AI coding agents

A maintainable codebase for AI coding agents is one that lets an unfamiliar
agent find the right context, make a narrow change, and verify that change
without loading the whole system into working memory. As AI writes much larger
volumes of code much faster, defects can now propagate through a codebase at a
pace that used to require an entire team. The core problem is not that coding
agents are incapable; it is that they need legible structure, reliable
constraints, and feedback loops that let them verify their own work.
[\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[7\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-7) [\[16\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-16) [\[20\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-20)

That shifts the engineering problem. Instead of asking only whether humans can
maintain the system, we also have to ask whether an unfamiliar agent can explore
it, form a correct local model, make a narrow change, and validate that change
without disturbing unrelated parts of the application. OpenAI describes this as
increasing “application legibility,” while Anthropic emphasizes progressive
disclosure: the agent should discover relevant context incrementally rather than
drown in a giant undifferentiated prompt. [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[7\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-7)

> “Give Codex a map, not a 1,000-page instruction manual.” — OpenAI

In short, a maintainable codebase for AI coding agents starts with structure:
changes should stay local, side effects should remain contained, boundaries
should be explicit, and the codebase should stay simple enough that an agent can
build a correct local model without exploring the whole system. This article
focuses on those structural properties. The next article covers the supporting
systems around that structure: documentation, executable guardrails, and
verification loops.

The approaches below are not the only way to achieve a maintainable codebase.
The point is to compare design choices by the way they affect a few underlying
characteristics that matter for both humans and agents.

## What makes a codebase maintainable for AI coding agents?

> _"Everything in software architecture is a trade-off. \[...\] If an architect_
> _thinks they have discovered something that isn’t a trade-off, more likely they_
> _just haven’t yet identified the trade-off."_
>
> [DevIQ - Laws of Software Architecture](https://deviq.com/laws/laws-software-architecture/)

There is no architecture without trade-offs, and those trade-offs are not purely
technical. Different teams will tolerate different levels of duplication,
indirection, centralization, or coordination overhead. The useful question is
not “what style is right?” but “how does a given style improve or degrade the
following properties?”

| Characteristic | Why it matters for AI coding agents | What goes wrong when it is weak |
| --- | --- | --- |
| **Locality** | Lets an agent make a change without loading half the system into context | Features become scattered across modules, helpers, and side effects |
| **Blast radius** | Limits unintended consequences of a change | Small edits trigger broad regressions or risky refactors |
| **Boundary integrity** | Makes contracts and responsibilities legible | Agents infer the wrong contract from local evidence |
| **Navigability** | Helps an unfamiliar agent find the right files and entry points quickly | Agents waste context budget exploring irrelevant parts of the repo |
| **Rebuild/test scope** | Enables narrow verification loops and fast self-correction | Every change requires broad builds or slow end-to-end validation |

A quick diagnostic is simple: can a new coding agent find the relevant behavior,
change one bounded area, and run the smallest meaningful verification loop
without guessing? If not, the codebase is asking for more hidden context than an
agent can reliably hold.

### Locality

Locality describes how many places need to be touched to complete a concrete
task. In a healthy codebase, most changes stay narrow and do not require edits
across unrelated modules. Parnas’s classic formulation is still the right mental
model: modules should be organized around decisions likely to change, and those
decisions should be hidden from the rest of the system. [\[1\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-1)

> “Each module is then designed to hide such a decision from the others.” —
> Parnas

That matters even more for agentic engineering, AI-assisted software
engineering, and AI coding agents. If a feature is scattered across shared
helpers, orchestration layers, utility packages, and cross-cutting side effects,
the agent must keep more of the system in context and is more likely to miss one
dependency or “fix” the wrong abstraction. By contrast, when code is aligned
with cohesive business capabilities, many single-service or single-slice changes
can remain local to that area. [\[4\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-4) [\[5\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-5) [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6)

A practical consequence is that locality is not just about folder layout. It
depends on whether the ownership boundary, the semantic boundary, and the
build-and-test boundary line up well enough that the smallest sensible unit of
work is also the smallest sensible unit of change. [\[3\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-3) [\[5\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-5)

### Blast radius

Blast radius is the size of the unintended side effects of a change. A small
blast radius means a change remains local; a large one makes refactorings and
fast iteration much riskier than they should be. Protected Variation is a useful
lens here: likely points of change should sit behind stable interfaces so that
clients are not forced to move with every implementation decision. [\[2\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-2)

> “Identify points of predicted variation and create a stable interface around
> them.” — Larman

For coding agents, blast radius is partly an architectural issue and partly a
workflow issue. The architecture determines how far a change can leak; the
harness determines how much the agent is allowed to touch before it proves
safety. OpenAI’s agent-first repo uses mechanically enforced dependency rules,
while recent agent-harness work recommends filtered tools, isolated worktrees,
and explicit progress checkpoints so the system can recover from bad turns
instead of amplifying them. [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[8\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-8) [\[19\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-19)

This is also why migration patterns such as branch by abstraction or legacy
displacement matter in an agentic setting: they create seams that allow large
changes to proceed gradually instead of forcing a broad, one-shot rewrite.
[\[14\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-14) [\[15\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-15)

### Boundary integrity

Boundary integrity is about how clear the borders are between modules,
components, or services. Good boundaries rely on explicit interfaces and clearly
separated responsibilities, while heavy coupling usually signals structural
weakness. Fowler’s bounded-context framing is especially useful here: large
systems rarely support one perfectly unified model, so the healthier move is to
define local models and the translations between them explicitly. [\[3\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-3)

> “Strict boundaries and predictable structure.” — OpenAI

That principle becomes more important with agents because agents reason from
partial observations. If one module reaches directly into another team’s tables,
state, or side effects, the agent may infer the wrong contract from local
evidence. Mechanically enforced layer rules, explicit provider interfaces, and
dependency-direction checks reduce that ambiguity and make the architecture
legible in a way prompts alone cannot. [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[20\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-20)

Boundary integrity also improves failure handling. When responsibilities are
explicit, verification can target the contract at the boundary rather than
revalidating an entire downstream chain of accidental couplings. [\[2\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-2) [\[4\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-4)

### Navigability

Navigability is the speed with which a new person can orient themselves in the
codebase. This is especially important for AI coding agents, because they can
only inspect a small part of the system at a time. Anthropic describes the ideal
as progressive disclosure: the agent should be able to retrieve just enough
context for the next decision, then refine its mental model as exploration
continues. [\[7\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-7)

> “Agents can assemble understanding layer by layer.” — Anthropic

That makes searchability and repository-local knowledge first-class engineering
concerns. OpenAI argues for treating a short `AGENTS.md` as a table of contents
rather than an encyclopedia, while Google’s engineering guidance stresses both
code search and documentation designed to help unfamiliar contributors get
something real running quickly. [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[9\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-9) [\[11\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-11)

Recent work on code agents also suggests that architecture understanding itself
is an active-exploration problem. Agents build beliefs from partial
observations, revise them as the environment changes, and then exploit those
beliefs to perform downstream tasks. In practice, that means good navigability
is less about clever naming alone and more about giving the repo enough maps,
conventions, and discoverable entry points that those beliefs converge quickly
and correctly. [\[18\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-18) [\[20\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-20)

### Rebuild/test scope

Rebuild and test scope determine how narrowly a change can be verified. The
better the structure, the more often only the affected parts need to be built or
tested, which keeps feedback fast and iteration practical. Google’s testing
guidance is blunt: larger system-scale tests were often slower, less reliable,
and harder to debug than smaller tests. [\[10\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-10)

> “Slower, less reliable, and more difficult to debug than smaller tests.” —
> Software Engineering at Google

For agentic engineering and AI coding agents, narrow verification is not a
convenience; it is the difference between an agent being able to self-correct
within its loop and needing a human to arbitrate every change. Fowler’s
recommendation to shift validation left applies directly here: agents produce
better code when they can gauge quality themselves. [\[16\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-16) [\[17\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-17)

That requires both architectural seams and tooling seams. Hermetic builds make
local validation trustworthy by isolating the build from the host machine;
test-impact analysis narrows execution to the subset of tests relevant to the
change; and evaluation loops let teams check outputs against unit tests, latency
targets, or style rules without broad manual review. [\[12\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-12) [\[13\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-13) [\[17\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-17)

## How do you design a codebase that AI coding agents can work in safely?

The following approaches are useful not because they are fashionable, but
because they improve one or more of the characteristics above. Each one also
carries real costs, so the goal is not ideological purity but better trade-offs.

### Domain-oriented modules with vertical slices

Domain-oriented top-level modules mean the first thing you see in `src/`
reflects business capabilities, not technical layers. The tree should "scream"
the domain (`billing`, `identity`, `catalog`, `publishing`) rather than
framework concerns (`controllers`, `services`, `utils`). This is close to
Screaming Architecture: the structure tells you what the system does before it
tells you which tools it uses. Technical concerns still exist, but they live
inside each domain boundary instead of becoming the global organizing principle.

Vertical slices are easy to misunderstand. In the narrow sense used by Jimmy
Bogard, a slice is not just a business noun like `user` or `billing`. It is a
single request or use case: `changePassword`, `getUserProfile`, `refundPayment`,
`importCustomers`. The slice is the unit of change, validation, and
verification. [\[22\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-22)

For AI-heavy codebases, the most robust default is usually a hybrid: first-level
folders by domain or business capability, then vertical slices inside each
domain. For example:

```text
src/
  user/
    changePassword/
      command.ts
      validator.ts
      handler.ts
      test.ts
    viewUserDetails/
      query.ts
      handler.ts
      test.ts
  billing/
    refundPayment/
      command.ts
      handler.ts
      test.ts
```

That structure keeps the top-level map readable while still giving each concrete
change a small working set. The domain folder answers “who owns this concept?”,
while the slice answers “what behavior is changing?”

That hybrid tends to score well on the five characteristics above:

- Locality: the domain narrows where to look, and the slice narrows what to
change.
- Blast radius: shared rules stay in the domain boundary instead of being copied
across unrelated features.
- Boundary integrity: folder names tell you whether you are looking at an owned
concept or a specific behavior.
- Navigability: a new person can first pick the business area, then the use
case.
- Rebuild/test scope: each slice usually maps to a small test cluster or request
path.

The trade-offs are still real. Shared technical concerns can become harder to
standardize, and features that cut across multiple slices still need
coordination. But for AI-assisted development, the domain-plus-slice hybrid is
usually safer than either a flat slice list or recursive domain nesting alone.
[\[5\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-5) [\[23\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-23)

### Coupling and cohesion

Coupling and cohesion describe whether code that belongs together actually lives
together, and whether unrelated things are kept apart. High cohesion means a
module has a clear purpose; low coupling means that purpose does not depend on
hidden knowledge spread across the rest of the codebase. Parnas’s and Larman’s
arguments point in the same direction: hide volatile design decisions and
protect clients from expected variation. [\[1\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-1) [\[2\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-2)

In an agentic repo, this has a second-order effect on context quality. Cohesive
modules make retrieval cleaner because the files an agent opens are more likely
to belong to the same concern; loosely coupled modules make the agent’s inferred
contract more stable because fewer unrelated files silently participate in the
behavior. [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[7\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-7)

The most dangerous forms of coupling are the ones that defeat local reasoning. A
file may look self-contained, yet its behavior depends on conventions, side
effects, or assumptions defined elsewhere. In practice, the worst offenders are
not ordinary call relationships but hidden forms of dependency that make changes
propagate unpredictably. [\[25\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-25) [\[28\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-28) [\[30\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-30)

**Global-state coupling** is one of the most severe forms. A module reads or
mutates shared process-wide state—configuration registries, caches, feature
flags, singletons, ambient context, mutable globals—and therefore depends on
facts that do not appear in its interface. The result is that behavior cannot be
understood from the module and its direct inputs alone. This is damaging in any
codebase, but especially in an agentic one: retrieval may surface the file that
performs the work without surfacing the file that quietly sets the state it
relies on. [\[24\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-24) [\[25\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-25) [\[19\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-19) [\[20\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-20)

**Temporal coupling** is similarly corrosive. Here, correctness depends on
operations occurring in the right order: initialize this before calling that,
call `begin` before `commit`, populate a cache before reading from it, attach
middleware before issuing requests. The dependency is real, but it is encoded in
sequence rather than in types or explicit contracts. Temporal coupling often
survives code review because each individual step looks valid in isolation.
Failures appear only when a caller omits or reorders one of the hidden
preconditions. [\[26\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-26)

**Control coupling** becomes severe when one module passes flags, modes, or
discriminator values that instruct another module which branch of behavior to
execute. At small scale this seems harmless; at larger scale it means the caller
must know too much about the callee’s internal decision structure. A boolean
like `is_preview` or `skip_validation`, or a mode string like `"fast"` versus
`"safe"`, often signals that one abstraction is doing several jobs at once. The
interface stops describing a capability and starts exposing internal policy.
[\[24\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-24) [\[29\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-29)

**Semantic coupling through shared conventions** is worse still because it is
hard to see. Two modules may appear decoupled at the syntax level while
depending on naming schemes, magic string formats, directory layouts,
error-message text, undocumented JSON shapes, or implied units and meanings.
Nothing in the type system or function signature may reveal the dependency, but
changing the convention still breaks the system. This is one of the most common
failure modes in generated code, where separate files independently “agree” on a
format that was never explicitly defined anywhere. [\[25\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-25) [\[30\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-30)

**Content coupling** is the most obviously severe. One module reaches into
another’s internals, depends on private fields, patches hidden structures, or
relies on incidental implementation details instead of a published interface.
This creates brittle software because the client is no longer coupled to
behavior alone; it is coupled to representation. Even small internal refactors
become externally breaking changes. If low coupling means clients depend only on
what a module promises, content coupling is its direct opposite. [\[24\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-24)

A related category is **cross-cutting policy coupling**: authorization, logging,
retries, transactions, validation, or feature-flag decisions spread across many
modules instead of being centralized behind stable boundaries. This makes the
“real” behavior of the system emerge from the combination of many files, each
carrying a fragment of the rule. Such systems are hard to test, hard to change
safely, and hard to retrieve correctly because no single file states the whole
policy. [\[27\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-27) [\[8\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-8)

What makes these forms of coupling especially costly is that they expand the
hidden context required for correct change. In a healthy design, modifying a
behavior should require understanding a small, explicit neighborhood of code. In
a badly coupled design, the true neighborhood is larger than it appears. Humans
compensate with experience; agents compensate by opening more files. Both become
less reliable as more of the contract lives offstage. [\[25\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-25) [\[28\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-28) [\[30\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-30) [\[19\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-19) [\[20\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-20)

The practical priority, then, is not to eliminate all dependency. It is to
eliminate the dependency forms that smuggle knowledge across boundaries: ambient
state, sequencing requirements, mode flags, unowned conventions, and reliance on
internals. These are the couplings that most directly destroy cohesion, widen
change impact, and reduce the quality of reasoning that a codebase permits.
[\[24\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-24) [\[25\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-25) [\[30\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-30)

The trade-off is that stronger interfaces can increase friction at boundaries. A
team that aggressively decomposes everything may gain cleaner contracts but lose
flow through excess coordination.

### Avoiding unnecessary complexity

Avoiding unnecessary complexity means resisting abstractions, indirections, and
generalizations that do not solve a current problem. Complexity is not only a
readability issue; it expands the amount of context required to make safe
changes. OpenAI’s experience with agent-first development makes this very
concrete: a giant manual, a sprawling abstraction layer, or a maze of special
cases all crowd out the relevant signal. [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[7\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-7)

> “You must pick your battles in design.” — Larman

A common source of unnecessary complexity is **premature abstraction**:
introducing interfaces, wrappers, or generic mechanisms before the underlying
variation is real. This often feels like prudence, but it can turn one simple
path into several layers of indirection that every future reader must traverse.
In practice, an abstraction earns its keep only when it removes repeated change
or protects a volatility that has actually shown up. [\[2\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-2) [\[24\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-24) [\[25\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-25)

Unnecessary complexity also accumulates through **special cases**: feature
flags, compatibility branches, fallback paths, and one-off exceptions that
survive long after the original need has passed. Each exception may be locally
reasonable, yet together they widen the amount of context required to understand
the normal path. For agents especially, this is costly: every additional branch,
wrapper, or tool surface competes with the core signal needed to complete the
task. [\[26\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-26) [\[7\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-7) [\[27\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-27)

For agentic engineering, unnecessary complexity has a direct token cost.
Abstractions that are harmless for humans in a small codebase become expensive
when an agent must retrieve, interpret, and reconcile them across many files and
tool definitions. Simplicity is therefore not only a design preference but a
context-management strategy: fewer layers, fewer exceptions, and fewer invented
concepts leave more room for the information that actually determines behavior.
[\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[7\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-7) [\[27\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-27)

That does not mean “never abstract.” It means introducing indirection where it
protects meaningful variation, not where it merely advertises cleverness. If the
abstraction does not reduce the cost of future change, it often raises the cost
of navigation, testing, and debugging instead. [\[2\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-2)

### Avoiding premature generalization

Generalization is different from abstraction. An abstraction reduces something
to its essential form; a generalization consolidates multiple concrete cases
behind a shared module, interface, configuration surface, or reusable mechanism.
The risk is not merely that detail gets hidden, but that behaviors which only
appear similar are forced to live under one shared structure before there is
enough evidence that they truly belong together. [\[1\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-1) [\[2\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-2) [\[34\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-34) [\[35\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-35)

Avoiding premature generalization means refusing to introduce shared
abstractions, flexible configuration layers, or generic building blocks before
there is enough evidence that the behavior is truly shared. It is usually
cheaper to extract later than to maintain the wrong generalization too early.
[\[1\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-1) [\[2\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-2) [\[34\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-34) [\[36\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-36)

The failure mode is that early consolidation often creates a false common core.
Code paths that only look alike at first begin to diverge under real use, and
the shared layer then accumulates flags, branches, and configuration to preserve
the illusion of uniformity. Instead of reducing complexity, the generalized
module becomes the place where unrelated variation is forced to coexist.
[\[34\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-34) [\[35\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-35)

This matters disproportionately in agentic codebases because speculative
generalization expands the search space and obscures the source of truth. An
agent confronted with a premature “platform” layer has to infer whether the true
behavior lives in the feature code, the generic framework, the configuration
surface, or all three. What looks like reuse to the designer often looks like
ambiguity to the agent. [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[7\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-7)

A safer approach is to let duplication survive a little longer while the real
axes of variation become clear. Once repeated behavior is stable, extraction is
more likely to produce a shared seam that matches reality instead of a
generalized layer that has to be constantly patched to fit new cases.
[\[34\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-34) [\[35\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-35) [\[36\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-36)

When extraction is actually needed, patterns such as branch by abstraction let
teams move toward a shared seam gradually instead of forcing a big-bang
migration. [\[14\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-14)

### Avoiding premature optimization

Avoiding premature optimization means not distorting the structure of the system
around performance concerns that are not yet real constraints. Once optimization
becomes the driver too early, the codebase often becomes harder to understand,
change, and verify long before the performance gain is actually needed. For
agents, this usually manifests as extra caches, specialized execution paths, and
implicit invariants that are poorly documented and hard to validate locally.
[\[16\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-16) [\[17\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-17)

A useful test is whether the performance requirement is explicit enough to be
checked. A latency ceiling, memory budget, throughput target, or infrastructure
cost constraint can be measured and enforced; an imagined future bottleneck
cannot. Premature optimization becomes less likely when performance concerns are
expressed as budgets and profiling targets rather than as architectural
folklore. [\[37\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-37) [\[38\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-38)

The main danger is not only extra code, but extra invariants. Caches must stay
coherent, fast paths must remain behaviorally equivalent to slow paths, pooled
resources must be released correctly, and concurrent execution paths must
preserve assumptions that are rarely obvious from the interface. These hidden
constraints increase the amount of context required for safe change, which makes
them especially costly in agentic codebases. [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[7\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-7) [\[37\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-37)

When performance is truly a first-class requirement, the right response is not
speculative cleverness but explicit feedback loops: benchmarks, profilers,
latency SLOs, regression thresholds, and tests for the critical path. That gives
agents something concrete to optimize against while preserving a codebase whose
structure still reflects the product rather than a guessed bottleneck.
[\[17\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-17) [\[37\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-37) [\[38\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-38) [\[39\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-39)

That does not rule out performance-first design for real hot paths. It means
that performance work should be driven by measured constraints and reinforced by
explicit tests or budgets, not by speculative complexity. Evaluation loops
against latency targets are a better fit for agents than architecture distorted
in advance around imagined bottlenecks. [\[17\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-17)

### Ownership-aligned boundaries

Boundary quality improves when the conceptual structure of the system lines up
with ownership. When teams or maintainers have clear responsibility for a
bounded area, conventions, documentation, and decision-making tend to stay more
coherent inside that area, which also makes it easier for agents to orient
themselves. The goal is not to fragment the system for its own sake, but to
decompose it so that the unit of ownership, the unit of change, and the unit of
understanding reinforce each other instead of pulling apart. [\[5\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-5) [\[21\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-21)

Ownership alignment is also a coordination strategy. When a boundary matches a
team’s day-to-day responsibility, decisions about naming, documentation,
interfaces, and change sequencing are more likely to stay consistent inside that
area. When the boundary and the ownership model diverge, the software may still
look modular on paper while the real work crosses teams, handoffs, and approval
paths. That usually means the true cost of change is higher than the structure
suggests. [\[5\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-5) [\[21\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-21) [\[48\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-48)

This is closely related to Conway’s Law: systems tend to mirror the
communication structure of the organization that builds them. If a supposedly
bounded part of the system requires constant coordination across unrelated
owners, the boundary is probably wrong, incomplete, or socially unenforced. In
that case, the unit of understanding in the code and the unit of collaboration
in the organization are pulling in different directions. [\[48\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-48) [\[21\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-21)

For agentic engineering, ownership-aligned boundaries improve more than human
workflow; they improve context quality. A bounded area with clear ownership is
more likely to have coherent conventions, localized documentation, and a stable
source of truth, all of which make it easier for an agent to determine where a
change belongs and what assumptions it must preserve. [\[7\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-7) [\[17\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-17) [\[49\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-49)

The supporting systems around those boundaries matter too, but they deserve
their own treatment. The next article covers repository-local documentation,
behavior specs, executable guardrails, and verification loops.

## FAQ about maintainable codebases for AI coding agents

### What makes a codebase AI-agent-friendly?

A codebase is AI-agent-friendly when an unfamiliar coding agent can find the
right context, make a narrow change, and verify the result without exploring the
whole system. In practice, that means strong locality, small blast radius,
explicit boundaries, good navigability, and narrow verification loops.
[\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[7\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-7) [\[16\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-16)

### Why are vertical slices useful for AI coding agents?

Vertical slices reduce the semantic search space. Instead of scattering a
feature across technical layers and cross-cutting abstractions, they let the
agent work mostly inside one cohesive behavior, especially when that slice sits
inside a domain boundary. That improves locality, navigability, and testability.
[\[3\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-3) [\[4\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-4) [\[5\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-5) [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[22\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-22) [\[23\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-23)

### How do you reduce blast radius for AI-generated code changes?

You reduce blast radius by putting likely points of change behind stable
interfaces, enforcing dependency directions mechanically, and giving agents
narrow workflows with isolated worktrees, checkpoints, and targeted validation.
The goal is to make changes safer both structurally and operationally.
[\[2\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-2) [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[8\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-8) [\[19\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-19)

### Why do AI coding agents need clear boundaries?

AI coding agents reason from partial observations. If boundaries are weak, the
agent may infer the wrong contract from a local view of the code. Clear
interfaces, bounded contexts, and explicit ownership reduce ambiguity and make
the architecture more legible. [\[3\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-3) [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[20\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-20)

### Is this only relevant for teams using coding agents?

No. These characteristics also make codebases easier for humans to understand
and maintain. AI coding agents simply make structural weaknesses visible sooner,
because they rely more heavily on legible boundaries, concise context, and fast
validation. [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[7\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-7) [\[16\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-16)

## Conclusion

A maintainable codebase for AI coding agents starts with structure. If behavior
stays local, boundaries are explicit, ownership is legible, and unnecessary
complexity is kept out, agents can build better local models and change software
more safely. Those properties are valuable for humans too; agents simply make
the cost of neglecting them visible much sooner. [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[7\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-7) [\[16\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-16)

The next article builds on that structure by covering the surrounding support
system: documentation, behavioral specs, executable guardrails, and narrow
feedback loops. [\[6\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-6) [\[17\]](https://maintainable.software/agentic-engineering-part-2-agentic-codebase-principles/#ref-17)

## References

01. [D. L. Parnas, “On the Criteria To Be Used in Decomposing Systems into Modules,” 1971.](https://prl.khoury.northeastern.edu/img/p-tr-1971.pdf)
02. [Craig Larman, “Protected Variation: The Importance of Being Closed,” IEEE Software, 2001.](https://www.martinfowler.com/ieeeSoftware/protectedVariation.pdf)
03. [Martin Fowler, “Bounded Context.”](https://martinfowler.com/bliki/BoundedContext.html)
04. [Martin Fowler and James Lewis, “Microservices.”](https://martinfowler.com/articles/microservices.html)
05. [Chris Richardson, “Service per team.”](https://microservices.io/patterns/decomposition/service-per-team.html)
06. [OpenAI, “Harness engineering: leveraging Codex in an agent-first world.”](https://openai.com/index/harness-engineering/)
07. [Anthropic, “Effective context engineering for AI agents.”](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents)
08. [Anthropic, “Effective harnesses for long-running agents.”](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)
09. [Software Engineering at Google, Chapter 10: Documentation.](https://abseil.io/resources/swe-book/html/ch10.html)
10. [Software Engineering at Google, Chapter 11: Testing Overview.](https://abseil.io/resources/swe-book/html/ch11.html)
11. [Software Engineering at Google, Chapter 17: Code Search.](https://abseil.io/resources/swe-book/html/ch17.html)
12. [Bazel, “Hermeticity.”](https://bazel.build/basics/hermeticity)
13. [Microsoft Learn, “Use Test Impact Analysis.”](https://learn.microsoft.com/en-us/azure/devops/pipelines/test/test-impact-analysis?view=azure-devops)
14. [Martin Fowler, “Branch By Abstraction.”](https://martinfowler.com/bliki/BranchByAbstraction.html)
15. [Martin Fowler, “Patterns of Legacy Displacement.”](https://martinfowler.com/articles/patterns-legacy-displacement/)
16. [Martin Fowler, “Humans and Agents in Software Engineering Loops.”](https://martinfowler.com/articles/exploring-gen-ai/humans-and-agents.html)
17. [OpenAI Developers, “Building an AI-Native Engineering Team.”](https://developers.openai.com/codex/guides/build-ai-native-engineering-team/)
18. [Cognition, “How Cognition Uses Devin to Build Devin.”](https://cognition.ai/blog/how-cognition-uses-devin-to-build-devin)
19. [“Building Effective AI Coding Agents for the Terminal.”](https://arxiv.org/html/2603.05344v2)
20. [“Theory of Code Space: Do Code Agents Understand Software Architecture?”](https://arxiv.org/html/2603.00601v4)
21. [Martin Fowler, “Linking Modular Architecture to Development Teams.”](https://martinfowler.com/articles/linking-modular-arch.html)
22. [Jimmy Bogard, “Vertical Slice Architecture.”](https://www.jimmybogard.com/vertical-slice-architecture/)
23. [Herbert Graca, “Packaging & namespacing.”](https://herbertograca.com/2017/08/31/packaging-code/)
24. [W. P. Stevens, G. J. Myers, and L. L. Constantine, “Structured Design,” _IBM Systems Journal_, 1974.](https://dl.acm.org/doi/10.1147/sj.132.0115)
25. [N. Ajienka, A. Capiluppi, and S. Counsell, “Managing Hidden Dependencies in OO Software: A Study Based on Open Source Projects,” _ACM/IEEE International Symposium on Empirical Software Engineering and Measurement (ESEM)_, 2017.](https://doi.org/10.1109/ESEM.2017.21)
26. [S. Amann, H. A. Nguyen, S. Nadi, T. N. Nguyen, and M. Mezini, “A Systematic Evaluation of Static API-Misuse Detectors,” _IEEE Transactions on Software Engineering_, 2019.](https://www.computer.org/csdl/journal/ts/2019/12/08338426/13rRUzphDzB)
27. [G. Kiczales et al., “Aspect-Oriented Programming,” _European Conference on Object-Oriented Programming (ECOOP)_, 1997.](https://dl.acm.org/doi/10.1145/263698.263754)
28. [H. Gall, K. Hajek, and M. Jazayeri, “Detection of Logical Coupling Based on Product Release History,” _International Conference on Software Maintenance (ICSM)_, 1998.](https://plg.uwaterloo.ca/~migod/846/papers/gall-coupling.pdf)
29. [Martin Fowler, “Flag Argument,” 2011.](https://martinfowler.com/bliki/FlagArgument.html)
30. [M. Cataldo, A. Mockus, J. A. Roberts, and J. D. Herbsleb, “Software Dependencies, Work Dependencies, and Their Impact on Failures,” _IEEE Transactions on Software Engineering_, 2009.](https://cse.unl.edu/~witty/papers/TSE_2008_11_0361_R1.pdf)
31. [Cucumber, “BDD.”](https://cucumber.io/docs/bdd/)
32. [OpenAI Developers, “Testing Agent Skills Systematically with Evals.”](https://developers.openai.com/blog/eval-skills/)
33. [Cucumber, “Gherkin Reference.”](https://cucumber.io/docs/gherkin/reference/)
34. [Sandi Metz, “The Wrong Abstraction.”](https://sandimetz.com/blog/2016/1/20/the-wrong-abstraction)
35. [John Ousterhout, _A Philosophy of Software Design_, 2nd ed., 2021.](https://web.stanford.edu/~ouster/cgi-bin/aposd2ndEdExtract.pdf)
36. [Kent C. Dodds, “AHA Programming.”](https://kentcdodds.com/blog/aha-programming)
37. [Martin Fowler, “Yet Another Optimization Article,” _IEEE Software_, 2002.](https://www.martinfowler.com/ieeeSoftware/yetOptimization.pdf)
38. [Katie Hempenius, “Performance Budgets 101.”](https://web.dev/articles/performance-budgets-101)
39. [Google SRE, “Service Level Objectives.”](https://sre.google/sre-book/service-level-objectives/)
40. [Neal Ford, Rebecca Parsons, and Patrick Kua, _Building Evolutionary Architectures_, sample chapter.](https://www.thoughtworks.com/content/dam/thoughtworks/documents/books/bk_building_evolutionary_architectures_en.pdf)
41. [ArchUnit, “User Guide.”](https://www.archunit.org/userguide/html/000_Index.html)
42. [Import Linter, “Layers.”](https://import-linter.readthedocs.io/en/latest/contract_types/layers/)
43. [Anthropic, “Harness design for long-running application development.”](https://www.anthropic.com/engineering/harness-design-long-running-apps)
44. [Thoughtworks, “Fitness function-driven development.”](https://www.thoughtworks.com/insights/articles/fitness-function-driven-development)
45. [Martin Fowler, “Test Pyramid.”](https://martinfowler.com/bliki/TestPyramid.html)
46. [Google Testing Blog, “Just Say No to More End-to-End Tests.”](https://testing.googleblog.com/2015/04/just-say-no-to-more-end-to-end-tests.html)
47. [Software Engineering at Google, Chapter 12: Unit Testing.](https://abseil.io/resources/swe-book/html/ch12.html)
48. [Martin Fowler, “Conway’s Law.”](https://martinfowler.com/bliki/ConwaysLaw.html)
49. [Team Topologies, “Key Concepts.”](https://teamtopologies.com/key-concepts)
