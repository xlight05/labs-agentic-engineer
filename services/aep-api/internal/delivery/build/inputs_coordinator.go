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
	"strings"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/spec"
)

// defaultInputEnv is the environment the build drawer's single value set is
// staged under. The drawer collects one set of values (not per-env), so we pin
// them to "development" — the codebase's default environment (see the resources
// provisioner tests, which key every byEnv on "development").
const defaultInputEnv = "development"

// InputsCoordinator turns the build drawer's inputs into the pre-tag side
// effects a build requires: it collects external specs and derives end-user
// auth BEFORE the tag-cut (ApplyPreTag), and splits/stages external-config
// values into non-secret config + SM-API secret references, passing platform
// resource params + approvals through (BuildProvisionInputs). It is a thin
// orchestrator over four ports — it holds no state and authors no OC resources
// (that is the workflow's job, Task 3).
type InputsCoordinator struct {
	spec   SpecCollector
	auth   AuthDeriver
	stager SecretStager
	design PreflightDesignReader
}

// NewInputsCoordinator wires the coordinator.
func NewInputsCoordinator(spec SpecCollector, auth AuthDeriver, stager SecretStager, design PreflightDesignReader) *InputsCoordinator {
	return &InputsCoordinator{spec: spec, auth: auth, stager: stager, design: design}
}

// ApplyPreTag runs the side effects that MUST land on HEAD before the tag-cut
// captures the spec: every external-spec input is collected (content →
// rawSpec, else url → specURL), then the end-user-auth derivation runs exactly
// once. A per-input CollectSpec failure is reported as an InputFailure (the
// handler returns {failures} and cuts no tag); an auth-derivation error
// (conflict / catalog-unavailable) propagates as err for the handler to map to
// 409 / 503.
//
// When any spec collection fails the build is already aborting, so we return
// the failures WITHOUT deriving auth — deriving would commit to HEAD for a
// build that never cuts a tag, and an auth error would mask the spec failures
// the user actually needs to see. The next Build re-derives idempotently.
func (c *InputsCoordinator) ApplyPreTag(ctx context.Context, orgID, projectID string, inputs []BuildInputItem) ([]InputFailure, error) {
	var failures []InputFailure
	for _, in := range inputs {
		if in.Kind != "external-spec" {
			continue
		}
		var raw []byte
		if in.SpecContent != "" {
			raw = []byte(in.SpecContent)
		}
		if _, err := c.spec.CollectSpec(ctx, orgID, projectID, in.Component, in.Dependency, raw, in.SpecURL); err != nil {
			failures = append(failures, InputFailure{
				Component:  in.Component,
				Dependency: in.Dependency,
				Kind:       in.Kind,
				Reason:     err.Error(),
			})
		}
	}
	if len(failures) > 0 {
		return failures, nil
	}
	if err := c.auth.DeriveEndUserAuthAtHead(ctx, orgID, projectID); err != nil {
		return failures, err
	}
	return failures, nil
}

// BuildProvisionInputs turns the drawer inputs into the provision payload the
// version's gate resolver consumes. For external-config it splits each input's values into non-secret
// Config and a secret map (keyed by the design's ConfigKey.Secret flag at
// HEAD), stages the secret map to SM-API, and lands only the returned reference
// in SecretRefByEnv — a raw secret value never enters a ProvisionInput. For
// platform-resource / org-service it passes Parameters + Approved through.
// external-spec inputs carry no provision payload (handled in ApplyPreTag).
func (c *InputsCoordinator) BuildProvisionInputs(ctx context.Context, orgID, ocOrgID, projectID string, inputs []BuildInputItem) ([]delivery.ProvisionInput, []InputFailure, error) {
	secretKeys, err := c.secretKeysByDep(ctx, orgID, projectID)
	if err != nil {
		return nil, nil, err
	}

	var (
		out      []delivery.ProvisionInput
		failures []InputFailure
	)
	for _, in := range inputs {
		switch in.Kind {
		case "external-config":
			pin, err := c.externalConfigInput(ctx, orgID, ocOrgID, projectID, in, secretKeys[strings.ToLower(in.Dependency)])
			if err != nil {
				return nil, nil, err
			}
			out = append(out, pin)
		case "platform-resource", "org-service":
			out = append(out, delivery.ProvisionInput{
				Component:  in.Component,
				Dependency: in.Dependency,
				Kind:       in.Kind,
				Parameters: in.Parameters,
				Approved:   in.Approved,
			})
		case "external-spec":
			// Collected in ApplyPreTag; no provision payload.
		}
	}
	return out, failures, nil
}

// externalConfigInput splits one external-config input into non-secret config +
// staged secret references, consulting isSecret (key → secret flag from the
// design) to route each value.
func (c *InputsCoordinator) externalConfigInput(ctx context.Context, orgID, ocOrgID, projectID string, in BuildInputItem, isSecret map[string]bool) (delivery.ProvisionInput, error) {
	config := map[string]string{}
	secret := map[string]string{}
	for _, v := range in.Values {
		if isSecret[v.Key] {
			secret[v.Key] = v.Value
			continue
		}
		config[v.Key] = v.Value
	}

	pin := delivery.ProvisionInput{
		Component:  in.Component,
		Dependency: in.Dependency,
		Kind:       in.Kind,
		Config:     config,
	}
	if len(secret) > 0 {
		refByEnv, err := c.stager.StageExternalSecrets(ctx, orgID, ocOrgID, projectID, in.Dependency,
			map[string]map[string]string{defaultInputEnv: secret})
		if err != nil {
			return delivery.ProvisionInput{}, err
		}
		pin.SecretRefByEnv = refByEnv
	}
	return pin, nil
}

// secretKeysByDep reads the design at HEAD and returns, per external dependency
// name (lowercased), the map of config key → secret flag — the source of truth
// for the secret/non-secret split. It unions each external's config across
// every declaring component with SECRET WINNING on conflict, via the shared
// spec.UnionExternalConfigKeys — the exact same classifier the provision +
// runner-secret paths use — so a key marked secret by ANY component is never
// staged as a plaintext ConfigMap value because a different component declared
// it plain. A nil design reader (degraded/absent) yields an empty map (the
// handler never reaches here without a design).
func (c *InputsCoordinator) secretKeysByDep(ctx context.Context, orgID, projectID string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	if c.design == nil {
		return out, nil
	}
	comps, err := c.design.ReadDesignComponents(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	for name, cfg := range spec.UnionExternalConfigKeys(comps) {
		flags := map[string]bool{}
		for _, k := range cfg {
			flags[k.Key] = k.Secret
		}
		out[strings.ToLower(name)] = flags
	}
	return out, nil
}
