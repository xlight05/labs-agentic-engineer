# dependencies — Dependencies & Provisioning

> **L2 · a domain.** Part of the [aep-api architecture](../../README.md).

Discover the platform-resource catalog and resource-type markers, provision the platform + external
resources a Spec declares, broker cross-project org-service access, coordinate the `aep:provision` gate,
and wire the resulting runtime config onto deployed apps. **The two halves of OpenChoreo's
`Workload.spec.dependencies[]` — resources (external / platform-resource) and endpoints (component /
org-service) — live here; provisioning mints the milestone's `aep:provision` gates and keeps their
execution rows.**

```mermaid
flowchart LR
  API(["/api/v1"]) --> HTTP
  MCP(["/mcp"]) --> DISC
  subgraph dependencies
    HTTP["httpapi — provisioning · mcpdiscovery(resource-types)"]
    ROOT["root (kernel) — provisioner cores (external+platform) · resource-type catalog · markers · naming · endpoint catalog"]
    PROV["provisioning — aep:provision gate lifecycle"]
    RC["runtimeconfig — SPA env-config convergence"]
    DISC["mcpdiscovery — MCP discovery server + resource-type/endpoint reads"]
    HTTP --> PROV
    HTTP --> DISC
    PROV --> ROOT
    RC --> ROOT
    DISC --> ROOT
  end
  ROOT -->|Resource/Binding CRs · resource-type discovery| OC[[OpenChoreo]]
  ROOT -->|secret values| SM[[platform/secrets · SM-API]]
  PROV -->|admit/finish provision executions| DEL[[delivery]]
  PROV -->|aep:provision gate issues| SC[[sourcecontrol]]
  RC -->|design at HEAD| SPEC[[spec]]
```

## Structure — kernel-root domain
Unlike the flat-root domains (`spec`/`organization`/`projects`), `dependencies` is the kernel-root shape
`delivery` uses. The two **pure provisioner cores** — `resources` (external + platform provisioner cores,
the `ResourceTypeCatalog`, `TypeMarkers`, `ExternalResourceBindingName`/`EnvVarName` naming) and `endpoints`
(the org-service `Catalog` + `resolve`) — have no back-edges and are the shared kernel every service builds
on, so `slice → root` is the only legal direction and they **are** the root package `dependencies`. The
three services are sub-package slices that import only that root.

| Slice | Ops / role | Reaches |
|---|---|---|
| `provisioning` | 7 HTTP ops: list/delete/collect-values external resources, provision-platform, dependency-status, request/list org-service access + the aep:provision gate lifecycle, watcher, teardown | root cores; delivery (provision execution rows); sourcecontrol (gate issues) |
| `mcpdiscovery` | the MCP discovery server + `ListPlatformResourceTypes` HTTP read | root `ResourceTypeLister` / endpoint catalog |
| `runtimeconfig` | the SPA `env-config.js` convergence service + its watcher (no HTTP op) | root naming/markers; spec (design at HEAD); repositories (execution enumerate) |

Each slice owns its service AND its HTTP handler (as delivery's `build` slice does); `httpapi` aggregates
the two handler-bearing slices and **holds `Deps`** — the kernel-root consequence that the root may not name
its own sub-package services (`root ⊥ slice`), so assembly lives in the one package allowed to import the
slices.

## Ports
| Port | Dir | Peer · contract |
|---|---|---|
| SecretWriter | needs | `platform/secrets` — SM-API vault writes for external-resource secret values |
| OC `Resource`/`ResourceReleaseBinding` CRUD · `ClusterResourceType` discovery | needs | `openchoreo` client — OC is the store |
| ExecutionStore (admit/finish) | needs | `delivery` — a gate's provisioning run is the last remaining execution row, and this is its write surface |
| IssueClient (aep:provision gate) | needs | `sourcecontrol` — gate issues, closed via a no-secrets reference |
| DesignReader / DesignBundleReader | needs | `spec` — design at HEAD (what to provision) + provider design bundles |
| the 8 public ops | offers | the edge (`dependenciesHandlers`) |

## Owns
- `ExternalResource` (an in-memory definition, NOT a DB row — see Persistence), `AccessRequest`, the
  authored OC external Resource model + provisioned binding values, the `aep:provision` gate issues
  (via `sourcecontrol`), and the resource-type catalog projection.
- **Persistence**: only `AccessRequest` is persisted (`repository_access_request.go` over
  `access_request.go`), single write-authority. `ExternalResource` is an in-memory definition, not a
  DB row — the org-namespaced OpenChoreo `ResourceType` is the registry (ADR-0009).

## Invariants — don't break
- **Secret values never leave the SecretWriter port.** External-resource secret values route through SM-API;
  issue bodies, comments, and API responses carry only names / paths / refs — never secret material. The
  domain imports no secret-backend SDK (the fence holds via `platform/secrets`).
- **A gate issue is PROSE plus two labels.** `aep:provision` marks it as a dispatch gate; `aep:dep/<slug>`
  keys it to the dependency it holds. That pair is the whole index: both the mint-time dedupe and the
  drawer's resolve are LABEL queries, never a body read (bodies are prose a human may rewrite) and never a
  title match. A gate deliberately does not carry `aep` — it is a hold on the next dispatch, never agent
  work — and it holds only DISPATCH: an open gate never blocks a run from settling.
- **A gate's provisioning run keeps an execution row.** It is the one execution kind the milestone model
  still writes: admitted when the drawer submits, finished by the readiness watcher, and its terminal state
  is what closes the gate issue.
- **The wire quirks the contract-first cutover pinned stay pinned**: wrong-kind → 400, not-found/
  not-registered → 404, in-use → 409, provision-failure → 502; get-dependency-status and list-access-requests
  return their empty-but-present shapes; a nil service 503s (the surface exists with the feature unwired).
- Platform-wide rules (tenant gate, secrets fence, feature-free domains) → [../../README.md](../../README.md).
