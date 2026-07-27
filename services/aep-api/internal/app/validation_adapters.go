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

package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/wso2/aep/aep-api/internal/delivery"

	"github.com/wso2/aep/aep-api/internal/delivery/validation"
	"github.com/wso2/aep/aep-api/internal/gen"
	authn "github.com/wso2/aep/aep-api/internal/platform/auth"
	"github.com/wso2/aep/aep-api/internal/spec"
)

// validationCriteriaPath is the acceptance-oracle file the validation minter
// reads (kept in sync with validation.criteriaFilePath, which is unexported).
const validationCriteriaPath = "specs/validation/validation-criteria.json"

// validationCriteria adapts the Files API to validation's CriteriaReader port:
// it reads specs/validation/validation-criteria.json at HEAD, reporting a file
// absent at HEAD as found=false with no error (the design agent has not authored
// the oracle yet). Keeps the files feature out of the validation package.
type validationCriteria struct {
	files spec.FilesService
}

func (a validationCriteria) ReadValidationCriteria(ctx context.Context, orgID, projectID string) (raw []byte, found bool, err error) {
	fc, rerr := a.files.Read(ctx, orgID, projectID, validationCriteriaPath)
	if rerr != nil {
		if errors.Is(rerr, spec.ErrFileNotFound) {
			return nil, false, nil
		}
		return nil, false, rerr
	}
	return []byte(fc.Content), true, nil
}

// validationExecLocator adapts the executions repository to validation's
// ExecutionLocator port: it resolves a runner's execution id to its project,
// org-fenced (GetByIDScoped returns nil for a different org — the tenant fence).
type validationExecLocator struct {
	repo delivery.ExecutionRepository
}

func (l validationExecLocator) LookupExecutionProject(ctx context.Context, orgHandle, executionID string) (string, bool, error) {
	row, err := l.repo.GetByIDScoped(ctx, orgHandle, executionID)
	if err != nil {
		return "", false, err
	}
	if row == nil {
		return "", false, nil
	}
	return row.ProjectID, true, nil
}

// componentDeployLister is the ListDeployments slice of ComponentService the
// endpoint resolver needs (satisfied structurally by *component.componentService).
type componentDeployLister interface {
	ListDeployments(ctx context.Context, orgName, projectName, componentName string) (*gen.DeploymentList, error)
}

// validationEndpointResolver adapts the design read + ComponentService to
// validation's EndpointResolver port: the deployed external URL (first HTTP
// external endpoint from the OpenChoreo ReleaseBinding) per design component. A
// component with no resolved URL yet is skipped; a ListDeployments ERROR is
// propagated (it is an infra failure, not "undeployed" — see ResolveEndpoints).
type validationEndpointResolver struct {
	store *spec.ArtifactStore
	comp  componentDeployLister
}

func (r validationEndpointResolver) ResolveEndpoints(ctx context.Context, orgHandle, projectID string) ([]validation.ComponentEndpoint, error) {
	// This runs inside the runner's validation-context request, whose ctx carries
	// the runner's inbound task JWT (aud git-service). Without this marker the OC
	// transport would forward that token to OpenChoreo, which rejects it (401) —
	// so every ListDeployments below would fail and we'd resolve zero endpoints.
	// Act as the BFF's own service identity (org resolved via namespace), exactly
	// like the MCP handler and the async watchers.
	ctx = authn.WithServiceIdentity(ctx)
	df, err := r.store.ReadDesign(ctx, orgHandle, projectID)
	if err != nil {
		return nil, err
	}
	var out []validation.ComponentEndpoint
	for i := range df.Components {
		name := df.Components[i].Name
		// A never-deployed component is an EMPTY 200 list (ListReleaseBindings
		// filters by component), so an error here is genuinely exceptional
		// (auth/network/OC down) — propagate it instead of silently resolving
		// fewer endpoints than the deployed system actually has.
		list, lerr := r.comp.ListDeployments(ctx, orgHandle, projectID, name)
		if lerr != nil {
			return nil, fmt.Errorf("list deployments for %s: %w", name, lerr)
		}
		// No resolved URL yet (empty list / no external endpoint) — skip.
		if url := firstDeploymentURL(list); url != "" {
			out = append(out, validation.ComponentEndpoint{Component: name, URL: url})
		}
	}
	return out, nil
}

// firstDeploymentURL returns the first non-empty deployed endpoint URL.
func firstDeploymentURL(list *gen.DeploymentList) string {
	if list == nil {
		return ""
	}
	for i := range list.Items {
		if u := list.Items[i].EndpointURL; u != "" {
			return u
		}
	}
	return ""
}

// mockValidationCredentials is the v1 test-credential provider: it returns a
// shared mock account (admin/admin) for any request, because programmatic user
// provisioning is not implemented yet. The account is marked Mock so the runner
// can note in its report that auth-gated criteria ran against a stand-in login,
// and the request hints (role/purpose/username) are ignored for now — they are
// the contract a real per-project provider will honor later. admin/admin is the
// OpenChoreo/Backstage portal admin used as a stand-in; it is not guaranteed to
// be a valid end-user of a generated app.
type mockValidationCredentials struct{}

func (mockValidationCredentials) RequestCredentials(_ context.Context, _, _ string, _ validation.CredentialRequest) (validation.TestCredential, error) {
	return validation.TestCredential{
		Username: "admin",
		Password: "admin",
		Mock:     true,
		Note:     "user provisioning not implemented; shared mock credentials — any role currently returns the same account",
	}, nil
}
