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
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/spec"
)

// EnvValues are the user-supplied values for one environment, split by the
// resource's registered schema.
type EnvValues struct {
	Plain  map[string]string // non-secret key → value
	Secret map[string]string // secret key → value
}

// ExternalResourceProvisioner authors the OpenChoreo Resource model for a
// registered external resource in a project: SM-API value write →
// ResourceType (get-or-create) → Resource (controller cuts a ResourceRelease)
// → per-env ResourceReleaseBinding pinned to status.latestRelease.
type ExternalResourceProvisioner struct {
	lookup externalResourceLookup
	rc     openchoreo.ResourceClient
	sm     SecretWriter

	// pollInterval / pollTimeout bound the wait for the controller to cut the
	// ResourceRelease (Resources have no AutoDeploy — we poll status.latestRelease
	// via openchoreo.WaitForLatestRelease).
	pollInterval time.Duration
	pollTimeout  time.Duration
}

// NewExternalResourceProvisioner wires the provisioner. lookup is only read
// by ResolveRunnerSecrets; Provision/Deprovision receive the resource row from
// the caller.
func NewExternalResourceProvisioner(lookup externalResourceLookup, rc openchoreo.ResourceClient, sm SecretWriter) *ExternalResourceProvisioner {
	return &ExternalResourceProvisioner{
		lookup:       lookup,
		rc:           rc,
		sm:           sm,
		pollInterval: 2 * time.Second,
		pollTimeout:  60 * time.Second,
	}
}

// ProvisionResult reports what was authored.
type ProvisionResult struct {
	ResourceName  string            // the per-project Resource name
	LatestRelease string            // the pinned ResourceRelease
	BindingByEnv  map[string]string // env → binding name
}

// Provision authors (idempotently) the external resource's OC Resource model
// for a project across the given environments. `orgHandle` is the OC namespace
// the CRs live in; `ocOrgID` is the SM-API org id. Per env it writes the
// secret values (when any) to SM-API and pins the per-env binding with the
// resulting secretStorePath + plain values.
func (p *ExternalResourceProvisioner) Provision(
	ctx context.Context,
	orgHandle, ocOrgID, projectName string,
	er *ExternalResource,
	byEnv map[string]EnvValues,
) (*ProvisionResult, error) {
	if er == nil {
		return nil, fmt.Errorf("external resources: nil resource")
	}
	if orgHandle == "" || projectName == "" {
		return nil, fmt.Errorf("external resources: orgHandle and projectName required")
	}

	// 1. ResourceType (get-or-create; immutable once created). The cluster RT
	// name is pinned to the generator's template version — a template change
	// authors a fresh RT instead of silently reusing a stale same-named one on
	// 409-conflict.
	rtName := openchoreo.ExternalResourceRTName(er.ResourceTypeName)
	rt, err := openchoreo.BuildExternalResourceType(rtName, toRTConfigKeys(er.ConfigKeys))
	if err != nil {
		return nil, fmt.Errorf("external resources: build resourcetype: %w", err)
	}
	if _, err := p.rc.EnsureResourceType(ctx, orgHandle, rt); err != nil {
		return nil, fmt.Errorf("external resources: ensure resourcetype %q: %w", rtName, err)
	}

	// 2. Resource (one per project; controller cuts the ResourceRelease).
	res := buildExternalResource(projectName, er, rtName)
	applied, err := p.rc.ApplyResource(ctx, orgHandle, res)
	if err != nil {
		return nil, fmt.Errorf("external resources: apply resource: %w", err)
	}

	// 3. Wait for status.latestRelease (no AutoDeploy → BFF pins it). On a
	// reconcile of an existing Resource `applied` already carries the stale
	// pre-reconcile release, so wait for a CHANGE off it; on a create it is
	// empty and this degrades to wait-for-nonempty.
	latest, err := openchoreo.WaitForReleaseChange(ctx, p.rc, orgHandle, res.Metadata.Name, openchoreo.ReleaseName(applied), p.pollInterval, p.pollTimeout)
	if err != nil {
		return nil, fmt.Errorf("external resources: %w", err)
	}

	// 4. Stage every env's secrets to SM-API up front (the same write the thin
	// build path reuses), then author the per-env binding pinned to
	// latestRelease off the returned secretStorePath refs.
	secretsByEnv := make(map[string]map[string]string, len(byEnv))
	for env, vals := range byEnv {
		if len(vals.Secret) > 0 {
			secretsByEnv[env] = vals.Secret
		}
	}
	refByEnv, err := p.StageSecrets(ctx, ocOrgID, projectName, er, secretsByEnv)
	if err != nil {
		return nil, err
	}
	result := &ProvisionResult{ResourceName: res.Metadata.Name, LatestRelease: latest, BindingByEnv: map[string]string{}}
	for env, vals := range byEnv {
		binding, berr := buildExternalResourceBinding(projectName, er.Name, env, latest, refByEnv[env], vals.Plain)
		if berr != nil {
			return nil, berr
		}
		if _, err := p.rc.EnsureBinding(ctx, orgHandle, binding); err != nil {
			return nil, fmt.Errorf("external resources: ensure binding for env %q: %w", env, err)
		}
		result.BindingByEnv[env] = binding.Metadata.Name
	}
	return result, nil
}

