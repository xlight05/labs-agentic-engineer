/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import { shellQuote } from "./shell.js";

// Templates for the workspace credential helpers.
//
// Two scripts live inside each task's `.aep/` directory:
//
//   - `credhelper.sh`: a git credential helper that, on every git op,
//     POSTs to /credentials/refresh on git-service to get a fresh token.
//     Phase 0's long-lived PAT makes this trivial; Phase 2's short-lived
//     App tokens use the same code path. The script also enforces the
//     PR D §6.6 anti-misroute tripwire (response.taskId must match the
//     workspace's task) and rewrites .git/config user fields when the
//     credential's identity drifted on the server side mid-task.
//
//   - `gh` (a wrapper): pre-flights every `gh` invocation by refreshing
//     the token and rewriting `$GH_CONFIG_DIR/hosts.yml` before exec'ing
//     the real binary. Same anti-misroute tripwire as credhelper.sh.
//
// Both scripts read the per-task bearer from a chmod-600 file rather than
// from env, so transcripts and process listings can't leak it. taskId
// and workspace path are baked into the script at provisioning time so
// the agent process doesn't need to thread them through env.

export interface CredHelperParams {
  // Per-task identifier baked into the script for the anti-misroute
  // check. The credhelper refuses if response.taskId doesn't match.
  taskId: string;
  // Absolute workspace path for `.git/config` rewrites on identity drift.
  workspaceDir: string;
}

// Env var carrying a clone token into the git child process, where only the
// askpass shim below reads it. It is set on a per-child env object and NEVER
// on `process.env`: runner.ts spreads `process.env` into the agent's child
// env, so a token placed there would reach the agent and everything it spawns.
export const CLONE_TOKEN_ENV = "AEP_CLONE_TOKEN";

// The shim's filename on disk. Written into a scratch dir, never the work tree.
export const ASKPASS_FILE = "askpass.sh";

// askpassScript answers git's credential prompts from the child environment.
// Mirrors services/aep-api/internal/platform/gitfs/askpass.go: because the
// token travels in the environment rather than in the clone URL, it cannot
// appear in argv (a `ps` listing), in a `Command failed: git clone '<url>'`
// error message, or in the cloned repo's `.git/config` — git preserves URL
// userinfo verbatim, so an authenticated clone URL leaves the credential at
// rest in the work tree for the whole run.
//
// Unlike credhelper.sh this shim performs no refresh round-trip: the caller
// has already minted the token. It is static, so writing it is idempotent.
export function askpassScript(): string {
  return `#!/bin/sh
# AEP clone credential shim — answers git's GIT_ASKPASS prompts from the
# child environment so the token never reaches argv, an error message, or
# .git/config.
case "$1" in
*sername*) echo x-access-token ;;
*) printf %s "$${CLONE_TOKEN_ENV}" ;;
esac
`;
}

