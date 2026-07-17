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

package api

import (
	"context"

	"github.com/wso2/aep/aep-api/internal/feature/dependencies/resources"
	"github.com/wso2/aep/aep-api/internal/gen"
)

// Dependencies feature on the strict interface: the platform-resource-type
// discovery endpoint (the HTTP transport of the same data the
// list_platform_resource_types MCP tool serves). The catalog is cluster-global
// — there is nothing org-scoped to filter by — but the operation still sits
// behind the deny-by-default tenant gate like every other one (the auth
// fence). A nil catalog answers 503, mirroring the retired
// RegisterResourceTypes nil guard.

func (s *legacyHandlers) ListPlatformResourceTypes(ctx context.Context, _ gen.ListPlatformResourceTypesRequestObject) (gen.ListPlatformResourceTypesResponseObject, error) {
	if s.deps.ResourceTypeCatalog == nil {
		return nil, errServiceUnavailable("resource-type catalog is not configured")
	}
	types, err := s.deps.ResourceTypeCatalog.List(ctx)
	if err != nil {
		// The catalog reads cluster ClusterResourceTypes over OpenChoreo; a
		// failure is an upstream (data-plane) fault, not the caller's.
		return nil, errBadGateway("failed to list platform resource types")
	}
	return gen.ListPlatformResourceTypes200JSONResponse(toPlatformResourceTypeDTOs(types)), nil
}

// toPlatformResourceTypeDTOs projects the domain resource types onto the wire
// DTO: the architect-facing fields (name, description, parameters, outputs)
// minus the AEP-internal markers.
func toPlatformResourceTypeDTOs(in []resources.PlatformResourceType) []gen.PlatformResourceTypeDTO {
	out := make([]gen.PlatformResourceTypeDTO, 0, len(in))
	for _, t := range in {
		out = append(out, gen.PlatformResourceTypeDTO{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
			Outputs:     t.Outputs,
		})
	}
	return out
}