// PreparedEnvValues are one environment's already-resolved binding inputs for
// the build author path: the non-secret values (Plain) and the SM-API
// secretStorePath the secret bundle was ALREADY staged under (pre-tag, by
// POST /build). No secret VALUE is carried — only the reference.
type PreparedEnvValues struct {
	Plain           map[string]string
	SecretStorePath string
}

// AuthorWithSecretRef authors (idempotently) the external resource's OC Resource
// model for a project from ALREADY-STAGED secret references — the author half of
// Provision the thin POST /build path uses (issue #164). It mirrors Provision's
// steps 1-3 (ensure ResourceType → apply Resource → wait for the release change)
// but its step 4 pins each per-env binding to the PASSED SecretStorePath instead
// of writing secrets to SM-API — so it never touches p.sm. `orgHandle` is the OC
// namespace the CRs live in.
func (p *ExternalResourceProvisioner) AuthorWithSecretRef(
	ctx context.Context,
	orgHandle, projectName string,
	er *ExternalResource,
	byEnv map[string]PreparedEnvValues,
) (*ProvisionResult, error) {
	if er == nil {
		return nil, fmt.Errorf("external resources: nil resource")
	}
	if orgHandle == "" || projectName == "" {
		return nil, fmt.Errorf("external resources: orgHandle and projectName required")
	}

	// 1. ResourceType (get-or-create; immutable once created).
	rtName := openchoreo.ExternalResourceRTName(er.ResourceTypeName)
	rt, err := openchoreo.BuildExternalResourceType(rtName, toRTConfigKeys(er.ConfigKeys))
	if err != nil {
		return nil, fmt.Errorf("external resources: build resourcetype: %w", err)
	}
	if _, err := p.rc.EnsureResourceType(ctx, orgHandle, rt); err != nil {
		return nil, fmt.Errorf("external resources: ensure resourcetype %q: %w", rtName, err)
	}

	// 2. Resource (one per project; controller cuts the ResourceRelease).
	res := buildExternalResource(projectName, er, rtName)
	applied, err := p.rc.ApplyResource(ctx, orgHandle, res)
	if err != nil {
		return nil, fmt.Errorf("external resources: apply resource: %w", err)
	}

	// 3. Wait for status.latestRelease to change off the pre-reconcile release.
	latest, err := openchoreo.WaitForReleaseChange(ctx, p.rc, orgHandle, res.Metadata.Name, openchoreo.ReleaseName(applied), p.pollInterval, p.pollTimeout)
	if err != nil {
		return nil, fmt.Errorf("external resources: %w", err)
	}

	// 4. Author the per-env binding pinned to latestRelease off the PASSED
	// secretStorePath (no SM-API write — the secret was staged pre-tag).
	result := &ProvisionResult{ResourceName: res.Metadata.Name, LatestRelease: latest, BindingByEnv: map[string]string{}}
	for env, vals := range byEnv {
		binding, berr := buildExternalResourceBinding(projectName, er.Name, env, latest, vals.SecretStorePath, vals.Plain)
		if berr != nil {
			return nil, berr
		}
		if _, err := p.rc.EnsureBinding(ctx, orgHandle, binding); err != nil {
			return nil, fmt.Errorf("external resources: ensure binding for env %q: %w", env, err)
		}
		result.BindingByEnv[env] = binding.Metadata.Name
	}
	return result, nil
}

