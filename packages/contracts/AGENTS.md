# AGENTS.md — packages/contracts (`@aep/contracts`)

## Instructions

- The OpenAPI contract is hand-maintained (contract-first), never generated.
  Edit deliberately — server code and console types are generated FROM it.
- `api/v1/` is a two-document contract (OpenAPI 3.0.3):
  - `openapi.yaml` — paths + security schemes; references the components doc.
  - `components.yaml` — every schema, kept separate so Go types can generate
    into `services/aep-api/models` while server code generates into its own
    package.
- Every error response points at the shared `Error` schema
  (`{code, message, details?[{field, message}]}`, always `application/json`);
  validation failures are `400`.
- The `path` parameter of `read-file` is a trailing wildcard (may contain
  slashes); the server registers the extra catch-all route for it.
