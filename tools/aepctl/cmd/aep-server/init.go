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

package main

import (
	"fmt"
	"time"

	"google.golang.org/grpc"

	"github.com/wso2/aep/aepctl/internal/adminpb"
	"github.com/wso2/aep/aepctl/internal/bootstrap"
	"github.com/wso2/aep/aepctl/internal/openbao"
)

func (s *server) Init(req *adminpb.InitRequest, stream grpc.ServerStreamingServer[adminpb.InitEvent]) error {
	ctx := stream.Context()

	progress := func(msg string) error {
		return stream.Send(&adminpb.InitEvent{
			Payload: &adminpb.InitEvent_Progress{Progress: msg},
		})
	}
	fatal := func(msg string) error {
		_ = stream.Send(&adminpb.InitEvent{
			Payload: &adminpb.InitEvent_Error{Error: msg},
		})
		return nil
	}

	if req.AnthropicApiKey == "" {
		return fatal("anthropic_api_key is required")
	}

	postgresPassword := req.PostgresPassword
	if postgresPassword == "" {
		pw, err := bootstrap.GeneratePassword(32)
		if err != nil {
			return fatal(fmt.Sprintf("generate postgres password: %v", err))
		}
		postgresPassword = pw
	}

	signingKey, err := bootstrap.GenerateRSAPrivateKey()
	if err != nil {
		return fatal(fmt.Sprintf("generate signing key: %v", err))
	}

	if err := progress("Waiting for OpenBao to be reachable..."); err != nil {
		return err
	}
	if err := openbao.WaitForReachable(ctx, s.openbaoAddr, 3*time.Minute); err != nil {
		return fatal(fmt.Sprintf("OpenBao not reachable at %s: %v", s.openbaoAddr, err))
	}

	health, _, err := openbao.Req(ctx, "GET", s.openbaoAddr, "", "/v1/sys/health", nil)
	if err != nil {
		return fatal(fmt.Sprintf("check OpenBao health: %v", err))
	}
	initialized, _ := health["initialized"].(bool)
	sealed, _ := health["sealed"].(bool)

	if initialized && sealed {
		return fatal("OpenBao is initialised but sealed — run `aep openbao unseal` first")
	}

	if initialized && !sealed {
		if err := progress("OpenBao already initialised and unsealed — skipping init"); err != nil {
			return err
		}
		return stream.Send(&adminpb.InitEvent{
			Payload: &adminpb.InitEvent_Complete{
				Complete: &adminpb.InitComplete{},
			},
		})
	}

	// --- Full init path ---

	if err := progress("Initialising OpenBao (5 shares, threshold 3)..."); err != nil {
		return err
	}
	initResp, err := openbao.Must(ctx, "POST", s.openbaoAddr, "", "/v1/sys/init", map[string]interface{}{
		"secret_shares":    5,
		"secret_threshold": 3,
	})
	if err != nil {
		return fatal(fmt.Sprintf("init OpenBao: %v", err))
	}

	rootToken, _ := initResp["root_token"].(string)
	rawKeys, _ := initResp["keys"].([]interface{})
	unsealKeys := make([]string, len(rawKeys))
	for i, k := range rawKeys {
		unsealKeys[i], _ = k.(string)
	}

	if err := progress("Unsealing OpenBao..."); err != nil {
		return err
	}
	for i := 0; i < 3; i++ {
		if _, err := openbao.Must(ctx, "PUT", s.openbaoAddr, "", "/v1/sys/unseal", map[string]interface{}{
			"key": unsealKeys[i],
		}); err != nil {
			return fatal(fmt.Sprintf("unseal (key %d): %v", i+1, err))
		}
	}

	if err := progress("Enabling KV-v2 secrets engine at secret/..."); err != nil {
		return err
	}
	if _, err := openbao.Must(ctx, "POST", s.openbaoAddr, rootToken, "/v1/sys/mounts/secret", map[string]interface{}{
		"type":    "kv",
		"options": map[string]interface{}{"version": "2"},
	}); err != nil {
		return fatal(fmt.Sprintf("enable KV-v2: %v", err))
	}

	if err := progress("Writing secrets to OpenBao..."); err != nil {
		return err
	}
	oauthStateKey, err := bootstrap.GeneratePassword(32)
	if err != nil {
		return fatal(fmt.Sprintf("generate oauth state key: %v", err))
	}

	// Generate a unique secret for every confidential Thunder OAuth client.
	// These are injected into the Thunder bootstrap script by `aep thunder setup`.
	thunderClientNames := []string{
		"oc-workload-publisher",
		"oc-observer-reader",
		"aep-api-client",
		"bff-git-service",
		"bff-remote-worker",
		"local-dev-seeder",
		"system-client",
		// SRE/RCA agent service-account identity. Consumed by the
		// openchoreo-observability-plane RCA agent (rca.oauth.clientId +
		// rca-agent-secret OAUTH_CLIENT_SECRET) once `aep sre install` runs.
		"openchoreo-rca-agent",
	}
	// fixedClientSecrets: clients whose secret an OpenChoreo component bakes in
	// as a fixed default and cannot be told a random value. The OC
	// dockerfile-builder's generate-workload-cr step authenticates as
	// openchoreo-workload-publisher-client using this literal (baked into the
	// ClusterWorkflowTemplate); a random secret here 401s the workload publish
	// (build fails at generate-workload-cr). Mirrors setup.sh's Thunder bootstrap.
	fixedClientSecrets := map[string]string{
		"oc-workload-publisher": "openchoreo-workload-publisher-secret",
	}
	thunderClientSecrets := make(map[string]string, len(thunderClientNames))
	for _, name := range thunderClientNames {
		if fixed, ok := fixedClientSecrets[name]; ok {
			thunderClientSecrets[name] = fixed
			continue
		}
		s, err := bootstrap.GeneratePassword(32)
		if err != nil {
			return fatal(fmt.Sprintf("generate thunder client secret %s: %v", name, err))
		}
		thunderClientSecrets[name] = s
	}

	agentsJWTSecret, err := bootstrap.GeneratePassword(32)
	if err != nil {
		return fatal(fmt.Sprintf("generate agents JWT secret: %v", err))
	}

	// GitHub webhook HMAC secret. aep-api registers it on each repo's webhook
	// and validates inbound X-Hub-Signature-256 with it (fail-closed). Generated
	// here so it's identical in local + prod and never a plaintext chart value.
	webhookSecret, err := bootstrap.GeneratePassword(32)
	if err != nil {
		return fatal(fmt.Sprintf("generate webhook secret: %v", err))
	}

	// OpenSearch admin credentials for the observability plane (Observer +
	// OpenSearch + logs-adapter). Seeded here — while the root token is still
	// held — because `aep sre install` runs after init (root token revoked) and
	// only reads these via ESO. The aep-secret-reader policy (secret/data/aep/*)
	// already covers these paths, so no OpenBao role change is needed.
	openSearchPassword, err := bootstrap.GeneratePassword(24)
	if err != nil {
		return fatal(fmt.Sprintf("generate opensearch password: %v", err))
	}

	secrets := []struct{ path, value string }{
		{"aep/anthropic-api-key", req.AnthropicApiKey},
		{"aep/postgres-password", postgresPassword},
		{"aep/task-signing-key", signingKey},
		{"aep/oauth-state-key", oauthStateKey},
		{"aep/agents-jwt-secret", agentsJWTSecret},
		{"aep/webhook-secret", webhookSecret},
		{"aep/opensearch-username", "admin"},
		{"aep/opensearch-password", openSearchPassword},
	}
	for _, name := range thunderClientNames {
		secrets = append(secrets, struct{ path, value string }{
			"aep/thunder-clients/" + name, thunderClientSecrets[name],
		})
	}
	for _, sec := range secrets {
		if _, err := openbao.Must(ctx, "PUT", s.openbaoAddr, rootToken, "/v1/secret/data/"+sec.path, map[string]interface{}{
			"data": map[string]interface{}{"value": sec.value},
		}); err != nil {
			return fatal(fmt.Sprintf("write %s: %v", sec.path, err))
		}
		if err := progress(fmt.Sprintf("  wrote secret/data/%s", sec.path)); err != nil {
			return err
		}
	}

	if err := progress("Configuring Kubernetes auth method..."); err != nil {
		return err
	}
	if _, err := openbao.Must(ctx, "POST", s.openbaoAddr, rootToken, "/v1/sys/auth/kubernetes", map[string]interface{}{
		"type": "kubernetes",
	}); err != nil {
		return fatal(fmt.Sprintf("enable kubernetes auth: %v", err))
	}
	if _, err := openbao.Must(ctx, "PUT", s.openbaoAddr, rootToken, "/v1/auth/kubernetes/config", map[string]interface{}{
		"kubernetes_host": "https://kubernetes.default.svc.cluster.local:443",
	}); err != nil {
		return fatal(fmt.Sprintf("configure kubernetes auth: %v", err))
	}

	if err := progress(fmt.Sprintf("Creating ESO auth role (SA: %s/%s)...", s.esoNamespace, s.esoSA)); err != nil {
		return err
	}
	if _, err := openbao.Must(ctx, "PUT", s.openbaoAddr, rootToken, "/v1/sys/policies/acl/aep-secret-reader", map[string]interface{}{
		"policy": `path "secret/data/aep/*" { capabilities = ["read"] }`,
	}); err != nil {
		return fatal(fmt.Sprintf("create policy: %v", err))
	}
	if _, err := openbao.Must(ctx, "PUT", s.openbaoAddr, rootToken, "/v1/auth/kubernetes/role/eso-reader", map[string]interface{}{
		"bound_service_account_names":      []string{s.esoSA},
		"bound_service_account_namespaces": []string{s.esoNamespace},
		"policies":                         []string{"aep-secret-reader"},
		"ttl":                              "1h",
	}); err != nil {
		return fatal(fmt.Sprintf("create ESO auth role: %v", err))
	}

	if err := progress("OpenBao provisioned successfully."); err != nil {
		return err
	}

	// Revoke the root token — all provisioning is done and it is no longer needed.
	// Recovery, if ever required, uses the unseal keys via OpenBao's generate-root process.
	if err := progress("Revoking root token..."); err != nil {
		return err
	}
	if _, err := openbao.Must(ctx, "POST", s.openbaoAddr, rootToken, "/v1/auth/token/revoke-self", nil); err != nil {
		return fatal(fmt.Sprintf("revoke root token: %v", err))
	}
	// rootToken was revoked above and is intentionally not referenced past this point.

	// Send credentials — root token is intentionally omitted; it has been revoked.
	return stream.Send(&adminpb.InitEvent{
		Payload: &adminpb.InitEvent_Complete{
			Complete: &adminpb.InitComplete{
				UnsealKeys: unsealKeys,
			},
		},
	})
}