// StageSecrets writes each env's secret values to SM-API and returns the
// secretStorePath reference per env — ONLY the SM-API write, no OC Resource or
// binding authoring. It is the pre-tag half of Provision the thin POST /build
// path reuses (issue #164): the build stages refs before the tag-cut, and the
// workflow's provisioning step (Task 3) authors bindings from the same refs.
// An env whose secret map is empty yields no write and no ref. Fails closed
// when secrets exist but SM-API is disabled (mirrors Provision's guard). The
// returned map holds references, never secret VALUES.
func (p *ExternalResourceProvisioner) StageSecrets(
	ctx context.Context,
	ocOrgID, projectName string,
	er *ExternalResource,
	secretsByEnv map[string]map[string]string,
) (map[string]string, error) {
	if er == nil {
		return nil, fmt.Errorf("external resources: nil resource")
	}
	refByEnv := map[string]string{}
	for env, secret := range secretsByEnv {
		if len(secret) == 0 {
			continue
		}
		if !p.sm.Enabled() {
			return nil, fmt.Errorf("external resources: SM-API not configured but resource %q has secret values", er.Name)
		}
		entity := externalResourceSecretEntity(er.Name, env)
		path, _, werr := p.sm.WriteExternalResourceSecret(ctx, ocOrgID, projectName, entity, secret)
		if werr != nil {
			return nil, fmt.Errorf("external resources: write secret for env %q: %w", env, werr)
		}
		refByEnv[env] = path
	}
	return refByEnv, nil
}

