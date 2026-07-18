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

// Consumer-side ports. dependencies/resources has an EMPTY arch-allowlist row
// (internal/arch/arch_test.go) — it imports NO other feature package. Every
// collaborator is expressed as a narrow interface here and wired concretely in
// the composition root.

import (
	"context"

	"github.com/wso2/aep/aep-api/models"
)

// externalResourceLookup is the slice of the org-level external-resource
// catalog this package reads. *repositories.ExternalResourceRepository
// satisfies it. Returns (nil, nil) when the name is not registered.
type externalResourceLookup interface {
	Get(ctx context.Context, orgID, name string) (*models.ExternalResource, error)
}

// SecretWriter is the slice of the SM-API writer the provisioner needs.
// Satisfied by *orgcreds.SMAPIWriter.
type SecretWriter interface {
	Enabled() bool
	// WriteExternalResourceSecret uploads the secret fields for a
	// (project, entity) and returns the Vault KV path the ExternalSecret
	// reads (the secretStorePath).
	WriteExternalResourceSecret(ctx context.Context, ocOrgID, projectName, entityName string, data map[string]string) (vaultKey, secretRefName string, err error)
}

// NOTE (dependency-management migration): the task-coupled ports the
// value/resource services used — TaskStore (read the component_tasks repo),
// TaskCompleter (drive ComponentTask.Status through the projector) and
// RedispatchFunc — were removed here at the merge along with their only
// consumers (external_values.go / resources_service.go, git-rm'd). Phase 6
// rebuilt the value/param surface in internal/feature/provisioning on our
// GitHub-native aep:provision funnel; the completion port it uses there is
// "close the provision issue + Funnel.Reevaluate", not a component_tasks
// projector. The org-level external-resource catalog (list/delete + the
// consumer scan for the in-use delete guard) also lives there now
// (provisioning.ExternalResourceCatalog + the design-scan consumers), reading
// *repositories.ExternalResourceRepository directly.

// DesignReader is the slice of the design store the resource provisioner reads:
// the project's authored design components, whose platform-resource entries
// carry the ClusterResourceType to provision. It deliberately returns ONLY
// models-typed data — NOT artifacts.DesignFile — so this package keeps its
// empty arch-allowlist row (no dependencies/resources → artifacts feature
// edge). The composition root adapts artifacts.ArtifactStore.ReadDesign with
// a one-line wrapper returning design.Components ((nil, nil) design ⇒ nil
// components — "no design yet").
type DesignReader interface {
	ReadDesignComponents(ctx context.Context, orgID, projectID string) ([]models.DesignComponent, error)
}
