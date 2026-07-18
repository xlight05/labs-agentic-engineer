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

package provisioning

import (
	"context"
	"fmt"

	"github.com/wso2/aep/aep-api/internal/dependencies"
)

// DependencyStatus is the masked provisioning status of a dependency: the
// derived state, a readiness flag, and the output NAMES only. Output values +
// secret references are never surfaced — secrets live only in SM-API / the
// OC-rendered Secret (the no-secrets invariant).
type DependencyStatus struct {
	Status  string   `json:"status"`  // provisioning | ready | unknown
	Ready   bool     `json:"ready"`   //
	Outputs []string `json:"outputs"` // output names only (masked)
}

// Status reads the dependency's per-env OC binding and reports its readiness +
// output names. External and platform-resource bindings share one naming form
// (ExternalResourceBindingName), so one read path serves both. A missing binding
// (not provisioned yet) reports "unknown", not an error — the status endpoint
// stays 200 mid-provision.
func (s *Service) Status(ctx context.Context, orgID, projectID, depName, env string) (*DependencyStatus, error) {
	if env == "" {
		env = defaultEnv
	}
	bindingName := dependencies.ExternalResourceBindingName(projectID, depName, env)
	b, err := s.bindings.GetBinding(ctx, orgID, bindingName)
	if err != nil {
		return nil, fmt.Errorf("provisioning: read binding %q: %w", bindingName, err)
	}
	if b == nil {
		return &DependencyStatus{Status: "unknown", Ready: false, Outputs: []string{}}, nil
	}
	out := &DependencyStatus{Ready: b.IsReady(), Outputs: []string{}}
	if out.Ready {
		out.Status = "ready"
	} else {
		out.Status = "provisioning"
	}
	if b.Status != nil {
		for _, o := range b.Status.Outputs {
			out.Outputs = append(out.Outputs, o.Name) // MASKED: name only, never value/secretRef.
		}
	}
	return out, nil
}
