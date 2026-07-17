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

package thunder

import "fmt"

// AuthenticatedScript returns a POSIX sh script (alpine-compatible) that
// registers all AEP OAuth clients in a running Thunder instance.
//
// It authenticates using client_credentials + scope=system (the OC system app),
// then calls Thunder's admin API with the resulting Bearer token.
// All client secrets are injected as environment variables — nothing is
// embedded in the script itself.
func AuthenticatedScript(thunderURL, consoleURL string) string {
	return fmt.Sprintf(`#!/bin/sh
set -e

THUNDER_URL="%s"
CONSOLE_URL="%s"

log()    { echo "[INFO]    $*"; }
log_ok() { echo "[SUCCESS] $*"; }
log_err(){ echo "[ERROR]   $*" >&2; }

# ── Wait for Thunder and obtain admin Bearer token ───────────────────────────
log "Waiting for Thunder at ${THUNDER_URL}..."
TOKEN=""
i=0
while [ "$i" -lt 60 ]; do
  TOKEN=$(curl -sf --noproxy "*" --max-time 10 \
    -X POST "${THUNDER_URL}/oauth2/token" \
    -d "grant_type=client_credentials&client_id=${THUNDER_ADMIN_CLIENT_ID}&client_secret=${THUNDER_ADMIN_CLIENT_SECRET}&scope=system" \
    2>/dev/null | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4 || true)
  if [ -n "$TOKEN" ]; then
    log "Thunder ready — admin token obtained"
    break
  fi
  i=$((i+1))
  [ "$i" -eq 60 ] && { log_err "Thunder not reachable after 300s"; exit 1; }
  log "  not ready yet ($i/60), retrying in 5s..."
  sleep 5
done

# ── Fetch default OU ID ──────────────────────────────────────────────────────
log "Fetching default organisation unit..."
OU_RESP=$(curl -sf --noproxy "*" -H "Authorization: Bearer $TOKEN" \
  "${THUNDER_URL}/organization-units/tree/default")
OU_ID=$(echo "$OU_RESP" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
[ -z "$OU_ID" ] && { log_err "Could not fetch OU ID. Response: $OU_RESP"; exit 1; }
log "OU ID: $OU_ID"

# ── Fetch auth flow ID ───────────────────────────────────────────────────────
log "Fetching default-basic-flow ID..."
FLOWS_RESP=$(curl -sf --noproxy "*" -H "Authorization: Bearer $TOKEN" \
  "${THUNDER_URL}/flows?flowType=AUTHENTICATION&limit=200")
AUTH_FLOW_ID=$(echo "$FLOWS_RESP" | tr '\n' ' ' \
  | grep -o '"id":"[^"]*"[^}]*"handle":"default-basic-flow"' \
  | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
[ -z "$AUTH_FLOW_ID" ] && { log_err "Could not find default-basic-flow"; exit 1; }
log "Auth flow ID: $AUTH_FLOW_ID"

# ── Load existing apps once ──────────────────────────────────────────────────
APPS=$(curl -sf --noproxy "*" -H "Authorization: Bearer $TOKEN" \
  "${THUNDER_URL}/applications?limit=200")

# ── Upsert helper ────────────────────────────────────────────────────────────
upsert_app() {
  local client_id="$1" payload="$2"
  local app_id
  app_id=$(echo "$APPS" | tr '\n' ' ' | sed 's/" *: *"/":"/g' \
    | grep -o "\"client_id\":\"${client_id}\"[^}]*\"id\":\"[^\"]*\"\|\"id\":\"[^\"]*\"[^}]*\"client_id\":\"${client_id}\"" \
    | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
  if [ -n "$app_id" ]; then
    log "  updating ${client_id} (${app_id})..."
    curl -sf --noproxy "*" -X PUT \
      -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
      -d "$payload" "${THUNDER_URL}/applications/${app_id}" -o /dev/null \
      || { log_err "Update failed for ${client_id}"; exit 1; }
  else
    log "  creating ${client_id}..."
    curl -sf --noproxy "*" -X POST \
      -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
      -d "$payload" "${THUNDER_URL}/applications" -o /dev/null \
      || { log_err "Create failed for ${client_id}"; exit 1; }
  fi
  log_ok "${client_id}"
}

confidential() {
  local name="$1" desc="$2" cid="$3" secret="$4"
  upsert_app "$cid" "{
    \"name\":\"$name\",\"description\":\"$desc\",\"ou_id\":\"$OU_ID\",
    \"inbound_auth_config\":[{\"type\":\"oauth2\",\"config\":{
      \"client_id\":\"$cid\",\"client_secret\":\"$secret\",
      \"grant_types\":[\"client_credentials\"],
      \"token_endpoint_auth_method\":\"client_secret_post\",
      \"pkce_required\":false,\"public_client\":false,
      \"token\":{\"access_token\":{\"validity_period\":3600}}
    }}]}"
}

# ── Register confidential clients ─────────────────────────────────────────────
log "Registering confidential clients..."
confidential "Workload Publisher"                    "OC Workload Publisher Client"              "openchoreo-workload-publisher-client" "$OC_WORKLOAD_PUBLISHER_SECRET"
confidential "OpenChoreo Observer Resource Reader"   "BFF token for OC Observer service"         "openchoreo-observer-resource-reader-client" "$OC_OBSERVER_READER_SECRET"
confidential "AEP API Service"                       "AEP API service-to-service client"         "aep-api-client"             "$AEP_API_CLIENT_SECRET"
confidential "AEP BFF to git-service"                "BFF outbound JWT, audience: git-service"   "aep-bff-to-git-service"     "$BFF_TO_GIT_SERVICE_SECRET"
confidential "AEP BFF to remote-worker"              "BFF outbound JWT, audience: remote-worker" "aep-bff-to-remote-worker"   "$BFF_TO_REMOTE_WORKER_SECRET"
confidential "AEP Local Dev Seeder"                  "Local-dev convenience client"              "aep-local-dev-seeder"       "$LOCAL_DEV_SEEDER_SECRET"
confidential "AEP System Client"                     "System-level Thunder admin client"         "aep-system-client"          "$AEP_SYSTEM_CLIENT_SECRET"
confidential "OpenChoreo RCA Agent"                  "SRE/RCA agent service-account identity"    "openchoreo-rca-agent"       "$OC_RCA_AGENT_SECRET"

# ── Register console PKCE client ─────────────────────────────────────────────
log "Registering console PKCE client..."
USER_ATTRS='["given_name","family_name","username","groups","ouId","ouName","ouHandle"]'
upsert_app "aep-console-client" "{
  \"name\":\"AEP Console\",\"description\":\"AEP Platform Console\",
  \"ou_id\":\"$OU_ID\",\"auth_flow_id\":\"$AUTH_FLOW_ID\",
  \"inbound_auth_config\":[{\"type\":\"oauth2\",\"config\":{
    \"client_id\":\"aep-console-client\",
    \"redirect_uris\":[\"${CONSOLE_URL}\",\"${CONSOLE_URL}/\",\"${CONSOLE_URL}/callback\"],
    \"grant_types\":[\"authorization_code\",\"refresh_token\"],
    \"response_types\":[\"code\"],
    \"token_endpoint_auth_method\":\"none\",
    \"pkce_required\":true,\"public_client\":true,
    \"token\":{
      \"access_token\":{\"validity_period\":86400,\"user_attributes\":$USER_ATTRS},
      \"id_token\":{\"validity_period\":86400,\"user_attributes\":$USER_ATTRS}
    }
  }}]}"

# ── Register CLI PKCE client ──────────────────────────────────────────────────
log "Registering CLI PKCE client..."
upsert_app "aep-cli-client" "{
  \"name\":\"AEP CLI\",\"description\":\"AEP CLI tool — PKCE login\",
  \"ou_id\":\"$OU_ID\",\"auth_flow_id\":\"$AUTH_FLOW_ID\",
  \"inbound_auth_config\":[{\"type\":\"oauth2\",\"config\":{
    \"client_id\":\"aep-cli-client\",
    \"redirect_uris\":[\"http://localhost\",\"http://127.0.0.1\"],
    \"grant_types\":[\"authorization_code\",\"refresh_token\"],
    \"response_types\":[\"code\"],
    \"token_endpoint_auth_method\":\"none\",
    \"pkce_required\":true,\"public_client\":true,
    \"token\":{\"access_token\":{\"validity_period\":86400}}
  }}]}"

# ── Assign aep-system-client to Administrator role ────────────────────────────
log "Assigning aep-system-client to Administrator role..."
APPS2=$(curl -sf --noproxy "*" -H "Authorization: Bearer $TOKEN" "${THUNDER_URL}/applications?limit=200")
SYS_APP_ID=$(echo "$APPS2" | tr '\n' ' ' | sed 's/" *: *"/":"/g' \
  | grep -o '"client_id":"aep-system-client"[^}]*"id":"[^"]*"\|"id":"[^"]*"[^}]*"client_id":"aep-system-client"' \
  | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
ROLES=$(curl -sf --noproxy "*" -H "Authorization: Bearer $TOKEN" "${THUNDER_URL}/roles?limit=200" || true)
ADMIN_ROLE_ID=$(echo "$ROLES" | tr '\n' ' ' \
  | grep -o '"name":"Administrator"[^}]*"id":"[^"]*"\|"id":"[^"]*"[^}]*"name":"Administrator"' \
  | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
if [ -n "$SYS_APP_ID" ] && [ -n "$ADMIN_ROLE_ID" ]; then
  curl -sf --noproxy "*" -X POST \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d "{\"role_id\":\"$ADMIN_ROLE_ID\",\"application_id\":\"$SYS_APP_ID\"}" \
    "${THUNDER_URL}/role-assignments" -o /dev/null 2>/dev/null || true
  log_ok "aep-system-client -> Administrator"
else
  log "Administrator role not found — skipping role assignment"
fi

log_ok "AEP Thunder OAuth bootstrap complete"
`, thunderURL, consoleURL)
}
