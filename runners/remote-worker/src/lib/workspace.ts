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

// Per-task workspace provisioning.
//
// On dispatch the BFF creates a WorkflowRun of `aep-coding-agent`
// and Argo schedules an ephemeral pod whose entrypoint (src/oneshot.ts)
// calls this function. We clone the project's repo on its **default
// branch** into $WORKSPACE_BASE_PATH/<orgId>/<projectId>/<taskId>/ and
// configure `.git/config` + `gh` so the agent can git/gh against GitHub
// without ever seeing a token in environment variables. The agent itself
// creates the feature branch and opens the PR with `Closes #<issueNumber>`
// — see remote-worker/plugin/skills/aep/SKILL.md.
//
// Layout inside the workspace:
//
//   <workspace>/
//     .git/                     ← cloned repo, default branch checked out
//     .gh-config/hosts.yml      ← gh's auth config (rewritten by ghWrapper)
//     .aep/
//       bearer                  ← chmod 600 — per-task JWT
//       credhelper.sh           ← chmod 700 — git credential helper
//       gh                      ← chmod 755 — gh wrapper, on PATH
//
// The agent runs with cwd=<workspace> and PATH prefixed with <workspace>/.aep
// so `gh ...` resolves to the wrapper. No tokens cross via process env.

import fs from "node:fs";
import { exec } from "node:child_process";
import path from "node:path";
import { promisify } from "node:util";
import http from "node:http";
import https from "node:https";
import { config } from "../config.js";
import { credHelperScript, ghWrapperScript } from "./credhelper.js";
import { cloneWithToken } from "./git_clone.js";
import { primeScrubber } from "./progress/emitter.js";
import { shellQuote } from "./shell.js";

const execAsync = promisify(exec);

export interface WorkspaceLayout {
  workspace: string;
  ghConfigDir: string;
  bearerFile: string;
  aepDir: string;
  helperBin: string;
  ghWrapper: string;
}

export interface ProvisionRequest {
  orgId: string;
  projectId: string;
  taskId: string;
  repoUrl: string;
  bearer: string;
  identity: { name: string; email: string; login?: string };
  gitServiceUrl: string;
  correlationId?: string;
  // WS2.6 — full refresh URL, set by oneshot.ts to the path-scoped
  // `${platformUrl}/internal/v1/executions/{executionId}/credentials/refresh`
  // (accepts both publisher-cc and legacy Task-JWT; taskId carries the
  // execution id, §9.2). Falls back to a path-scoped URL built from
  // gitServiceUrl below when unset.
  refreshUrl?: string;
}

// computeLayout names every path the dispatch flow touches. Pure function
// so tests can verify the path layout without filesystem effects.
export function computeLayout(orgId: string, projectId: string, taskId: string): WorkspaceLayout {
  const workspace = path.join(config.workspaceBasePath, orgId, projectId, taskId);
  const aepDir = path.join(workspace, ".aep");
  return {
    workspace,
    ghConfigDir: path.join(workspace, ".gh-config"),
    bearerFile: path.join(aepDir, "bearer"),
    aepDir,
    helperBin: path.join(aepDir, "credhelper.sh"),
    ghWrapper: path.join(aepDir, "gh"),
  };
}

// GitTokenRequest is the minimal shape refreshGitToken needs — satisfied by
// both ProvisionRequest and the oneshot DispatchRequest.
export interface GitTokenRequest {
  taskId: string;
  gitServiceUrl: string;
  refreshUrl?: string;
  correlationId?: string;
}

// refreshGitToken POSTs to the path-scoped credentials/refresh endpoint with
// the given runner bearer and returns the GitHub PAT. The endpoint returns an
// org/installation-wide token, so the SAME PAT clones both the project repo
// (workspace provisioning) and the org-skills repo (per-task skills clone).
//
// This is the one place a git token enters the runner process, so it is also
// where the token is enrolled as a scrubber literal — every later emission,
// log line or error message is redacted by shape-independent exact match, not
// by hoping the credential looks like a `github_pat_…`.
export async function refreshGitToken(req: GitTokenRequest, bearer: string): Promise<string> {
  if (!bearer.trim()) {
    throw new Error("bearer is empty");
  }

  // WS2.6 — refreshUrl (set by oneshot.ts) is the path-scoped endpoint. The
  // fallback builds the same path-scoped URL from gitServiceUrl for the rare
  // case oneshot didn't set it (AEP_PLATFORM_URL unset).
  let url: URL;
  if (req.refreshUrl && req.refreshUrl !== "") {
    url = new URL(req.refreshUrl);
  } else {
    url = new URL(req.gitServiceUrl);
    if (!url.pathname.endsWith("/")) url.pathname += "/";
    url.pathname += `internal/v1/executions/${encodeURIComponent(req.taskId)}/credentials/refresh`;
  }

  const headers: Record<string, string> = {
    "Authorization": `Bearer ${bearer.trim()}`,
    "Content-Type": "application/json",
  };
  if (req.correlationId) {
    headers["X-Correlation-ID"] = req.correlationId;
  }

  // Pick the transport by URL scheme: in cloud the BFF/git endpoint is https
  // (gateway), locally it's http. Node's http.request rejects https URLs with
  // "Protocol \"https:\" not supported", so this must branch on the scheme.
  const lib = url.protocol === "https:" ? https : http;

  return new Promise((resolve, reject) => {
    const hReq = lib.request(
      url,
      { method: "POST", headers, timeout: 10000 },
      (res) => {
        let body = "";
        res.on("data", (chunk: Buffer) => { body += chunk.toString(); });
        res.on("end", () => {
          if (res.statusCode !== 200) {
            return reject(new Error(`git-service returned ${res.statusCode}: ${body.slice(0, 200)}`));
          }
          try {
            const data = JSON.parse(body);
            if (!data.token) {
              return reject(new Error("git-service response missing token"));
            }
            const token = data.token as string;
            primeScrubber([token]);
            resolve(token);
          } catch {
            // Never quote the body: this is the credential-bearing payload,
            // and an unparseable one is exactly the case where a token could
            // ride out inside a fragment that matches no known shape.
            reject(new Error(`invalid git-service response (${body.length} bytes, not JSON)`));
          }
        });
      },
    );
    hReq.on("error", reject);
    hReq.on("timeout", () => { hReq.destroy(); reject(new Error("git-service request timed out")); });
    hReq.write("{}");
    hReq.end();
  });
}

