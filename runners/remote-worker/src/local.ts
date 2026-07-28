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

// Local-mode entrypoint (docs/design/playground.md §5 phase 4) — oneshot.ts's
// sibling for the AEP playground: the workspace IS a plain local project dir
// (no clone, no credhelper, no PAT), the task is an issue FILE, and the
// workflow skill is the `aep-local` plugin (edit-in-place, no PR). The same
// runClaudeQuery drives the SDK session; the same NDJSON progress contract
// streams on stdout; the same exit codes report the result:
//
//   0 — agent reported success
//   1 — agent reported failure
//   2 — setup/unexpected error before the agent ran
//
// Env contract (stamped by the playground CLI):
//   AEP_LOCAL_PROJECT_DIR  the project directory (becomes the cwd)
//   AEP_LOCAL_ISSUE_FILE   issue file, relative to the project dir
//   AEP_COMPONENT_NAME     the building component (selects design.json)
//   AEP_LOCAL_SKILLS_DIR   the working-tree skill library (optional)
//   AEP_LOCAL_PLUGIN_DIR   the base plugin (default: ../plugin-local)
//   AEP_LOCAL_RUN_DIR      scratch/log dir (default: <project>/.aep-playground/runner)
//   ANTHROPIC_API_KEY      the SDK session's key
//
// This module imports nothing outside the package — remote-worker stays
// workspace-dep-free for its standalone image; plugin-local is never loaded
// by the production entrypoint (oneshot.ts).

import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { runClaudeQuery } from "./lib/runner.js";
import { openTaskLog } from "./lib/logger.js";
import type { DispatchRequest } from "./lib/types.js";
import type { WorkspaceLayout } from "./lib/workspace.js";
import { emit, primeScrubber } from "./lib/progress/emitter.js";
import { installConsoleScrubber } from "./lib/progress/console_scrub.js";
import { resolveTaskSkills } from "./lib/skills_resolver.js";
import { materializeSkills } from "./lib/skills_materializer.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const DEFAULT_LOCAL_PLUGIN = path.resolve(__dirname, "../plugin-local");

function requireEnv(name: string): string {
  const v = process.env[name];
  if (v === undefined || v === "") {
    throw new Error(`missing required env var: ${name}`);
  }
  return v;
}

interface LocalRun {
  projectDir: string;
  issueFile: string;
  componentName: string;
  skillsDir?: string;
  pluginDir: string;
  runDir: string;
}

function readLocalRunFromEnv(): LocalRun {
  const projectDir = path.resolve(requireEnv("AEP_LOCAL_PROJECT_DIR"));
  const issueFile = requireEnv("AEP_LOCAL_ISSUE_FILE");
  const componentName = requireEnv("AEP_COMPONENT_NAME");
  requireEnv("ANTHROPIC_API_KEY");

  if (!fs.existsSync(projectDir) || !fs.statSync(projectDir).isDirectory()) {
    throw new Error(`AEP_LOCAL_PROJECT_DIR is not a directory: ${projectDir}`);
  }
  if (path.isAbsolute(issueFile) || issueFile.includes("..")) {
    throw new Error(`AEP_LOCAL_ISSUE_FILE must be a project-relative path: ${issueFile}`);
  }
  if (!fs.existsSync(path.join(projectDir, issueFile))) {
    throw new Error(`issue file not found: ${path.join(projectDir, issueFile)}`);
  }
  if (componentName.includes("/") || componentName.includes("..")) {
    throw new Error(`AEP_COMPONENT_NAME must not contain '/' or '..': ${componentName}`);
  }

  const skillsDir = process.env.AEP_LOCAL_SKILLS_DIR || undefined;
  if (skillsDir && !fs.existsSync(skillsDir)) {
    throw new Error(`AEP_LOCAL_SKILLS_DIR does not exist: ${skillsDir}`);
  }
  const pluginDir = process.env.AEP_LOCAL_PLUGIN_DIR || DEFAULT_LOCAL_PLUGIN;
  const runDir = process.env.AEP_LOCAL_RUN_DIR || path.join(projectDir, ".aep-playground", "runner");

  return { projectDir, issueFile, componentName, ...(skillsDir ? { skillsDir } : {}), pluginDir, runDir };
}

