---
name: openapi-conventions
description: Conventions for editing OpenAPI YAML specs — operationIds, response descriptions.
---

# OpenAPI conventions

Apply these when editing an `openapi.yaml`:

- Every operation has an `operationId` in lowerCamelCase (e.g. `getHello`).
- Every response has a non-empty `description`.
- Request and response bodies prefer `application/json`.
