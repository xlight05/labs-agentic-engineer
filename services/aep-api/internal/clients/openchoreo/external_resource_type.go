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

package openchoreo

import (
	"encoding/json"
	"fmt"
)

// ExternalResourceRTTemplateVersion is bumped whenever BuildExternalResourceType's
// emitted manifest shape changes in a way that an already-applied ResourceType
// would NOT reflect. ResourceTypes are immutable AND shared by name across
// projects, and EnsureResourceType reuses an existing RT on 409-conflict — so
// without a version in the name, a stale RT authored by older code (e.g. v1's
// buggy `readyWhen` that gated on a foreign CRD's `.status.conditions` and
// threw "no such key") is silently reused. Pinning the version into the RT
// name makes a generator change author a fresh, correctly-shaped RT instead.
//
//	v1 → original (secret readyWhen gated on the ExternalSecret Ready condition; broken)
//	v2 → secret readyWhen = ${true} (an ES-only Resource isn't Ready by default; OC's
//	     applied.<id>.status snapshot is stale for foreign CRDs)
const ExternalResourceRTTemplateVersion = 2

// rtTemplateVersionLabel records the generator version on the RT for debugging.
// aep-prefixed (not openchoreo.dev/*) like the other aep-authored labels in
// constants.go, so it never collides with OC's own label validation.
const rtTemplateVersionLabel = "aep.openchoreo.dev/rt-template-version"

// ExternalResourceRTName is the cluster ResourceType name for an external
// resource's logical RT name (the registry's ResourceTypeName), pinned to the
// current template version so a generator change never collides with — and
// silently reuses — a stale RT of the same logical name.
func ExternalResourceRTName(base string) string {
	return fmt.Sprintf("%s-t%d", base, ExternalResourceRTTemplateVersion)
}

// ExternalResourceConfigKey is one env-var key in an external resource's
// schema. Mirrors the agents/BFF spec.ConfigKey without importing models
// (keeps the OC client leaf-level).
type ExternalResourceConfigKey struct {
	Key    string
	Secret bool
}

// Per-env value field names the BFF supplies on the binding's
// resourceTypeEnvironmentConfigs (and the ResourceType's environmentConfigs
// schema). Plain keys are supplied verbatim by their own key name; all secret
// values live in a single SM-API secret addressed by SecretStorePathField,
// read per-property by the ExternalSecret.
const (
	// SecretStorePathField is the environmentConfig holding the SM-API/OpenBao
	// KV path where this external resource's secret values live for the
	// environment.
	SecretStorePathField = "secretStorePath"
	// extResourceConfigMapID / extResourceSecretID are the ResourceType
	// manifest ids.
	extResourceConfigMapID = "config"
	extResourceSecretID    = "secret"
)

// retainPolicyDelete is the ResourceType/binding retainPolicy that cascades
// the rendered DP objects (ConfigMap/ExternalSecret) on delete.
const retainPolicyDelete = "Delete"