// The workspace IS the project dir; the auth-flavored layout fields point at
// empty scratch entries so runClaudeQuery's env composition stays untouched
// (an unauthenticated GH_CONFIG_DIR, an empty bearer file, an empty PATH dir).
function localDirWorkspace(run: LocalRun): WorkspaceLayout {
  const ghConfigDir = path.join(run.runDir, "gh-config");
  const aepDir = path.join(run.runDir, "bin");
  const bearerFile = path.join(run.runDir, "bearer");
  fs.mkdirSync(ghConfigDir, { recursive: true });
  fs.mkdirSync(aepDir, { recursive: true });
  fs.writeFileSync(bearerFile, "", "utf8");
  return {
    workspace: run.projectDir,
    ghConfigDir,
    bearerFile,
    aepDir,
    helperBin: path.join(aepDir, "credhelper"), // never provisioned in local mode
    ghWrapper: path.join(aepDir, "gh"), // never provisioned in local mode
  };
}

// The injected "clone": copy the working-tree skill library into the scratch
// dir under the flat org-skills layout (skills/<name>/…) the resolver reads.
async function copyLocalSkillLibrary(skillsDir: string, destDir: string): Promise<void> {
  await fs.promises.rm(destDir, { recursive: true, force: true });
  await fs.promises.mkdir(path.join(destDir, "skills"), { recursive: true });
  await fs.promises.cp(skillsDir, path.join(destDir, "skills"), { recursive: true });
}

async function main(): Promise<number> {
  installConsoleScrubber();

  let run: LocalRun;
  try {
    run = readLocalRunFromEnv();
  } catch (err) {
    console.error("[local] env validation failed:", err instanceof Error ? err.message : String(err));
    return 2;
  }

  primeScrubber([process.env.ANTHROPIC_API_KEY]);
  emit({ kind: "phase", phase: "workspace_provisioning" });

  let layout: WorkspaceLayout;
  try {
    layout = localDirWorkspace(run);
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    emit({ kind: "result", status: "failure", error: `workspace_provisioning: ${msg}` });
    return 2;
  }
  emit({ kind: "phase", phase: "workspace_ready" });

  // Per-task skills: same readSkillsApplied → resolveTaskSkills →
  // materializeSkills pipeline as production, with the git clone swapped for a
  // working-tree copy. Failure degrades to the base plugin, loudly.
  let preloadSkillNames: string[] = [];
  let skillsPluginDir: string | undefined;
  if (run.skillsDir) {
    try {
      const skillsDir = run.skillsDir;
      const resolutions = await resolveTaskSkills({
        workspace: run.projectDir,
        // The local harness runs one named component, not a milestone.
        scope: { kind: "component", componentName: run.componentName },
        skillsRepoURL: "local:working-tree",
        pat: "",
        scratchDir: path.join(run.runDir, "skills-clone"),
        log: (l) => console.log(l),
        clone: (_url, _pat, dest) => copyLocalSkillLibrary(skillsDir, dest),
      });
      // Materialize under the run dir — never inside the user's project tree.
      const result = await materializeSkills(run.runDir, resolutions);
      if (result) {
        skillsPluginDir = result.pluginDir;
        preloadSkillNames = result.preloadNames;
        console.log(`[local] materialised ${resolutions.length} skill(s); preload=${preloadSkillNames.length}`);
      } else {
        console.log("[local] no per-task skills to materialise");
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      console.warn(`[local] ⚠️  SKILLS UNAVAILABLE — proceeding without the per-task skill plugin: ${msg}`);
    }
  } else {
    console.log("[local] AEP_LOCAL_SKILLS_DIR not set — skipping per-task skills");
  }

  const req: DispatchRequest = {
    taskId: randomUUID(),
    orgId: "play",
    projectId: path.basename(run.projectDir).toLowerCase(),
    componentName: run.componentName,
    repoUrl: "",
    bearer: "",
    identity: { name: "AEP Playground", email: "playground@localhost" },
    gitServiceUrl: "",
    prompt:
      `Work on the task described in \`${run.issueFile}\` (relative to your cwd). Read it first. ` +
      "The workflow and constraints are in the `aep-local` skill. When done, update the issue " +
      "file's `derivedStatus` as the skill describes.",
    taskKind: "implementation",
  };

  const log = openTaskLog(run.runDir);
  const { completion } = runClaudeQuery(
    req,
    layout,
    log,
    { ...(skillsPluginDir ? { skillsPluginDir } : {}), preloadSkillNames },
    { basePluginPath: run.pluginDir, basePreload: ["aep-local:aep-local"] },
  );
  const result = await completion;
  return result.exitCode;
}

main()
  .then((code) => process.exit(code))
  .catch((err) => {
    console.error("[local] unhandled error:", err);
    process.exit(2);
  });
