---
name: component-architecture
description: How to derive a component from the design — where its spec lives and the frontmatter it must carry.
---

# Deriving a component

A component is one independently deployable unit. Derive each as its own file at
`specs/design/components/<name>/design.md`, where `<name>` is the component's
kebab-case id. Give it YAML frontmatter with exactly these keys:

- `type`: `service` for a backend/API, `webapp` for a user-facing UI.
- `language`: the implementation language (e.g. `Go`, `TypeScript`).
- `buildpack`: always `docker`.

Follow the frontmatter with a one-paragraph description of the component's single
responsibility. One component per directory — never put two components in one file.
