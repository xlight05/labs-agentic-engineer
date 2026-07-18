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
	"testing"
)

type fakeExecLocator struct {
	projectID string
	found     bool
}

func (f fakeExecLocator) LookupExecutionProject(_ context.Context, _, _ string) (string, bool, error) {
	return f.projectID, f.found, nil
}

type fakeEndpoints struct{ eps []ComponentEndpoint }

func (f fakeEndpoints) ResolveEndpoints(_ context.Context, _, _ string) ([]ComponentEndpoint, error) {
	return f.eps, nil
}

func TestValidationContext_ResolvesEndpoints(t *testing.T) {
	svc := NewContextService(
		fakeExecLocator{projectID: "proj", found: true},
		fakeEndpoints{eps: []ComponentEndpoint{
			{Component: "hello-web", URL: "https://web.example"},
			{Component: "hello-api", URL: "https://api.example"},
		}},
	)
	resp, err := svc.ValidationContext(context.Background(), "exec-1", "org")
	if err != nil {
		t.Fatalf("ValidationContext: %v", err)
	}
	if len(resp.Endpoints) != 2 || resp.Endpoints[0].Component != "hello-web" {
		t.Errorf("endpoints = %+v", resp.Endpoints)
	}
	// Credentials are no longer bundled in the context — the runner requests
	// them on demand from the sibling test-credentials endpoint.
	if resp.CriteriaPath != criteriaFilePath {
		t.Errorf("criteriaPath = %q; want %q", resp.CriteriaPath, criteriaFilePath)
	}
}

func TestValidationContext_UnknownExecutionIs404(t *testing.T) {
	svc := NewContextService(
		fakeExecLocator{found: false}, // execution not in caller's org
		fakeEndpoints{},
	)
	_, err := svc.ValidationContext(context.Background(), "exec-x", "org")
	if !errors.Is(err, ErrExecutionNotFound) {
		t.Fatalf("want ErrExecutionNotFound (→ 404), got %v", err)
	}
}
