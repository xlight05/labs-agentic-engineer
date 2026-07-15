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

package build

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wso2/aep/aep-api/models"
)

var ctx = context.Background()

// ----- coordinator fakes -----------------------------------------------------

// recordingStager captures the secret map + dep it was handed and returns a
// canned reference-per-env — proving the coordinator hands the raw secret to
// the stager (SM-API) and threads back only the reference.
type recordingStager struct {
	refByEnv    map[string]string
	lastSecrets map[string]map[string]string
	lastDep     string
}

func (s *recordingStager) StageExternalSecrets(_ context.Context, _, _, _, depName string, secretsByEnv map[string]map[string]string) (map[string]string, error) {
	s.lastDep = depName
	s.lastSecrets = secretsByEnv
	return s.refByEnv, nil
}

// (fakeDesign — the canned design-component reader whose ConfigKey.Secret flags
// are the secret-vs-nonsecret source of truth the coordinator splits on — is
// declared in preflight_test.go.)

// stripeExternal is a service component with one external dependency whose
// schema marks STRIPE_KEY secret and STRIPE_ORG non-secret.
func stripeExternal() []models.DesignComponent {
	return []models.DesignComponent{{
		Name:          "o",
		ComponentType: models.ComponentTypeService,
		Dependencies: []models.Dependency{{
			Kind: models.DependencyKindExternal,
			Name: "stripe",
			Config: []models.ConfigKey{
				{Key: "STRIPE_KEY", Secret: true},
				{Key: "STRIPE_ORG", Secret: false},
			},
		}},
	}}
}

type recordingSpec struct {
	calls []specCall
	err   error // when set, every CollectSpec call fails with it
}

type specCall struct {
	component, dep, url string
	raw                 []byte
}

func (s *recordingSpec) CollectSpec(_ context.Context, _, _, component, dep string, raw []byte, url string) (string, error) {
	s.calls = append(s.calls, specCall{component: component, dep: dep, url: url, raw: raw})
	if s.err != nil {
		return "", s.err
	}
	return "components/" + component + "/dependencies/" + dep + ".openapi.yaml", nil
}

type recordingAuth struct {
	calls int
	err   error
}

func (a *recordingAuth) DeriveEndUserAuthAtHead(context.Context, string, string) error {
	a.calls++
	return a.err
}

// ----- BuildProvisionInputs --------------------------------------------------

func TestBuildProvisionInputs_StagesSecretsAndSplits(t *testing.T) {
	stager := &recordingStager{refByEnv: map[string]string{"development": "sm://acme/shop/stripe"}}
	c := &InputsCoordinator{stager: stager, design: fakeDesign{comps: stripeExternal()}}
	pins, fails, err := c.BuildProvisionInputs(ctx, "acme", "acme", "shop", []BuildInputItem{
		{Component: "o", Dependency: "stripe", Kind: "external-config",
			Values: []ConfigValue{{Key: "STRIPE_KEY", Value: "sk_live"}, {Key: "STRIPE_ORG", Value: "org_1"}}}})
	require.NoError(t, err)
	require.Empty(t, fails)
	require.Len(t, pins, 1)
	require.Equal(t, map[string]string{"STRIPE_ORG": "org_1"}, pins[0].Config) // non-secret only
	require.Equal(t, map[string]string{"development": "sm://acme/shop/stripe"}, pins[0].SecretRefByEnv)
	require.Equal(t, map[string]string{"STRIPE_KEY": "sk_live"}, stager.lastSecrets["development"]) // secret staged, not returned
}

// A platform-resource input carries its params + approval through verbatim and
// never touches the secret stager.
func TestBuildProvisionInputs_PlatformResourcePassThrough(t *testing.T) {
	stager := &recordingStager{}
	c := &InputsCoordinator{stager: stager, design: fakeDesign{comps: nil}}
	pins, fails, err := c.BuildProvisionInputs(ctx, "acme", "acme", "shop", []BuildInputItem{
		{Component: "api", Dependency: "orders-db", Kind: "platform-resource",
			Parameters: map[string]any{"instances": 2, "storage": "10Gi"}, Approved: true}})
	require.NoError(t, err)
	require.Empty(t, fails)
	require.Len(t, pins, 1)
	require.Equal(t, map[string]any{"instances": 2, "storage": "10Gi"}, pins[0].Parameters)
	require.True(t, pins[0].Approved)
	require.Nil(t, stager.lastSecrets) // stager untouched for platform-resource
}

// ----- ApplyPreTag -----------------------------------------------------------

// Pre-tag fans out: one CollectSpec per external-spec input (content → rawSpec,
// url → specURL), and the end-user-auth derivation runs exactly ONCE.
func TestApplyPreTag_FansOut(t *testing.T) {
	spec := &recordingSpec{}
	auth := &recordingAuth{}
	c := &InputsCoordinator{spec: spec, auth: auth}
	fails, err := c.ApplyPreTag(ctx, "acme", "shop", []BuildInputItem{
		{Component: "o", Dependency: "stripe", Kind: "external-spec", SpecContent: "openapi: 3.0.0"},
		{Component: "o", Dependency: "weather", Kind: "external-spec", SpecURL: "https://x/openapi.yaml"},
		{Component: "o", Dependency: "orders-db", Kind: "platform-resource"},
	})
	require.NoError(t, err)
	require.Empty(t, fails)
	require.Len(t, spec.calls, 2) // only the two external-spec inputs
	require.Equal(t, 1, auth.calls)
	require.Equal(t, []byte("openapi: 3.0.0"), spec.calls[0].raw)
	require.Empty(t, spec.calls[0].url)
	require.Equal(t, "https://x/openapi.yaml", spec.calls[1].url)
	require.Nil(t, spec.calls[1].raw)
}

// An auth-derivation conflict propagates as the error (the handler maps it to
// 409) — it is NOT a per-input failure.
func TestApplyPreTag_AuthConflictReturnsError(t *testing.T) {
	auth := &recordingAuth{err: ErrEndUserAuthConflict}
	c := &InputsCoordinator{spec: &recordingSpec{}, auth: auth}
	_, err := c.ApplyPreTag(ctx, "acme", "shop", nil)
	require.ErrorIs(t, err, ErrEndUserAuthConflict)
}

// When a spec collection fails the build is aborting, so auth derivation must
// NOT run (it would commit to HEAD for a build that cuts no tag) and the spec
// failures must be returned intact rather than masked by an auth error.
func TestApplyPreTag_SpecFailureSkipsAuthDerivation(t *testing.T) {
	spec := &recordingSpec{err: errors.New("fetch failed")}
	auth := &recordingAuth{}
	c := &InputsCoordinator{spec: spec, auth: auth}
	fails, err := c.ApplyPreTag(ctx, "acme", "shop", []BuildInputItem{
		{Component: "o", Dependency: "stripe", Kind: "external-spec", SpecURL: "https://x/openapi.yaml"},
	})
	require.NoError(t, err)
	require.Len(t, fails, 1)
	require.Equal(t, "stripe", fails[0].Dependency)
	require.Equal(t, 0, auth.calls) // auth derivation skipped on spec failure
}
