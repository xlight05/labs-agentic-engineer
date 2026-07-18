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
	"strings"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	ocmocks "github.com/wso2/aep/aep-api/internal/clients/openchoreo/mocks"
	"github.com/wso2/aep/aep-api/models"
)

// newFakeRC returns a ResourceClientMock whose GetResource already reports a
// cut ResourceRelease named latest — the provisioner's poll loop resolves on
// the first tick.
func newFakeRC(latest string) *ocmocks.ResourceClientMock {
	return &ocmocks.ResourceClientMock{
		EnsureResourceTypeFunc: func(_ context.Context, _ string, rt *openchoreo.ResourceType) (*openchoreo.ResourceType, error) {
			return rt, nil
		},
		ApplyResourceFunc: func(_ context.Context, _ string, r *openchoreo.Resource) (*openchoreo.Resource, error) {
			return r, nil
		},
		GetResourceFunc: func(_ context.Context, _, name string) (*openchoreo.Resource, error) {
			return &openchoreo.Resource{
				Metadata: openchoreo.OCObjectMeta{Name: name},
				Status:   &openchoreo.ResourceStatus{LatestRelease: &openchoreo.ResourceLatestRelease{Name: latest}},
			}, nil
		},
		EnsureBindingFunc: func(_ context.Context, _ string, b *openchoreo.ResourceReleaseBinding) (*openchoreo.ResourceReleaseBinding, error) {
			return b, nil
		},
		DeleteBindingFunc:  func(_ context.Context, _, _ string) error { return nil },
		DeleteResourceFunc: func(_ context.Context, _, _ string) error { return nil },
	}
}

// fakeSecretWriter records SM-API writes per entity name.
type fakeSecretWriter struct {
	disabled bool
	wrote    map[string]map[string]string
}

func (s *fakeSecretWriter) Enabled() bool { return !s.disabled }

func (s *fakeSecretWriter) WriteExternalResourceSecret(_ context.Context, _, _, entityName string, data map[string]string) (string, string, error) {
	if s.wrote == nil {
		s.wrote = map[string]map[string]string{}
	}
	s.wrote[entityName] = data
	return "user-app-secrets/wc-org/cred-" + entityName, "cred-" + entityName, nil
}

// fakeLookup serves one registered external resource by name.
type fakeLookup struct{ er *models.ExternalResource }

func (f *fakeLookup) Get(_ context.Context, _, name string) (*models.ExternalResource, error) {
	if f.er != nil && f.er.Name == name {
		return f.er, nil
	}
	return nil, nil
}

func newTestProvisioner(lookup externalResourceLookup, rc openchoreo.ResourceClient, sm SecretWriter) *ExternalResourceProvisioner {
	p := NewExternalResourceProvisioner(lookup, rc, sm)
	p.pollInterval = time.Millisecond
	p.pollTimeout = time.Second
	return p
}

func TestProvision_OrchestratesResourceModel(t *testing.T) {
	t.Parallel()

	rc := newFakeRC("openweather-proj-abc123")
	sw := &fakeSecretWriter{}
	p := newTestProvisioner(nil, rc, sw)

	er := &models.ExternalResource{
		Name:             "openweather",
		ResourceTypeName: "openweather",
		ConfigKeys: []models.ConfigKey{
			{Key: "OPENWEATHER_BASE_URL", Secret: false},
			{Key: "OPENWEATHER_API_KEY", Secret: true},
		},
	}
	byEnv := map[string]EnvValues{
		"development": {
			Plain:  map[string]string{"OPENWEATHER_BASE_URL": "https://api.openweathermap.org"},
			Secret: map[string]string{"OPENWEATHER_API_KEY": "k123"},
		},
	}

	res, err := p.Provision(context.Background(), "default", "oc-org-1", "weatherproj", er, byEnv)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	// ResourceType built from the schema + ensured, under the version-pinned name.
	wantRT := openchoreo.ExternalResourceRTName("openweather")
	rtCalls := rc.EnsureResourceTypeCalls()
	if len(rtCalls) != 1 || rtCalls[0].Rt.Metadata.Name != wantRT {
		t.Fatalf("resourcetype not ensured under %q: %+v", wantRT, rtCalls)
	}
	// Resource: project-prefixed name, owner, type ref points at the versioned RT.
	applied := rc.ApplyResourceCalls()
	if len(applied) != 1 {
		t.Fatalf("want 1 ApplyResource call, got %d", len(applied))
	}
	if applied[0].R.Metadata.Name != "weatherproj-openweather" {
		t.Errorf("resource name = %q", applied[0].R.Metadata.Name)
	}
	if applied[0].R.Spec.Owner.ProjectName != "weatherproj" || applied[0].R.Spec.Type.Name != wantRT {
		t.Errorf("resource spec wrong: %+v", applied[0].R.Spec)
	}
	if res.LatestRelease != "openweather-proj-abc123" {
		t.Errorf("latestRelease = %q", res.LatestRelease)
	}

	// Secret written under the per-env "extres-" entity.
	if _, ok := sw.wrote["extres-openweather-development"]; !ok {
		t.Fatalf("secret not written per-env: %v", sw.wrote)
	}

	// One binding, pinned, with plain value + secretStorePath in env configs.
	bindings := rc.EnsureBindingCalls()
	if len(bindings) != 1 {
		t.Fatalf("want 1 binding, got %d", len(bindings))
	}
	b := bindings[0].B
	if b.Metadata.Name != "weatherproj-openweather-development" {
		t.Errorf("binding name = %q", b.Metadata.Name)
	}
	if b.Spec.ResourceRelease != "openweather-proj-abc123" {
		t.Errorf("binding not pinned: %q", b.Spec.ResourceRelease)
	}
	if b.Spec.Owner.ResourceName != "weatherproj-openweather" || b.Spec.Environment != "development" {
		t.Errorf("binding owner/env wrong: %+v", b.Spec)
	}
	var cfg map[string]string
	if err := json.Unmarshal(b.Spec.ResourceTypeEnvironmentConfigs, &cfg); err != nil {
		t.Fatalf("env configs not json: %v", err)
	}
	if cfg["OPENWEATHER_BASE_URL"] != "https://api.openweathermap.org" {
		t.Errorf("plain value missing from env configs: %v", cfg)
	}
	if cfg[openchoreo.SecretStorePathField] == "" {
		t.Errorf("secretStorePath missing from env configs: %v", cfg)
	}
	if res.BindingByEnv["development"] != "weatherproj-openweather-development" {
		t.Errorf("BindingByEnv wrong: %+v", res.BindingByEnv)
	}
}

