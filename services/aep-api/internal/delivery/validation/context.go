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

package validation

import (
	"context"
	"errors"
	"fmt"
)

// ErrExecutionNotFound means the runner's execution id does not resolve to a
// task in the caller's org — the endpoint surfaces it as 404.
var ErrExecutionNotFound = errors.New("validation: execution not found for org")

// ComponentEndpoint is one deployed component's reachable URL, the runner's
// e2e target.
type ComponentEndpoint struct {
	Component string `json:"component"`
	URL       string `json:"url"`
}

// ValidationContextResponse is the secure runtime-inputs payload the runner
// fetches at dispatch time (never carried in the public issue): the deployed
// endpoint URLs and the criteria file path. Test credentials are NOT bundled
// here — the runner requests them on demand (only when a criterion needs a
// login) from the sibling test-credentials endpoint (credentials.go).
type ValidationContextResponse struct {
	Endpoints    []ComponentEndpoint `json:"endpoints"`
	CriteriaPath string              `json:"criteriaPath"`
}

// ExecutionLocator resolves a runner's execution id to its project, fenced by
// the caller's org (the INT-6 tenant fence). repositories.ExecutionRepository's
// GetByIDScoped satisfies the adapter wired at the composition root.
type ExecutionLocator interface {
	LookupExecutionProject(ctx context.Context, orgHandle, executionID string) (projectID string, found bool, err error)
}

// EndpointResolver resolves a project's deployed component endpoint URLs (first
// external URL per component, from OpenChoreo ReleaseBindings). The composition
// root adapts the design-component read + ComponentService.ListDeployments so
// this feature needs neither the artifacts nor the component edge.
type EndpointResolver interface {
	ResolveEndpoints(ctx context.Context, orgHandle, projectID string) ([]ComponentEndpoint, error)
}

// ContextService answers the runner's validation-context fetch.
type ContextService struct {
	execs     ExecutionLocator
	endpoints EndpointResolver
}

// NewContextService wires the validation-context service.
func NewContextService(execs ExecutionLocator, endpoints EndpointResolver) *ContextService {
	return &ContextService{execs: execs, endpoints: endpoints}
}

// ValidationContext resolves the runtime inputs for a runner's execution: the
// deployed endpoint URLs. orgHandle is the verified caller org (the auth layer
// fences it against the execution).
func (s *ContextService) ValidationContext(ctx context.Context, executionID, orgHandle string) (*ValidationContextResponse, error) {
	projectID, found, err := s.execs.LookupExecutionProject(ctx, orgHandle, executionID)
	if err != nil {
		return nil, fmt.Errorf("validation context: resolve execution: %w", err)
	}
	if !found {
		return nil, ErrExecutionNotFound
	}
	eps, err := s.endpoints.ResolveEndpoints(ctx, orgHandle, projectID)
	if err != nil {
		return nil, fmt.Errorf("validation context: resolve endpoints: %w", err)
	}
	return &ValidationContextResponse{
		Endpoints:    eps,
		CriteriaPath: criteriaFilePath,
	}, nil
}