// BuildExternalResourceType turns an external resource's config key schema
// into a per-resource ResourceType (the "external SaaS dependency pattern" —
// no upstream sample exists, so this is modeled on the shipped `postgres`
// ResourceType + the `${dataplane.secretStore}` ExternalSecret form).
//
// It renders, per consuming environment:
//   - a ConfigMap holding the plain (non-secret) values, fed from
//     environmentConfigs (one ConfigMap key per plain config key; always emitted
//     so the resources list is non-empty — OC requires MinItems=1);
//   - an ESO ExternalSecret (only when ≥1 secret key) that pulls the secret
//     values from the data plane's store (`secretStoreRef: ${dataplane.secretStore}`)
//     at the per-env SM-API path (environmentConfigs.secretStorePath), one
//     `data[]` entry per secret key (property == the key name);
//   - explicit `readyWhen` (a ConfigMap/ExternalSecret-only Resource is not Ready
//     by default — without it the consumer gates forever);
//   - `outputs[]` mapping each key to a consumer-bindable value: plain →
//     configMapKeyRef, secret → secretKeyRef. The consumer binds these via
//     Workload.spec.dependencies.resources[].envBindings.
//
// `name` is the external resource's RT name (e.g. "salesforce"); the caller
// pins the template version via ExternalResourceRTName and sets the namespace
// via EnsureResourceType. ResourceTypes are effectively immutable — a changed
// key schema must use a new name.
func BuildExternalResourceType(name string, keys []ExternalResourceConfigKey) (*ResourceType, error) {
	if name == "" {
		return nil, fmt.Errorf("external resourcetype: empty name")
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("external resourcetype %q: at least one config key required", name)
	}

	var plain, secret []ExternalResourceConfigKey
	for _, k := range keys {
		if k.Key == "" {
			return nil, fmt.Errorf("external resourcetype %q: empty config key", name)
		}
		if k.Secret {
			secret = append(secret, k)
		} else {
			plain = append(plain, k)
		}
	}

	// environmentConfigs schema: each plain key (string) + the secret store path
	// (string) when there are secrets. These are the per-env values the BFF
	// supplies on the binding's resourceTypeEnvironmentConfigs.
	props := map[string]any{}
	for _, k := range plain {
		props[k.Key] = map[string]any{"type": "string"}
	}
	if len(secret) > 0 {
		props[SecretStorePathField] = map[string]any{"type": "string"}
	}
	envConfigSchema := &SchemaSection{OpenAPIV3Schema: map[string]any{
		"type":       "object",
		"properties": props,
	}}

	// ── ConfigMap (always) — non-secret values. ─────────────────────────────
	cmData := map[string]any{}
	for _, k := range plain {
		cmData[k.Key] = "${environmentConfigs." + k.Key + "}"
	}
	if len(cmData) == 0 {
		// Keep the ConfigMap non-empty when the resource is all-secret.
		cmData["_ready"] = "true"
	}
	cmTemplate, err := toRawTemplate(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "${metadata.name}-" + extResourceConfigMapID,
			"namespace": "${metadata.namespace}",
			"labels":    "${metadata.labels}",
		},
		"data": cmData,
	})
	if err != nil {
		return nil, err
	}
	resources := []ResourceTypeManifest{{
		ID:        extResourceConfigMapID,
		ReadyWhen: "${true}", // an applied ConfigMap is ready immediately
		Template:  cmTemplate,
	}}

	outputs := make([]ResourceTypeOutput, 0, len(keys))
	for _, k := range plain {
		outputs = append(outputs, ResourceTypeOutput{
			Name:            k.Key,
			ConfigMapKeyRef: &OCKeyRef{Name: "${metadata.name}-" + extResourceConfigMapID, Key: k.Key},
		})
	}

	// ── ExternalSecret (only when there are secrets). ───────────────────────
	if len(secret) > 0 {
		esData := make([]map[string]any, 0, len(secret))
		for _, k := range secret {
			esData = append(esData, map[string]any{
				"secretKey": k.Key,
				"remoteRef": map[string]any{
					"key":      "${environmentConfigs." + SecretStorePathField + "}",
					"property": k.Key,
				},
			})
		}
		esTemplate, terr := toRawTemplate(map[string]any{
			"apiVersion": "external-secrets.io/v1",
			"kind":       "ExternalSecret",
			"metadata": map[string]any{
				"name":      "${metadata.name}-" + extResourceSecretID,
				"namespace": "${metadata.namespace}",
				"labels":    "${metadata.labels}",
			},
			"spec": map[string]any{
				"refreshInterval": "1m",
				"secretStoreRef": map[string]any{
					"name": "${dataplane.secretStore}",
					"kind": "ClusterSecretStore",
				},
				"target": map[string]any{
					"name":           "${metadata.name}-" + extResourceSecretID,
					"creationPolicy": "Owner",
				},
				"data": esData,
			},
		})
		if terr != nil {
			return nil, terr
		}
		resources = append(resources, ResourceTypeManifest{
			ID: extResourceSecretID,
			// readyWhen MUST be set (an ExternalSecret-only Resource isn't Ready by
			// default). We do NOT gate on the ExternalSecret's own Ready condition:
			// OC's `applied.<id>.status` snapshot does not reflect ESO's live status
			// for a foreign CRD (it reads empty/stale → the CEL strands the binding).
			// `${true}` makes the binding Ready once applied; the binding's outputs
			// still resolve the Secret, and ESO materialises it in ~1s — well before
			// the consumer (gated separately by the config-collection task) renders.
			ReadyWhen: "${true}",
			Template:  esTemplate,
		})
		for _, k := range secret {
			outputs = append(outputs, ResourceTypeOutput{
				Name:         k.Key,
				SecretKeyRef: &OCKeyRef{Name: "${metadata.name}-" + extResourceSecretID, Key: k.Key},
			})
		}
	}

	return &ResourceType{
		APIVersion: ocResourceAPIVersion,
		Kind:       kindResourceType,
		Metadata: OCObjectMeta{
			Name:   name,
			Labels: map[string]string{rtTemplateVersionLabel: fmt.Sprintf("%d", ExternalResourceRTTemplateVersion)},
		},
		Spec: ResourceTypeSpec{
			EnvironmentConfigs: envConfigSchema,
			RetainPolicy:       retainPolicyDelete,
			Outputs:            outputs,
			Resources:          resources,
		},
	}, nil
}

func toRawTemplate(m map[string]any) (json.RawMessage, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("external resourcetype: marshal template: %w", err)
	}
	return json.RawMessage(b), nil
}