func TestProvision_AllPlain_NoSecretWrite(t *testing.T) {
	t.Parallel()

	rc := newFakeRC("rel-1")
	sw := &fakeSecretWriter{}
	p := newTestProvisioner(nil, rc, sw)
	er := &models.ExternalResource{
		Name: "plainsvc", ResourceTypeName: "plainsvc",
		ConfigKeys: []models.ConfigKey{{Key: "BASE_URL"}},
	}
	byEnv := map[string]EnvValues{"development": {Plain: map[string]string{"BASE_URL": "https://x"}}}
	if _, err := p.Provision(context.Background(), "default", "oc-org-1", "proj", er, byEnv); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if len(sw.wrote) != 0 {
		t.Errorf("no secret should be written for an all-plain resource: %v", sw.wrote)
	}
}

func TestProvision_SecretValuesWithoutSMAPI_Fails(t *testing.T) {
	t.Parallel()

	rc := newFakeRC("rel-1")
	p := newTestProvisioner(nil, rc, &fakeSecretWriter{disabled: true})
	er := &models.ExternalResource{
		Name: "openweather", ResourceTypeName: "openweather",
		ConfigKeys: []models.ConfigKey{{Key: "OPENWEATHER_API_KEY", Secret: true}},
	}
	byEnv := map[string]EnvValues{"development": {Secret: map[string]string{"OPENWEATHER_API_KEY": "k"}}}
	if _, err := p.Provision(context.Background(), "default", "oc-org-1", "proj", er, byEnv); err == nil {
		t.Fatal("want error when SM-API is not configured but secret values exist")
	}
}

