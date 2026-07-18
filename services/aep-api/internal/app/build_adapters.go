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
	"github.com/wso2/aep/aep-api/internal/delivery/build"
	"github.com/wso2/aep/aep-api/internal/delivery/devflow"
	"github.com/wso2/aep/aep-api/internal/dependencies"
	"github.com/wso2/aep/aep-api/internal/dependencies/provisioning"
	"github.com/wso2/aep/aep-api/internal/spec"
	"github.com/wso2/aep/aep-api/models"
)

// buildSpecTagger adapts spec.SaveSpec onto the build feature's
// SpecTagger port. Errors pass through unwrapped — the build handler unpacks
// *spec.SpecValidationError into the 422 detail.
type buildSpecTagger struct {
	art spec.ArtifactService
}

func (t buildSpecTagger) TagSpec(ctx context.Context, orgID, projectID string) (*spec.SpecSaveResult, error) {
	return t.art.SaveSpec(ctx, orgID, projectID, spec.SaveRequest{Message: "Build"})
}

// designAuthDeriver / designExternalRegistrar are the composition root's narrow
// consumer views of the concrete *design service — the two pre-tag steps the
// thin POST /build path reuses (issue #164). The design package no longer
// exports an interface (its read HTTP surface was retired); *designService
// satisfies these structurally, so app wires the concrete value straight in.
type designAuthDeriver interface {
	DeriveEndUserAuthAtHead(ctx context.Context, orgID, projectID string) error
}

type designExternalRegistrar interface {
	RegisterExternalResources(ctx context.Context, orgID, projectID string) error
}

// buildAuthDeriver adapts design's DeriveEndUserAuthAtHead onto the build
// feature's AuthDeriver port, translating design's domain sentinels into the
// build-local ones the handler maps to 409 / 503 (build cannot import design —
// arch allowlist). Everything else passes through so the handler 500s it.
type buildAuthDeriver struct {
	svc designAuthDeriver
}

func (d buildAuthDeriver) DeriveEndUserAuthAtHead(ctx context.Context, orgID, projectID string) error {
	err := d.svc.DeriveEndUserAuthAtHead(ctx, orgID, projectID)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, spec.ErrEndUserAuthConflict):
		return fmt.Errorf("%w: %v", build.ErrEndUserAuthConflict, err)
	case errors.Is(err, spec.ErrResourceCatalogUnavailable):
		return fmt.Errorf("%w: %v", build.ErrResourceCatalogUnavailable, err)
	default:
		return err
	}
}

// buildSecretStager adapts the external-resource provisioner's SM-API-only
// StageSecrets onto the build feature's SecretStager port. The dependency name
// is the registered external-resource name (registerExternalResources keys the
// catalog on dep.Name), so a name-only ExternalResource is all StageSecrets
// needs to form the per-env secret entity. orgID is unused — the SM-API write
// keys on ocOrgID.
type buildSecretStager struct {
	prov *dependencies.ExternalResourceProvisioner
}

func (s buildSecretStager) StageExternalSecrets(ctx context.Context, _, ocOrgID, projectID, depName string, secretsByEnv map[string]map[string]string) (map[string]string, error) {
	return s.prov.StageSecrets(ctx, ocOrgID, projectID, &models.ExternalResource{Name: depName}, secretsByEnv)
}

// buildProvisionStatus adapts provisioning.Service.Status onto the build
// preflight's ProvisionStatusReader port. It collapses the provisioning
// tri-state onto the preflight bool: a dependency is "already handled" (Ready ==
// true, so the drawer does NOT re-ask for it) whenever its status is anything
// other than "unknown". This is intentionally NOT DependencyStatus.Ready, which
// is false for BOTH "unknown" (nothing started) AND "provisioning" (in-flight) —
// returning .Ready would re-ask a dependency that is already being provisioned.
// A Status error surfaces the item (safe direction: preflight over-asks rather
// than silently dropping a dependency).
type buildProvisionStatus struct {
	svc *provisioning.Service
}

func (b buildProvisionStatus) Ready(ctx context.Context, orgID, projectID, depName string) (bool, error) {
	st, err := b.svc.Status(ctx, orgID, projectID, depName, "")
	if err != nil {
		return false, err
	}
	return st.Status != "unknown", nil
}

// buildProvisioner adapts the design + provisioning features onto devflow's
// BuildProvisioner port (issue #164) — the dev workflow's provisioning step.
// devflow imports neither feature (that would cycle), so the mapping between
// devflow.ProvisionInput ⇄ provisioning.BuildProvisionInput and their failure
// twins lives here at the composition root. RegisterExternalResources runs FIRST
// so the external deps are in the catalog before ProvisionForBuild's
// EnsureProvisionIssues / catalog.Get read them. orgID doubles as the OC org
// handle == the SM-API org id (the build path stages secrets under the org
// handle — Task 2/3 precedent).
type buildProvisioner struct {
	design designExternalRegistrar
	prov   *provisioning.Service
}

func (b buildProvisioner) ProvisionForBuild(ctx context.Context, orgID, projectID, tag string, inputs []delivery.ProvisionInput) ([]devflow.ProvisionFailure, error) {
	if err := b.design.RegisterExternalResources(ctx, orgID, projectID); err != nil {
		return nil, err
	}
	mapped := make([]provisioning.BuildProvisionInput, 0, len(inputs))
	for _, in := range inputs {
		mapped = append(mapped, provisioning.BuildProvisionInput{
			Component:      in.Component,
			Dependency:     in.Dependency,
			Kind:           in.Kind,
			Config:         in.Config,
			SecretRefByEnv: in.SecretRefByEnv,
			Parameters:     in.Parameters,
			Approved:       in.Approved,
		})
	}
	fails, err := b.prov.ProvisionForBuild(ctx, orgID, orgID, projectID, tag, mapped)
	if err != nil {
		return nil, err
	}
	out := make([]devflow.ProvisionFailure, 0, len(fails))
	for _, f := range fails {
		out = append(out, devflow.ProvisionFailure{Component: f.Component, Dependency: f.Dependency, Reason: f.Reason})
	}
	return out, nil
}

// providerBuildTrigger adapts build.StartProjectBuild onto provisioning's
// ProviderBuildTrigger port (issue #164, Task 4): the automated org-service
// visibility flow kicks a not-yet-published provider project's build so it
// deploys (and publishes org-wide). StartProjectBuild is idempotent — an
// already-running provider build is treated as success.
type providerBuildTrigger struct {
	build *build.Service
}

func (t providerBuildTrigger) TriggerBuild(ctx context.Context, orgID, projectID string) error {
	return t.build.StartProjectBuild(ctx, orgID, projectID)
}
