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

import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { exec } from "node:child_process";
import { promisify } from "node:util";
import { CLONE_TOKEN_ENV } from "./credhelper.js";
import { buildCloneInvocation, cloneWithToken, writeAskpassShim } from "./git_clone.js";
import { shellQuote } from "./shell.js";

const execAsync = promisify(exec);

// A realistically-shaped token. Shape matters for one test only (the scrubber
// covers shapes independently); every other assertion here must hold for a
// credential of ANY shape, which is the point of keeping it out of argv.
const TOKEN = "github_pat_11TESTONLY_abcdefghijklmnopqrstuvwxyz0123456789";

async function tmpDir(): Promise<string> {
  return fs.promises.mkdtemp(path.join(os.tmpdir(), "aep-clone-test-"));
}

// ---- buildCloneInvocation (pure seam) ---------------------------------------

test("buildCloneInvocation: token is in the env, never in the command", () => {
  const { cmd, env } = buildCloneInvocation({
    repoUrl: "https://github.com/asdlc-repos/store-handmade-ceramics.git",
    destDir: "/home/aep/aep-workspace/default/store/abc",
    token: TOKEN,
    askpassPath: "/stage/askpass.sh",
    baseEnv: {},
  });

  assert.ok(!cmd.includes(TOKEN), "command must not contain the token");
  assert.ok(!cmd.includes("x-access-token"), "command must not carry URL userinfo");
  assert.equal(
    cmd,
    "git clone 'https://github.com/asdlc-repos/store-handmade-ceramics.git' " +
      "'/home/aep/aep-workspace/default/store/abc'",
  );
  assert.equal(env[CLONE_TOKEN_ENV], TOKEN);
  assert.equal(env.GIT_ASKPASS, "/stage/askpass.sh");
  assert.equal(env.GIT_TERMINAL_PROMPT, "0");
});

test("buildCloneInvocation: --depth 1 when requested", () => {
  const { cmd } = buildCloneInvocation({
    repoUrl: "https://github.com/acme/org-skills.git",
    destDir: "/tmp/skills",
    token: TOKEN,
    askpassPath: "/tmp/askpass.sh",
    depth1: true,
    baseEnv: {},
  });
  assert.equal(cmd, "git clone --depth 1 'https://github.com/acme/org-skills.git' '/tmp/skills'");
});

test("buildCloneInvocation: an empty token configures no askpass at all", () => {
  const { env } = buildCloneInvocation({
    repoUrl: "/tmp/origin.git",
    destDir: "/tmp/dest",
    token: "",
    askpassPath: "/tmp/askpass.sh",
    baseEnv: {},
  });
  assert.equal(env.GIT_ASKPASS, undefined);
  assert.equal(env[CLONE_TOKEN_ENV], undefined);
});

test("buildCloneInvocation: does not mutate the caller's base env", () => {
  const baseEnv: NodeJS.ProcessEnv = { PATH: "/usr/bin" };
  buildCloneInvocation({
    repoUrl: "https://example.invalid/r.git",
    destDir: "/tmp/d",
    token: TOKEN,
    askpassPath: "/tmp/a.sh",
    baseEnv,
  });
  assert.deepEqual(baseEnv, { PATH: "/usr/bin" });
});

// ---- writeAskpassShim -------------------------------------------------------

test("writeAskpassShim: writes an executable shim that holds no secret", async () => {
  const dir = await tmpDir();
  const shim = await writeAskpassShim(dir);
  const body = await fs.promises.readFile(shim, "utf-8");

  assert.ok(!body.includes(TOKEN));
  // It reads the token from the environment rather than baking one in.
  assert.match(body, new RegExp(`\\$${CLONE_TOKEN_ENV}`));
  const mode = (await fs.promises.stat(shim)).mode & 0o777;
  assert.equal(mode, 0o700, `expected 0700, got ${mode.toString(8)}`);
});