export function credHelperScript(params: CredHelperParams): string {
  const { taskId, workspaceDir } = params;
  return `#!/usr/bin/env bash
# Git credential helper for AEP platform-managed repos.
# Two auth modes (WS2.6 — both call the path-scoped endpoint
# POST /internal/v1/executions/{executionId}/credentials/refresh, which accepts
# either token; the bound id below is the execution id, §9.2):
#   (a) publisher cc — when PUBLISHER_CLIENT_ID/SECRET/TOKEN_URL are set,
#       mint a Thunder access token via client_credentials.
#   (b) legacy TaskJWT — read the per-task bearer from \$AEP_BEARER_FILE.
# Stays silent on errors so git's own error message reaches the user.
#
# Phase 2 PR D §6.6 anti-misroute: refuses if the refresh response's
# taskId doesn't match this script's bound task — defends against a
# bearer mistakenly mounted in the wrong workspace from rewriting this
# task's identity or borrowing this task's credential.
#
# Phase 2 PR D §6.6 identity drift: when the server-side credential's
# identity changed mid-task (PAT replaced with a different-user PAT,
# or App account renamed), rewrite .git/config user.name / user.email
# so subsequent commits attribute correctly. The first in-flight commit
# may still carry the old identity (best-effort, not transactional).
set -e
expected_task_id=${shellQuote(taskId)}
workspace_dir=${shellQuote(workspaceDir)}

corr_header=()
if [ -n "$AEP_CORRELATION_ID" ]; then
  corr_header=(-H "X-Correlation-ID: $AEP_CORRELATION_ID")
fi

bearer=""
refresh_url=""
if [ -n "$PUBLISHER_CLIENT_ID" ] && [ -n "$PUBLISHER_CLIENT_SECRET" ] && [ -n "$PUBLISHER_TOKEN_URL" ]; then
  # WS2.4 — mint cc token via Basic auth, call path-scoped endpoint.
  cc_resp="$(curl -fsS -X POST \\
    -u "$PUBLISHER_CLIENT_ID:$PUBLISHER_CLIENT_SECRET" \\
    -H "Content-Type: application/x-www-form-urlencoded" \\
    -d 'grant_type=client_credentials' \\
    "$PUBLISHER_TOKEN_URL" 2>/dev/null || true)"
  if [ -n "$cc_resp" ] && command -v python3 >/dev/null 2>&1; then
    bearer="$(printf '%s' "$cc_resp" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("access_token",""))' 2>/dev/null || true)"
  elif [ -n "$cc_resp" ]; then
    bearer="$(echo "$cc_resp" | sed -n 's/.*"access_token":"\\([^"]*\\)".*/\\1/p')"
  fi
  if [ -n "$bearer" ]; then
    refresh_url="\${AEP_PLATFORM_URL:-$AEP_GIT_SERVICE_URL}/internal/v1/executions/$expected_task_id/credentials/refresh"
  fi
fi
if [ -z "$bearer" ]; then
  bearer="$(cat "$AEP_BEARER_FILE" 2>/dev/null || true)"
  refresh_url="\${AEP_PLATFORM_URL:-$AEP_GIT_SERVICE_URL}/internal/v1/executions/$expected_task_id/credentials/refresh"
fi
if [ -z "$bearer" ]; then
  exit 1
fi

resp="$(curl -fsS -X POST \\
  -H "Authorization: Bearer $bearer" \\
  -H "Content-Type: application/json" \\
  "\${corr_header[@]}" \\
  -d '{}' \\
  "$refresh_url" 2>/dev/null || true)"
if [ -z "$resp" ]; then
  exit 1
fi

# Parse the JSON response in a single python3 process — five fields,
# one line each, read in order. Falls back to sed if python3 is missing.
resp_token=""
resp_task_id=""
resp_login=""
resp_name=""
resp_email=""
if command -v python3 >/dev/null 2>&1; then
  {
    read -r resp_token || true
    read -r resp_task_id || true
    read -r resp_login || true
    read -r resp_name || true
    read -r resp_email || true
  } < <(printf '%s' "$resp" | python3 -c '
import json, sys
d = json.load(sys.stdin)
i = d.get("identity") or {}
print(d.get("token", ""))
print(d.get("taskId", ""))
print(i.get("login", ""))
print(i.get("name", ""))
print(i.get("email", ""))
' 2>/dev/null)
else
  resp_token="$(echo "$resp" | sed -n 's/.*"token":"\\([^"]*\\)".*/\\1/p')"
  resp_task_id="$(echo "$resp" | sed -n 's/.*"taskId":"\\([^"]*\\)".*/\\1/p')"
fi

if [ -z "$resp_token" ]; then
  exit 1
fi

# Anti-misroute tripwire (PR D §6.6). Empty taskId in the response is
# tolerated — older git-service versions may not echo it; in that mode
# the workspace bearer's signature is the only credential check. Once
# git-service ships the echo (this PR), every refresh carries it and
# mismatch surfaces here.
if [ -n "$resp_task_id" ] && [ "$resp_task_id" != "$expected_task_id" ]; then
  echo "credhelper: refusing — response.taskId ($resp_task_id) != bound task ($expected_task_id)" >&2
  exit 1
fi

# Identity drift rewrite (PR D §6.6). Only applies when the response
# actually carries identity fields and they differ from what's in
# .git/config. Soft-fails — git op continues with the credential even
# if .git/config rewrite fails (subsequent commits would still attribute
# under the old identity in that case, surfaced in audit later).
if [ -n "$resp_login" ] && [ -d "$workspace_dir/.git" ]; then
  current_name="$(git -C "$workspace_dir" config user.name 2>/dev/null || true)"
  current_email="$(git -C "$workspace_dir" config user.email 2>/dev/null || true)"
  new_name="\${resp_name:-$resp_login}"
  new_email="\${resp_email:-$resp_login@users.noreply.github.com}"
  if [ "$current_name" != "$new_name" ] || [ "$current_email" != "$new_email" ]; then
    echo "credhelper: identity drift detected ($current_name → $new_name); rewriting .git/config" >&2
    git -C "$workspace_dir" config user.name "$new_name" >/dev/null 2>&1 || true
    git -C "$workspace_dir" config user.email "$new_email" >/dev/null 2>&1 || true
  fi
fi

# Dual-protocol output:
#   - GIT_ASKPASS: invoked once per prompt, prompt text in \$1, expects ONE line.
#   - credential.helper get: invoked with action on stdin (get/store/erase),
#     expects 'username=...\\npassword=...' on stdout.
# A non-empty \$1 means git is using us as GIT_ASKPASS (workspace clone path).
if [ -n "\$1" ]; then
  case "\$1" in
    *[Uu]sername*) echo "x-access-token" ;;
    *[Pp]assword*) echo "$resp_token" ;;
    *)             echo "$resp_token" ;;
  esac
else
  echo "username=x-access-token"
  echo "password=$resp_token"
fi
`;
}