// Deprovision is the 2-step delete: per-env bindings first (their retainPolicy
// cascades the DP ConfigMap/ExternalSecret), then the Resource (its finalizer
// blocks until bindings are gone). The ResourceType is long-lived — never
// deleted.
func (p *ExternalResourceProvisioner) Deprovision(ctx context.Context, orgHandle, projectName, name string, envs []string) error {
	resourceName := ExternalResourceName(projectName, name)
	// Collect binding-delete errors and continue (mirror the platform twin):
	// one failed env must not short-circuit and leave the Resource behind.
	var errs []error
	for _, env := range envs {
		if err := p.rc.DeleteBinding(ctx, orgHandle, ExternalResourceBindingName(projectName, name, env)); err != nil {
			errs = append(errs, fmt.Errorf("delete binding (%s/%s): %w", name, env, err))
		}
	}
	// Delete the Resource regardless of per-env binding failures.
	if err := p.rc.DeleteResource(ctx, orgHandle, resourceName); err != nil {
		errs = append(errs, fmt.Errorf("delete resource (%s): %w", name, err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("external resources: %w", errors.Join(errs...))
	}
	return nil
}

// ExternalResourceRunnerSecret describes one external resource's per-env
// secret bundle for the coding-agent runner: the SM-API vault KV path + the
// secret key list. The dispatcher materialises these into the runner pod (the
// agent integration-tests against the live service during implementation).
type ExternalResourceRunnerSecret struct {
	KVPath string   // user-app-secrets/<orgNs>/<secretRefName>
	Keys   []string // the secret config keys (== env var names + the SM-API properties)
}

// ResolveRunnerSecrets returns the per-run-ExternalSecret inputs for the
// external resources a task depends on, for one environment: the secret key
// list (from the org catalog) + the SM-API vault path (read back off the
// resource's per-env ResourceReleaseBinding, where Provision pinned it).
// Resources with no secret keys (or not yet provisioned) are skipped.
func (p *ExternalResourceProvisioner) ResolveRunnerSecrets(ctx context.Context, orgHandle, projectName, env string, names []string) ([]ExternalResourceRunnerSecret, error) {
	out := make([]ExternalResourceRunnerSecret, 0, len(names))
	for _, name := range names {
		er, err := p.lookup.Get(ctx, orgHandle, name)
		if err != nil || er == nil {
			continue
		}
		var keys []string
		for _, k := range er.ConfigKeys {
			if k.Secret {
				keys = append(keys, k.Key)
			}
		}
		if len(keys) == 0 {
			continue
		}
		b, err := p.rc.GetBinding(ctx, orgHandle, ExternalResourceBindingName(projectName, name, env))
		if err != nil || b == nil {
			continue
		}
		var cfg map[string]string
		if len(b.Spec.ResourceTypeEnvironmentConfigs) > 0 {
			_ = json.Unmarshal(b.Spec.ResourceTypeEnvironmentConfigs, &cfg)
		}
		path := cfg[openchoreo.SecretStorePathField]
		if path == "" {
			continue
		}
		out = append(out, ExternalResourceRunnerSecret{KVPath: path, Keys: keys})
	}
	return out, nil
}

// ---- pure builders (unit-tested) -------------------------------------------

// externalResourceSecretEntity is the SM-API EntityName for an external
// resource's per-env secret bundle — one SM-API secret per (project, resource,
// env).
func externalResourceSecretEntity(name, env string) string { return "extres-" + name + "-" + env }

// buildExternalResource references the version-pinned cluster RT name (rtName),
// not the logical er.ResourceTypeName, so the Resource binds to the freshly
// authored RT rather than a stale same-named one.
func buildExternalResource(projectName string, er *ExternalResource, rtName string) *openchoreo.Resource {
	return &openchoreo.Resource{
		Metadata: openchoreo.OCObjectMeta{Name: ExternalResourceName(projectName, er.Name)},
		Spec: openchoreo.ResourceSpec{
			Owner: openchoreo.ResourceOwner{ProjectName: projectName},
			Type:  openchoreo.ResourceTypeRef{Kind: "ResourceType", Name: rtName},
		},
	}
}

func buildExternalResourceBinding(projectName, name, env, latestRelease, secretStorePath string, plain map[string]string) (*openchoreo.ResourceReleaseBinding, error) {
	cfg := map[string]string{}
	for k, v := range plain {
		cfg[k] = v
	}
	if secretStorePath != "" {
		cfg[openchoreo.SecretStorePathField] = secretStorePath
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("external resources: marshal env configs: %w", err)
	}
	resourceName := ExternalResourceName(projectName, name)
	return &openchoreo.ResourceReleaseBinding{
		Metadata: openchoreo.OCObjectMeta{Name: ExternalResourceBindingName(projectName, name, env)},
		Spec: openchoreo.ResourceReleaseBindingSpec{
			Owner:                          openchoreo.ResourceReleaseBindingOwner{ProjectName: projectName, ResourceName: resourceName},
			Environment:                    env,
			ResourceRelease:                latestRelease,
			ResourceTypeEnvironmentConfigs: json.RawMessage(raw),
		},
	}, nil
}

func toRTConfigKeys(in []spec.ConfigKey) []openchoreo.ExternalResourceConfigKey {
	out := make([]openchoreo.ExternalResourceConfigKey, 0, len(in))
	for _, k := range in {
		out = append(out, openchoreo.ExternalResourceConfigKey{Key: k.Key, Secret: k.Secret})
	}
	return out
}
