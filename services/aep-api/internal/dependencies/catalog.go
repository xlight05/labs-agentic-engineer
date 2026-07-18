// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package dependencies

import (
	"context"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
)

// Catalog enumerates the service endpoints published by the Workloads in an
// org namespace — the dynamic source that supersedes any static catalog. The
// architect discovers `org-service` targets here (via the MCP
// list_org_endpoints tool, a later task), and both design resolution and the
// consumer-wiring gate on each endpoint's namespace visibility.
//
// `orgHandle` is the OC namespace the org's Workloads live in — the whole
// dependencies feature treats `orgHandle` as the OC namespace, and this
// catalog follows the same convention. (Cloud namespace resolution, when
// added, is uniform across dependencies, not special-cased here.)
type Catalog struct {
	rc openchoreo.ResourceClient
	// repos/design are optional resolver collaborators (see resolve.go). Nil
	// when a caller wires only the raw catalog reads; the resolver then degrades
	// its availability computation rather than panicking.
	repos  RepoLocator
	design DesignBundleReader
}

// NewCatalog wires a Catalog over the given OC resource client. A nil rc
// leaves the Catalog "not wired" — every read then degrades to a documented
// no-op (nil, nil) rather than a nil-pointer panic. Optional CatalogOptions
// wire the spec-discovery resolver's collaborators (repo locator, design
// reader); omit them for the read-only catalog surface.
func NewCatalog(rc openchoreo.ResourceClient, opts ...CatalogOption) *Catalog {
	c := &Catalog{rc: rc}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// List returns every provider-side endpoint across the org's Workloads (one
// row per endpoint, carrying owner project/component + visibility). Returns
// nil when the catalog is not wired (nil receiver or nil client).
func (c *Catalog) List(ctx context.Context, orgHandle string) ([]openchoreo.WorkloadEndpointInfo, error) {
	if c == nil || c.rc == nil {
		return nil, nil
	}
	return c.rc.ListWorkloadEndpoints(ctx, orgHandle)
}

// ResolveNamespaceVisible finds the namespace-visible endpoint published under
// the given `org-service` name (== the provider component name). Returns
// ok=false when no component with that name publishes a namespace-visible
// endpoint (i.e. it doesn't exist or is project-only) — that's the
// `unresolved` case. When a component publishes several namespace-visible
// endpoints, an HTTP one wins (a service exposes a single API endpoint in
// practice).
func (c *Catalog) ResolveNamespaceVisible(ctx context.Context, orgHandle, name string) (openchoreo.WorkloadEndpointInfo, bool, error) {
	infos, err := c.List(ctx, orgHandle)
	if err != nil {
		return openchoreo.WorkloadEndpointInfo{}, false, err
	}
	var fallback *openchoreo.WorkloadEndpointInfo
	for i := range infos {
		e := &infos[i]
		if e.Component != name || !e.NamespaceVisible() {
			continue
		}
		if e.Type == "HTTP" {
			return *e, true, nil
		}
		if fallback == nil {
			fallback = e
		}
	}
	if fallback != nil {
		return *fallback, true, nil
	}
	return openchoreo.WorkloadEndpointInfo{}, false, nil
}

// ResolveProjectEndpoint finds the endpoint owned by {project, ocComponent}
// within the org namespace — the same-project sibling case (visibility
// `project`). Unlike ResolveNamespaceVisible this applies NO visibility
// filter: project visibility is always implicit, so a same-project sibling
// resolves even when it publishes only `project` (no namespace/external).
// When the component publishes several endpoints, an HTTP one wins (services
// expose a single API endpoint in practice); otherwise the first match is the
// fallback. ok=false means no endpoint owned by that project+component yet.
func (c *Catalog) ResolveProjectEndpoint(ctx context.Context, orgHandle, project, ocComponent string) (openchoreo.WorkloadEndpointInfo, bool, error) {
	infos, err := c.List(ctx, orgHandle)
	if err != nil {
		return openchoreo.WorkloadEndpointInfo{}, false, err
	}
	var fallback *openchoreo.WorkloadEndpointInfo
	for i := range infos {
		e := &infos[i]
		if e.Project != project || e.Component != ocComponent {
			continue
		}
		if e.Type == "HTTP" {
			return *e, true, nil
		}
		if fallback == nil {
			fallback = e
		}
	}
	if fallback != nil {
		return *fallback, true, nil
	}
	return openchoreo.WorkloadEndpointInfo{}, false, nil
}

// IsNamespaceVisible reports whether an org-service named `name` is published
// namespace-visible in the org — the resolution gate for design display. Used
// to mark an `org-service` dependency `resolved` (true) vs `unresolved`
// (false). Errors are surfaced so the caller can degrade.
func (c *Catalog) IsNamespaceVisible(ctx context.Context, orgHandle, name string) (bool, error) {
	_, ok, err := c.ResolveNamespaceVisible(ctx, orgHandle, name)
	return ok, err
}

// FindByComponent returns the catalog row owned by a component named `name`,
// regardless of visibility — the provider lookup. The org-service name a
// consumer references is the OC component name (catalog key); this resolves
// it to the provider row so the request flow can derive the provider project
// and the component's app path. When the component publishes several
// endpoints, an HTTP one wins (services expose a single API endpoint in
// practice); otherwise the first match is the fallback. ok=false means no
// component with that name (the `not-found` case — not requestable).
func (c *Catalog) FindByComponent(ctx context.Context, orgHandle, name string) (openchoreo.WorkloadEndpointInfo, bool, error) {
	infos, err := c.List(ctx, orgHandle)
	if err != nil {
		return openchoreo.WorkloadEndpointInfo{}, false, err
	}
	var fallback *openchoreo.WorkloadEndpointInfo
	for i := range infos {
		e := &infos[i]
		if e.Component != name {
			continue
		}
		if e.Type == "HTTP" {
			return *e, true, nil
		}
		if fallback == nil {
			fallback = e
		}
	}
	if fallback != nil {
		return *fallback, true, nil
	}
	return openchoreo.WorkloadEndpointInfo{}, false, nil
}

// ExistsAnyVisibility reports whether ANY endpoint in the catalog is owned by
// a component named `name`, regardless of visibility. It distinguishes
// "published only project-only" (exists — requestable, blocked/
// access-required) from "no such component at all" (not-found). Used to
// compute the `reason` for an unresolved org-service dependency. Errors are
// surfaced so the caller can degrade (best-effort: leave the reason empty).
func (c *Catalog) ExistsAnyVisibility(ctx context.Context, orgHandle, name string) (bool, error) {
	infos, err := c.List(ctx, orgHandle)
	if err != nil {
		return false, err
	}
	for i := range infos {
		if infos[i].Component == name {
			return true, nil
		}
	}
	return false, nil
}