export function ghWrapperScript(realGhPath: string, params: CredHelperParams): string {
  const { taskId } = params;
  return `#!/usr/bin/env bash
# gh CLI wrapper. Refreshes the GitHub token and rewrites
# $GH_CONFIG_DIR/hosts.yml on every invocation, then execs the real
# binary. Phase 0's long-lived PAT makes the refresh redundant; Phase 2's
# 1h App tokens require it for any task that runs > 1h between gh calls.
#
# Phase 2 PR D §6.6 anti-misroute: refuses if the refresh response's
# taskId doesn't match this script's bound task. Same shape as the
# credhelper.sh tripwire.
set -e
expected_task_id=${shellQuote(taskId)}

bearer="$(cat "$AEP_BEARER_FILE" 2>/dev/null || true)"
if [ -n "$bearer" ]; then
  corr_header=()
  if [ -n "$AEP_CORRELATION_ID" ]; then
    corr_header=(-H "X-Correlation-ID: $AEP_CORRELATION_ID")
  fi
  resp="$(curl -fsS -X POST \\
    -H "Authorization: Bearer $bearer" \\
    -H "Content-Type: application/json" \\
    "\${corr_header[@]}" \\
    -d '{}' \\
    "\${AEP_PLATFORM_URL:-$AEP_GIT_SERVICE_URL}/internal/v1/executions/$expected_task_id/credentials/refresh" 2>/dev/null || true)"
  if [ -n "$resp" ]; then
    token=""
    login=""
    resp_task_id=""
    if command -v python3 >/dev/null 2>&1; then
      {
        read -r token || true
        read -r resp_task_id || true
        read -r login || true
      } < <(printf '%s' "$resp" | python3 -c '
import json, sys
d = json.load(sys.stdin)
i = d.get("identity") or {}
print(d.get("token", ""))
print(d.get("taskId", ""))
print(i.get("login", ""))
' 2>/dev/null)
    else
      token="$(echo "$resp" | sed -n 's/.*"token":"\\([^"]*\\)".*/\\1/p')"
      login="$(echo "$resp" | sed -n 's/.*"login":"\\([^"]*\\)".*/\\1/p')"
      resp_task_id="$(echo "$resp" | sed -n 's/.*"taskId":"\\([^"]*\\)".*/\\1/p')"
    fi
    if [ -n "$resp_task_id" ] && [ "$resp_task_id" != "$expected_task_id" ]; then
      echo "gh wrapper: refusing — response.taskId ($resp_task_id) != bound task ($expected_task_id)" >&2
      exit 1
    fi
    if [ -n "$token" ]; then
      mkdir -p "$GH_CONFIG_DIR"
      cat > "$GH_CONFIG_DIR/hosts.yml" <<EOF
github.com:
    oauth_token: $token
    user: \${login:-x-access-token}
    git_protocol: https
EOF
    fi
  fi
fi
exec ${JSON.stringify(realGhPath)} "$@"
`;
}