test("writeAskpassShim: answers the username prompt and the password prompt", async () => {
  const dir = await tmpDir();
  const shim = await writeAskpassShim(dir);
  const env = { ...process.env, [CLONE_TOKEN_ENV]: TOKEN };

  const user = await execAsync(`${shellQuote(shim)} "Username for 'https://github.com': "`, { env });
  assert.equal(user.stdout.trim(), "x-access-token");

  const pass = await execAsync(`${shellQuote(shim)} "Password for 'https://github.com': "`, { env });
  assert.equal(pass.stdout.trim(), TOKEN);
});

// ---- the reported leak ------------------------------------------------------

test("cloneWithToken: a failed clone's error message carries no token", async () => {
  // Regression for the credential leak: the runner logs this error and the BFF
  // forwards it into the console build log. `.invalid` is reserved and never
  // resolves, reproducing the exact "Could not resolve host" failure that
  // surfaced the token.
  const dir = await tmpDir();
  await assert.rejects(
    cloneWithToken({
      repoUrl: "https://aep-does-not-exist.invalid/asdlc-repos/store.git",
      destDir: path.join(dir, "dest"),
      token: TOKEN,
      shimDir: path.join(dir, "stage"),
    }),
    (err: unknown) => {
      const text = err instanceof Error ? `${err.stack ?? ""}${err.message}` : String(err);
      assert.ok(text.includes("git clone"), `expected the clone command in the error, got: ${text}`);
      assert.ok(!text.includes(TOKEN), `clone error leaked the token: ${text}`);
      assert.ok(!text.includes("x-access-token"), `clone error leaked URL userinfo: ${text}`);
      return true;
    },
  );
});

test("cloneWithToken: a successful clone leaves no token at rest in the work tree", async () => {
  // The .git/config analogue of gitfs/hygiene_test.go: git preserves URL
  // userinfo verbatim, so an authenticated clone URL would persist the
  // credential in the cloned tree for the whole run.
  const dir = await tmpDir();
  const origin = path.join(dir, "origin.git");
  const seed = path.join(dir, "seed");

  await execAsync(`git init --bare -q ${shellQuote(origin)}`);
  await fs.promises.mkdir(seed, { recursive: true });
  await execAsync(`git init -q ${shellQuote(seed)}`);
  await fs.promises.writeFile(path.join(seed, "README.md"), "hello\n");
  await execAsync(`git -C ${shellQuote(seed)} add .`);
  await execAsync(
    `git -C ${shellQuote(seed)} -c user.name=T -c user.email=t@example.com commit -qm seed`,
  );
  await execAsync(`git -C ${shellQuote(seed)} push -q ${shellQuote(origin)} HEAD:refs/heads/main`);
  // `git init --bare` points HEAD at the local init.defaultBranch, which need
  // not be `main`; without this the clone checks out nothing.
  await execAsync(`git -C ${shellQuote(origin)} symbolic-ref HEAD refs/heads/main`);

  const dest = path.join(dir, "clone");
  await cloneWithToken({
    repoUrl: origin,
    destDir: dest,
    token: TOKEN,
    shimDir: path.join(dir, "stage"),
  });

  assert.ok(fs.existsSync(path.join(dest, "README.md")), "clone should have checked out the tree");

  // runner.ts spreads process.env into the agent's child env, so a token that
  // ever landed there would reach the agent and everything it spawns.
  assert.equal(
    process.env[CLONE_TOKEN_ENV],
    undefined,
    "clone token must never be written to process.env",
  );

  const offenders: string[] = [];
  const walk = async (d: string): Promise<void> => {
    for (const e of await fs.promises.readdir(d, { withFileTypes: true })) {
      const full = path.join(d, e.name);
      if (e.isDirectory()) {
        await walk(full);
      } else if (e.isFile()) {
        const body = await fs.promises.readFile(full);
        if (body.includes(TOKEN)) offenders.push(full);
      }
    }
  };
  await walk(dest);
  assert.deepEqual(offenders, [], `token found at rest in: ${offenders.join(", ")}`);
});