// resolvePATForClone reads the staged bearer file and refreshes the GitHub PAT
// the workspace clone authenticates with.
async function resolvePATForClone(bearerFile: string, req: ProvisionRequest): Promise<string> {
  const bearer = await fs.promises.readFile(bearerFile, "utf-8");
  if (!bearer.trim()) {
    throw new Error("bearer file is empty");
  }
  return refreshGitToken(req, bearer);
}
// provisionWorkspace clones the feature branch and writes credentials.
// Idempotent: it removes any existing workspace first (§12.1 step 5
// resume-safety: a crash mid-clone leaves DispatchedAt=null, the resume
// sweep re-enters this step, which begins with rm -rf).
//
// Order matters: `git clone <url> <dir>` refuses to write into an existing
// non-empty directory. So we stage the clone's credentials in a sibling tmp
// dir, clone into the workspace path (which must not exist yet), and only
// then drop the .aep/ and .gh-config/ directories inside the cloned tree.
export async function provisionWorkspace(req: ProvisionRequest): Promise<WorkspaceLayout> {
  const layout = computeLayout(req.orgId, req.projectId, req.taskId);
  const stageDir = layout.workspace + ".stage";

  // Wipe both the target and any prior stage. Don't pre-create the workspace
  // dir — git clone will materialise it.
  await fs.promises.rm(layout.workspace, { recursive: true, force: true });
  await fs.promises.rm(stageDir, { recursive: true, force: true });
  await fs.promises.mkdir(path.dirname(layout.workspace), { recursive: true, mode: 0o755 });
  await fs.promises.mkdir(stageDir, { recursive: true, mode: 0o700 });

  // Stage the bearer in the sibling dir: refreshGitToken reads it to mint the
  // clone token, and it has to live somewhere outside the not-yet-existing
  // workspace. The askpass shim lands here too (see cloneWithToken below).
  // The runtime credhelper is written into the cloned tree afterwards.
  const stageBearer = path.join(stageDir, "bearer");
  await fs.promises.writeFile(stageBearer, req.bearer, { mode: 0o600 });

  try {
    // Resolve the PAT from git-service, then clone the PLAIN URL with the
    // token handed to git via the askpass shim (see git_clone.ts). The token
    // stays in the clone child's environment: not in argv, not in the error
    // message on failure, and not left behind in .git/config. `.git/config`
    // gets the long-lived credential.https://github.com.helper below, which is
    // what authenticates the agent's own later fetch/push.
    //
    // No --branch: clone the remote's default branch (HEAD). The agent
    // creates its own feature branch via `git checkout -b ...` once it
    // starts working, per the aep skill workflow.
    const patResp = await resolvePATForClone(stageBearer, req);
    await cloneWithToken({
      repoUrl: req.repoUrl,
      destDir: layout.workspace,
      token: patResp,
      shimDir: stageDir,
    });

    // Materialise the runtime layout inside the cloned tree.
    await fs.promises.mkdir(layout.aepDir, { recursive: true, mode: 0o755 });
    await fs.promises.mkdir(layout.ghConfigDir, { recursive: true, mode: 0o755 });
    await fs.promises.writeFile(layout.bearerFile, req.bearer, { mode: 0o600 });
    await fs.promises.writeFile(
      layout.helperBin,
      credHelperScript({ taskId: req.taskId, workspaceDir: layout.workspace }),
      { mode: 0o700 },
    );

    // gh wrapper (chmod 755). Resolve the real gh binary path eagerly so the
    // wrapper doesn't have to. If gh is not on PATH we fall back to
    // /usr/bin/env gh and let the wrapper fail at run time with a useful error.
    let realGhPath = "gh";
    try {
      const which = await execAsync("which gh");
      realGhPath = which.stdout.trim() || "gh";
    } catch {
      realGhPath = "/usr/bin/env gh";
    }
    await fs.promises.writeFile(
      layout.ghWrapper,
      ghWrapperScript(realGhPath, { taskId: req.taskId, workspaceDir: layout.workspace }),
      { mode: 0o755 },
    );

    // .git/config: identity + credential helper so subsequent ops don't need
    // GIT_ASKPASS env.
    await execAsync(
      `git -C ${shellQuote(layout.workspace)} config user.name ${shellQuote(req.identity.name)}`,
    );
    await execAsync(
      `git -C ${shellQuote(layout.workspace)} config user.email ${shellQuote(req.identity.email)}`,
    );
    await execAsync(
      `git -C ${shellQuote(layout.workspace)} config credential.https://github.com.helper ${shellQuote(layout.helperBin)}`,
    );
  } finally {
    await fs.promises.rm(stageDir, { recursive: true, force: true });
  }

  return layout;
}