// TestAuthorWithSecretRef_UsesStagedRefNoSMWrite pins the build path's author
// half (issue #164): it authors the per-env binding pinned to the PASSED
// SecretStorePath (staged pre-tag by POST /build) and NEVER writes to SM-API.
func TestAuthorWithSecretRef_UsesStagedRefNoSMWrite(t *testing.T) {
	t.Parallel()

	rc := newFakeRC("openweather-proj-abc123")
	sw := &fakeSecretWriter{}
	p := newTestProvisioner(nil, rc, sw)

	er := &models.ExternalResource{
		Name: "openweather", ResourceTypeName: "openweather",
		ConfigKeys: []models.ConfigKey{
			{Key: "OPENWEATHER_BASE_URL", Secret: false},
			{Key: "OPENWEATHER_API_KEY", Secret: true},
		},
	}
	byEnv := map[string]PreparedEnvValues{
		"development": {
			Plain:           map[string]string{"OPENWEATHER_BASE_URL": "https://api.openweathermap.org"},
			SecretStorePath: "user-app-secrets/wc-org/staged-ref",
		},
	}

	res, err := p.AuthorWithSecretRef(context.Background(), "default", "weatherproj", er, byEnv)
	if err != nil {
		t.Fatalf("AuthorWithSecretRef: %v", err)
	}
	// The SM-API writer is NEVER touched — the secret was staged pre-tag.
	if len(sw.wrote) != 0 {
		t.Fatalf("AuthorWithSecretRef must not write to SM-API, wrote: %v", sw.wrote)
	}
	// One binding, pinned to the release, carrying the PASSED secretStorePath.
	bindings := rc.EnsureBindingCalls()
	if len(bindings) != 1 {
		t.Fatalf("want 1 binding, got %d", len(bindings))
	}
	b := bindings[0].B
	if b.Metadata.Name != "weatherproj-openweather-development" {
		t.Errorf("binding name = %q", b.Metadata.Name)
	}
	if b.Spec.ResourceRelease != "openweather-proj-abc123" {
		t.Errorf("binding not pinned: %q", b.Spec.ResourceRelease)
	}
	var cfg map[string]string
	if err := json.Unmarshal(b.Spec.ResourceTypeEnvironmentConfigs, &cfg); err != nil {
		t.Fatalf("env configs not json: %v", err)
	}
	if cfg["OPENWEATHER_BASE_URL"] != "https://api.openweathermap.org" {
		t.Errorf("plain value missing from env configs: %v", cfg)
	}
	if cfg[openchoreo.SecretStorePathField] != "user-app-secrets/wc-org/staged-ref" {
		t.Errorf("staged secretStorePath must be pinned verbatim, got %q", cfg[openchoreo.SecretStorePathField])
	}
	if res.BindingByEnv["development"] != "weatherproj-openweather-development" {
		t.Errorf("BindingByEnv wrong: %+v", res.BindingByEnv)
	}
}

func TestProvision_Validation(t *testing.T) {
	t.Parallel()

	p := newTestProvisioner(nil, newFakeRC("rel-1"), &fakeSecretWriter{})
	if _, err := p.Provision(context.Background(), "default", "oc-org-1", "proj", nil, nil); err == nil {
		t.Error("want error on nil external resource")
	}
	er := &models.ExternalResource{Name: "x", ResourceTypeName: "x", ConfigKeys: []models.ConfigKey{{Key: "K"}}}
	if _, err := p.Provision(context.Background(), "", "oc-org-1", "proj", er, nil); err == nil {
		t.Error("want error on empty orgHandle")
	}
	if _, err := p.Provision(context.Background(), "default", "oc-org-1", "", er, nil); err == nil {
		t.Error("want error on empty projectName")
	}
}

// TestStageSecrets_WritesPerEnvReturnsRefs proves the extracted SM-API-only
// write loop: each env's secrets land under the per-env "extres-" entity and
// the returned refByEnv carries ONLY the secretStorePath (a reference), while
// an env with no secrets produces no write and no ref.
func TestStageSecrets_WritesPerEnvReturnsRefs(t *testing.T) {
	t.Parallel()
	sw := &fakeSecretWriter{}
	p := newTestProvisioner(nil, newFakeRC("rel-1"), sw)
	er := &models.ExternalResource{
		Name: "stripe", ResourceTypeName: "stripe",
		ConfigKeys: []models.ConfigKey{{Key: "STRIPE_KEY", Secret: true}},
	}
	refByEnv, err := p.StageSecrets(context.Background(), "oc-org-1", "shop", er, map[string]map[string]string{
		"development": {"STRIPE_KEY": "sk_live"},
		"production":  {}, // empty → no write, no ref
	})
	if err != nil {
		t.Fatalf("stage secrets: %v", err)
	}
	if refByEnv["development"] == "" {
		t.Fatalf("want a development ref, got %v", refByEnv)
	}
	if _, ok := refByEnv["production"]; ok {
		t.Fatalf("an env with no secrets must not produce a ref: %v", refByEnv)
	}
	if got := sw.wrote["extres-stripe-development"]; got["STRIPE_KEY"] != "sk_live" {
		t.Fatalf("secret not written under the per-env entity: %v", sw.wrote)
	}
}

// Secrets present but SM-API disabled fails closed (mirrors Provision's guard).
func TestStageSecrets_SecretsWithoutSMAPIFails(t *testing.T) {
	t.Parallel()
	p := newTestProvisioner(nil, newFakeRC("rel-1"), &fakeSecretWriter{disabled: true})
	er := &models.ExternalResource{Name: "stripe"}
	if _, err := p.StageSecrets(context.Background(), "oc-org-1", "shop", er,
		map[string]map[string]string{"development": {"STRIPE_KEY": "k"}}); err == nil {
		t.Fatal("want error when SM-API disabled but secrets present")
	}
}

