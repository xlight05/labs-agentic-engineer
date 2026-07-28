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

// Authenticated `git clone` — the single owner of "clone a platform repo with
// an org token". Both clone sites in the runner go through it: the project
// work tree (workspace.ts) and the org-skills repo (skills_resolver.ts).
//
// The token is handed to git through a static GIT_ASKPASS shim rather than
// embedded in the clone URL. Embedding it puts the credential in four places
// the runner cannot control:
//
//   - argv, visible in a `ps` listing for the clone's duration;
//   - the `Command failed: git clone '<url>' …` message that child_process
//     puts on every non-zero exit — which the runner logs and the BFF
//     forwards verbatim into the console build log;
//   - the cloned repo's `.git/config`, because git preserves URL userinfo, so
//     the credential would sit at rest in the work tree for the whole run;
//   - any later `git remote -v` the agent happens to run.
//
// Same shape as the Go side's platform/gitfs/askpass.go. With the token in the
// child environment only, a clone failure can print the whole command safely.

import fs from "node:fs";
import path from "node:path";
import { exec } from "node:child_process";
import { promisify } from "node:util";
import { ASKPASS_FILE, CLONE_TOKEN_ENV, askpassScript } from "./credhelper.js";
import { shellQuote } from "./shell.js";

const execAsync = promisify(exec);

// Clone output can be large on a big repo; keep the generous buffer the two
// call sites used before they shared this module.
const CLONE_MAX_BUFFER = 16 * 1024 * 1024;

export interface CloneOptions {
  /** Plain https clone URL — never carries credentials. */
  repoUrl: string;
  /** Destination directory; must not exist (git refuses a non-empty dir). */
  destDir: string;
  /** Org token. Empty means an unauthenticated origin (file:// in tests). */
  token: string;
  /** Scratch dir the askpass shim is written into — never the work tree. */
  shimDir: string;
  /** `--depth 1` for the skills repo, where history is not needed. */
  depth1?: boolean;
}

export interface CloneInvocation {
  cmd: string;
  env: NodeJS.ProcessEnv;
}

// buildCloneInvocation is the pure seam: it returns the exact command string
// and environment a clone runs with, so a test can assert that the token is
// absent from the command and present only in the environment.
export function buildCloneInvocation(
  opts: Omit<CloneOptions, "shimDir"> & { askpassPath: string; baseEnv?: NodeJS.ProcessEnv },
): CloneInvocation {
  const depth = opts.depth1 ? " --depth 1" : "";
  const cmd = `git clone${depth} ${shellQuote(opts.repoUrl)} ${shellQuote(opts.destDir)}`;
  const env: NodeJS.ProcessEnv = {
    ...(opts.baseEnv ?? process.env),
    GIT_TERMINAL_PROMPT: "0",
  };
  // An unauthenticated origin gets no shim at all, so a genuinely missing
  // credential surfaces as git's own error rather than an empty password.
  if (opts.token !== "") {
    env.GIT_ASKPASS = opts.askpassPath;
    env[CLONE_TOKEN_ENV] = opts.token;
  }
  return { cmd, env };
}

// writeAskpassShim writes the static shim (0700) into dir and returns its path.
// The shim holds no secret, so re-writing it is idempotent.
export async function writeAskpassShim(dir: string): Promise<string> {
  await fs.promises.mkdir(dir, { recursive: true, mode: 0o700 });
  const shimPath = path.join(dir, ASKPASS_FILE);
  await fs.promises.writeFile(shimPath, askpassScript(), { mode: 0o700 });
  return shimPath;
}

// cloneWithToken stages the shim and runs the clone. Rejects with git's own
// error on failure — safe to log, since neither the command nor the message
// can contain the token.
export async function cloneWithToken(opts: CloneOptions): Promise<void> {
  const askpassPath = await writeAskpassShim(opts.shimDir);
  const { cmd, env } = buildCloneInvocation({ ...opts, askpassPath });
  await execAsync(cmd, { env, maxBuffer: CLONE_MAX_BUFFER });
}
