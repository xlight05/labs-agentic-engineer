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

package mcpdiscovery

// Consumer-side ports for the MCP discovery surface. The parent `dependencies`
// package owns only this umbrella surface; it imports its child
// `dependencies/resources` (for PlatformResourceType — an allowlisted
// parent→child edge) and otherwise depends on collaborators through narrow
// interfaces wired concretely at the composition root (app.Build, D5). Each is
// satisfied structurally by an existing type:
//
//   - ExternalResourceReader ← *repositories.ExternalResourceRepository (A3)
//   - OrgEndpointLister       ← *endpoints.Catalog (C1)
//   - ResourceTypeLister      ← *resources.ResourceTypeCatalog (C3)

import (
	"context"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/dependencies"
	"github.com/wso2/aep/aep-api/models"
)

// ExternalResourceReader is the read slice of the org external-resource catalog
// the MCP surface exposes (list every registered external resource, get one by
// name). Get returns (nil, nil) when the name is not registered.
type ExternalResourceReader interface {
	List(ctx context.Context, orgID string) ([]models.ExternalResource, error)
	Get(ctx context.Context, orgID, name string) (*models.ExternalResource, error)
}

// OrgEndpointLister is the read slice of the org endpoint catalog — the
// published-service targets an `org-service` dependency can point at (List),
// plus each one resolved with the provider's repo coordinates and a
// discovered OpenAPI contract (ListResolved) for the A3 MCP tool
// (list_org_component_endpoints).
type OrgEndpointLister interface {
	List(ctx context.Context, orgHandle string) ([]openchoreo.WorkloadEndpointInfo, error)
	ListResolved(ctx context.Context, orgHandle string) ([]dependencies.OrgComponentEndpoint, error)
}

// ResourceTypeLister is the read slice of the platform resource-type catalog —
// the installed cluster ResourceTypes a platform-resource dependency references.
type ResourceTypeLister interface {
	List(ctx context.Context) ([]dependencies.PlatformResourceType, error)
}

// RemoteGitReader reads an org's OWN GitHub repos over the REST API (Contents +
// Code Search, no clone) for endpoint spec discovery — the two MCP tools an
// agent uses to read a provider's OpenAPI file straight from its repo. Both
// methods take ocOrgID (the verified MCP claim, never a tool parameter) and
// MUST refuse (ErrOwnerNotInOrg) any `owner` that is not the org credential's
// GitHub account, so a caller in one org can never read another org's repos.
// Satisfied by *RemoteGitClient (remote_git.go).
type RemoteGitReader interface {
	GetFileContents(ctx context.Context, ocOrgID, owner, repo, path, ref string) (*RemoteGitFile, error)
	SearchCode(ctx context.Context, ocOrgID, owner, repo, query string) ([]RemoteGitSearchHit, error)
}