func TestDeprovision_DeletesBindingsThenResource(t *testing.T) {
	t.Parallel()

	rc := newFakeRC("rel-1")
	p := newTestProvisioner(nil, rc, &fakeSecretWriter{})
	if err := p.Deprovision(context.Background(), "default", "proj", "openweather", []string{"development", "production"}); err != nil {
		t.Fatalf("deprovision: %v", err)
	}
	db := rc.DeleteBindingCalls()
	if len(db) != 2 || db[0].Name != "proj-openweather-development" || db[1].Name != "proj-openweather-production" {
		t.Fatalf("binding deletes wrong: %+v", db)
	}
	dr := rc.DeleteResourceCalls()
	if len(dr) != 1 || dr[0].Name != "proj-openweather" {
		t.Fatalf("resource delete wrong: %+v", dr)
	}
}

// TestDeprovision_CollectsErrorsAndDeletesResource proves the short-circuit fix:
// a failed FIRST binding delete must not stop the second binding delete or the
// Resource delete, and the error is joined and surfaced.
func TestDeprovision_CollectsErrorsAndDeletesResource(t *testing.T) {
	t.Parallel()

	var deletedBindings, deletedResources []string
	rc := &ocmocks.ResourceClientMock{
		DeleteBindingFunc: func(_ context.Context, _, name string) error {
			deletedBindings = append(deletedBindings, name)
			if name == "proj-openweather-development" {
				return errors.New("binding stuck")
			}
			return nil
		},
		DeleteResourceFunc: func(_ context.Context, _, name string) error {
			deletedResources = append(deletedResources, name)
			return nil
		},
	}
	p := newTestProvisioner(nil, rc, &fakeSecretWriter{})
	err := p.Deprovision(context.Background(), "default", "proj", "openweather", []string{"development", "production"})
	if err == nil {
		t.Fatal("want the joined binding error surfaced")
	}
	if !strings.Contains(err.Error(), "development") {
		t.Errorf("error must name the failed env binding: %v", err)
	}
	if len(deletedBindings) != 2 {
		t.Fatalf("second binding must still be deleted after the first fails, got %v", deletedBindings)
	}
	if len(deletedResources) != 1 || deletedResources[0] != "proj-openweather" {
		t.Fatalf("Resource must be deleted despite the binding failure, got %v", deletedResources)
	}
}

func TestResolveRunnerSecrets_ReadsBindingStorePath(t *testing.T) {
	t.Parallel()

	cfg, _ := json.Marshal(map[string]string{
		"OPENWEATHER_BASE_URL":          "https://api.openweathermap.org",
		openchoreo.SecretStorePathField: "user-app-secrets/wc-org/cred-extres-openweather-development",
	})
	rc := newFakeRC("rel-1")
	rc.GetBindingFunc = func(_ context.Context, _, name string) (*openchoreo.ResourceReleaseBinding, error) {
		if name != "proj-openweather-development" {
			return nil, nil
		}
		return &openchoreo.ResourceReleaseBinding{
			Spec: openchoreo.ResourceReleaseBindingSpec{ResourceTypeEnvironmentConfigs: cfg},
		}, nil
	}
	lookup := &fakeLookup{er: &models.ExternalResource{
		Name: "openweather", ResourceTypeName: "openweather",
		ConfigKeys: []models.ConfigKey{
			{Key: "OPENWEATHER_BASE_URL"},
			{Key: "OPENWEATHER_API_KEY", Secret: true},
		},
	}}
	p := newTestProvisioner(lookup, rc, &fakeSecretWriter{})

	got, err := p.ResolveRunnerSecrets(context.Background(), "default", "proj", "development",
		[]string{"openweather", "unregistered"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 runner secret, got %+v", got)
	}
	if got[0].KVPath != "user-app-secrets/wc-org/cred-extres-openweather-development" {
		t.Errorf("KVPath = %q", got[0].KVPath)
	}
	if len(got[0].Keys) != 1 || got[0].Keys[0] != "OPENWEATHER_API_KEY" {
		t.Errorf("Keys = %v; want only the secret keys", got[0].Keys)
	}
}

func TestResolveRunnerSecrets_SkipsAllPlainAndUnprovisioned(t *testing.T) {
	t.Parallel()

	rc := newFakeRC("rel-1")
	rc.GetBindingFunc = func(_ context.Context, _, _ string) (*openchoreo.ResourceReleaseBinding, error) {
		return nil, nil // not yet provisioned
	}
	lookup := &fakeLookup{er: &models.ExternalResource{
		Name:       "plainsvc",
		ConfigKeys: []models.ConfigKey{{Key: "BASE_URL"}},
	}}
	p := newTestProvisioner(lookup, rc, &fakeSecretWriter{})
	got, err := p.ResolveRunnerSecrets(context.Background(), "default", "proj", "development", []string{"plainsvc"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("all-plain resource must be skipped, got %+v", got)
	}
}
