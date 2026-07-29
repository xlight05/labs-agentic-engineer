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

import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { query, type McpServerConfig, type Query } from "@anthropic-ai/claude-agent-sdk";
import { composeBasePlugin, type AgentMode } from "./skill_compose.js";
import type { TaskLog } from "./logger.js";
import type { DispatchRequest } from "./types.js";
import type { WorkspaceLayout } from "./workspace.js";
import { emit } from "./progress/emitter.js";
import { progressFromSdkMessage } from "./progress/from-sdk.js";
import { createWebSearchDlpHook, stagedSecretValues } from "./websearch_dlp.js";
import { createWebFetchGuardHook } from "./webfetch_guard.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const PLUGIN_PATH = path.resolve(__dirname, "../../plugin");

// Phase 0 allowed-tools: git, gh, build/test/lint via Bash; standard file
// tools. Endpoint Spec Discovery (B2) re-introduces MCP — but only as an
// in-process remote HTTP server (see buildMcpOptions below), never the
// file-based .mcp.json that settingSources: [] deliberately blocks.
// D9 secure search (Task 12) adds WebSearch, gated by the PreToolUse DLP
// hook wired in runClaudeQuery below (see websearch_dlp.ts for why
// PreToolUse, not canUseTool, and .superpowers/sdd/task-12-report.md for
// the spike evidence). WebFetch (external API/SDK doc + spec-URL fetches)
// is added alongside it, gated by its own PreToolUse SSRF + secret guard
// (see webfetch_guard.ts) — fail-closed, so pod egress to arbitrary
// fetched pages never reaches internal/private/link-local/metadata
// addresses or leaks a staged secret in the URL.
// Task joins the set for the milestone run loop (docs/design §9.3): a cycle
// works several issues, and the main agent fans the big, prose-independent,
// disjoint-App-Path ones out to subagents. The main agent stays the SOLE git
// writer — subagents Edit/Write only. That split is a SKILL rule, not a tool
// restriction: the SDK hands a subagent the same allowedTools as its parent,
// so `aep`'s deny-list is what keeps a subagent off git, and its fan-out
// section is what keeps small issues inline.
const BASE_ALLOWED_TOOLS = ["Read", "Write", "Edit", "Bash", "Glob", "Grep", "WebSearch", "WebFetch", "Task"];

// The server key the BFF MCP endpoint is registered under. The SDK
// namespaces MCP tools as `mcp__<serverKey>__<toolName>` (confirmed from
// node_modules/@anthropic-ai/claude-agent-sdk/sdk.d.ts, SDKControlMcpCallRequest
// doc comment: "Fully-qualified MCP tool name, e.g. mcp__server__tool_name.").
const MCP_SERVER_KEY = "aep";

// The three read-only tools the BFF's aep-api-mcp server exposes (see
// services/aep-api/internal/feature/dependencies/mcp_tools.go). Namespaced
// per the SDK's mcp__<server>__<tool> convention above.
const MCP_TOOL_NAMES = [
  `mcp__${MCP_SERVER_KEY}__list_org_component_endpoints`,
  `mcp__${MCP_SERVER_KEY}__get_remote_git_file_contents`,
  `mcp__${MCP_SERVER_KEY}__search_remote_git_code`,
];

export interface McpQueryOptions {
  mcpServers?: Record<string, McpServerConfig>;
  allowedTools: string[];
}

// buildMcpOptions is a pure seam so the env-presence guard is unit-testable
// without constructing a full query(). Both mcpUrl and mcpToken must be
// present — the BFF's coding-agent Job template stamps AEP_MCP_URL
// unconditionally but only stamps AEP_MCP_TOKEN when minting succeeded
// (see job_template.go), so a URL-without-token dispatch must still omit
// the server rather than register it unauthenticated.
export function buildMcpOptions(mcpUrl: string | undefined, mcpToken: string | undefined): McpQueryOptions {
  if (!mcpUrl || !mcpToken) {
    return { allowedTools: BASE_ALLOWED_TOOLS };
  }
  return {
    mcpServers: {
      [MCP_SERVER_KEY]: {
        type: "http",
        url: mcpUrl,
        headers: { Authorization: `Bearer ${mcpToken}` },
      },
    },
    allowedTools: [...BASE_ALLOWED_TOOLS, ...MCP_TOOL_NAMES],
  };
}

export interface RunResult {
  exitCode: number;
  error?: string;
}

export interface StartedRun {
  query: Query;
  completion: Promise<RunResult>;
}

// PerTaskSkills carries the materialised AgentSkills plugin into the SDK
// query options (built by skills_resolver.ts + skills_materializer.ts).
//
// `skillsPluginDir` is the absolute path to .aep/skills-plugin/. If
// set, runner.ts adds a second `{type:"local"}` plugin entry pointing
// at it. `preloadSkillNames` lists the materialised names of every
// platform-shipped (`kind: org`) skill in that plugin, which we push
// into the SDK's `skills:` array so their full bodies inject at
// startup. Custom and imported skills sit in the same plugin and
// surface via the SDK's standard skill listing (description in
// context, body on invoke) — they are NOT in the preload array,
// because a run must not pay for every attached skill's full body
// when only the stack ones are certain to apply.
export interface PerTaskSkills {
  skillsPluginDir?: string;
  preloadSkillNames: string[];
}

// BaseAgentConfig parameterizes what the always-loaded workflow plugin is and
// how it is composed. Every field defaults to production behavior, so
// `oneshot.ts` passes nothing at all (pinned in runner.test.ts).
//
// `mode` is stated by the caller, never inferred. The entrypoint unambiguously
// knows which flavour of run this is; deriving it from something incidental (an
// empty `repoUrl`, an absent MCP token) would tie the agent's whole procedure to
// a signal whose meaning could shift for an unrelated reason. It defaults to
// `github` because that is the safe direction: a local run mistakenly composed
// for GitHub fails loudly the first time `gh` is invoked, whereas a production
// run mistakenly composed for local would be told there is no remote and no PR
// to open — the exact opposite of its contract.
//
// A caller that overrides `basePreload` owns the FULL preload list — the
// validation-task append only applies to the default.
export interface BaseAgentConfig {
  /** The authored workflow plugin dir; defaults to the shipped `plugin/`. */
  basePluginPath?: string;
  /** The startup `skills:` preload; defaults to ["aep:aep"] (+ the validation body for validation tasks). */
  basePreload?: string[];
  /** Which mode to compose the workflow skill for; defaults to "github". */
  mode?: AgentMode;
  /** Where the composed plugin is written; defaults to a per-task dir under the OS temp dir. */
  composeDir?: string;
}

export interface ResolvedBaseAgentConfig {
  /** The authored plugin dir to compose FROM (not what the SDK loads). */
  sourcePluginPath: string;
  preload: string[];
  mode: AgentMode;
  composeDir: string;
}

// resolveBaseAgentConfig is a pure seam (the buildMcpOptions pattern) so the
// defaults are unit-pinnable without constructing a query() or touching disk.
export function resolveBaseAgentConfig(
  base: BaseAgentConfig | undefined,
  taskKind: DispatchRequest["taskKind"],
  taskId: string,
): ResolvedBaseAgentConfig {
  const sourcePluginPath = base?.basePluginPath ?? PLUGIN_PATH;
  const mode = base?.mode ?? "github";
  // Outside the workspace in both modes: in production the workspace is a git
  // clone the agent commits from, and in the playground it is the developer's
  // own project dir. Neither should grow a copy of the plugin tree.
  const composeDir = base?.composeDir ?? path.join(os.tmpdir(), "aep-base-plugin", taskId);
  const preload = base?.basePreload ? [...base.basePreload] : defaultPreload(taskKind);
  return { sourcePluginPath, preload, mode, composeDir };
}

function defaultPreload(taskKind: DispatchRequest["taskKind"]): string[] {
  const preload = ["aep:aep"];
  if (taskKind === "validation") preload.push("aep:aep-validation");
  return preload;
}

export function runClaudeQuery(
  req: DispatchRequest,
  layout: WorkspaceLayout,
  log: TaskLog,
  perTaskSkills?: PerTaskSkills,
  base?: BaseAgentConfig,
): StartedRun {
  // Spawn env: bearer + git-service URL passed by file path / URL only.
  // No tokens cross via env, so transcripts cannot leak credentials.
  // ANTHROPIC_API_KEY flows through from process.env (container env).
  // F3c — surface AEP_TASK_ID and AEP_PLATFORM_URL to the agent's
  // child env so the aep skill's verification-failed shell snippet can
  // hit POST $AEP_PLATFORM_URL/api/v1/tasks/$AEP_TASK_ID/verification-failed.
  // The bearer rides through a file (AEP_BEARER_FILE) so the agent's
  // SDK transcripts can't leak it; the curl snippet reads the file at
  // call time.
  const childEnv: Record<string, string> = {
    ...(process.env as Record<string, string>),
    PATH: `${layout.aepDir}:${process.env.PATH ?? ""}`,
    GH_CONFIG_DIR: layout.ghConfigDir,
    AEP_BEARER_FILE: layout.bearerFile,
    AEP_TASK_ID: req.taskId,
    AEP_PLATFORM_URL: process.env.AEP_PLATFORM_URL ?? "",
    AEP_GIT_SERVICE_URL: req.gitServiceUrl,
    AEP_CORRELATION_ID: req.correlationId ?? "",
  };

  // Two-tier plugin list: the base `aep` plugin (workflow + base
  // conventions) is always loaded; the per-task `aep-task-skills`
  // plugin (project-attached skills) is loaded conditionally when
  // workspace.ts materialised it. Per-task org skills land in the
  // `skills:` preload so the SDK injects their full bodies at startup;
  // custom + imported sit in the same plugin and surface via the SDK's
  // standard discovery (description in context, body on invoke).
  // Related-issue discovery/cross-linking moved to the SRE agent's handoff
  // stage (a "## Related issues" section in the issue body; GitHub #N
  // mentions back-link automatically) — issues arrive pre-linked, so the
  // former aep:related-issues preload is gone. See AE-HANDOFF-DESIGN.md in
  // openchoreo/agents/sre-agent.
  // Validation tasks additionally preload the validation workflow body:
  // it replaces the implementation workflow and the run cannot afford
  // the agent skipping a description-triggered load of it.
  //
  // The base plugin is COMPOSED, never loaded from the authored tree: the `aep`
  // skill carries `<!-- mode:… -->` blocks for the GitHub/local split, and this
  // is the single choke point that resolves them (see skill_compose.ts). Doing
  // it here rather than in each entrypoint means no caller can forget and ship a
  // session the raw source, markers and both procedures included.
  const resolvedBase = resolveBaseAgentConfig(base, req.taskKind, req.taskId);
  const basePluginDir = composeBasePlugin({
    sourceDir: resolvedBase.sourcePluginPath,
    destDir: resolvedBase.composeDir,
    mode: resolvedBase.mode,
  });
  const plugins: Array<{ type: "local"; path: string }> = [
    { type: "local", path: basePluginDir },
  ];
  const skillPreload: string[] = resolvedBase.preload;
  if (perTaskSkills?.skillsPluginDir) {
    plugins.push({ type: "local", path: perTaskSkills.skillsPluginDir });
    for (const name of perTaskSkills.preloadSkillNames) {
      skillPreload.push(`aep-task-skills:${name}`);
    }
  }

  // Endpoint Spec Discovery (B2) — register the BFF's MCP server in-process
  // when the dispatch carries both AEP_MCP_URL and AEP_MCP_TOKEN. Older
  // dispatches (or a failed token mint) omit one or both, in which case the
  // runner falls back to the base tool set unchanged.
  const { mcpServers, allowedTools } = buildMcpOptions(req.mcpUrl, req.mcpToken);

  // D9 secure search (Task 12) — DLP gate for the server-side WebSearch
  // tool. Secret candidates are read from childEnv, the SAME env record
  // injected into this run (see websearch_dlp.ts's stagedSecretValues doc
  // comment): staged dependency secrets (Tasks 9-11's per-run K8s Secrets,
  // mounted via envFrom) and the runner's own credentials both land there
  // before the query() call below, so this is the single source of truth
  // for "what's secret in this run" without a second, drift-prone channel.
  const stagedSecrets = stagedSecretValues(childEnv);
  const webSearchDlpHook = createWebSearchDlpHook(stagedSecrets);

  // WebFetch SSRF + secret-leak guard (see webfetch_guard.ts) — built from
  // the SAME staged-secret list as the WebSearch hook above, one source of
  // truth for "what's secret in this run".
  const webFetchGuardHook = createWebFetchGuardHook(stagedSecrets);

  // SDK v0.2.126 auto-discovers the bundled native binary — no
  // pathToClaudeCodeExecutable needed. settingSources: [] ensures no
  // host filesystem settings leak into the container agent.
  const q = query({
    prompt: req.prompt,
    options: {
      cwd: layout.workspace,
      plugins,
      // Force built-in skill bodies into context at startup. Do NOT
      // replace with 'all': that would inject every custom + imported
      // skill's body too, which is what the on-demand listing is for.
      skills: skillPreload as unknown as string[],
      allowedTools,
      ...(mcpServers ? { mcpServers } : {}),
      permissionMode: "bypassPermissions",
      allowDangerouslySkipPermissions: true,
      persistSession: false,
      settingSources: [],
      env: childEnv,
      // NOT canUseTool — the Task 12 spike found canUseTool is never
      // invoked for the server-executed WebSearch tool (confirmed under
      // bypassPermissions above too). PreToolUse is the mechanism that
      // actually gates it pre-dispatch. See websearch_dlp.ts. WebFetch is
      // a genuine local dispatch (it actually dials out), but is gated the
      // same way for consistency and because PreToolUse is still the
      // earliest point to deny before any egress happens. See
      // webfetch_guard.ts.
      hooks: {
        PreToolUse: [
          { matcher: "WebSearch", hooks: [webSearchDlpHook] },
          { matcher: "WebFetch", hooks: [webFetchGuardHook] },
        ],
      },
    },
  });

  const completion = (async (): Promise<RunResult> => {
    try {
      for await (const message of q) {
        log.write(message);
        for (const event of progressFromSdkMessage(message)) {
          emit(event);
        }
        if (message.type === "result") {
          if (message.subtype === "success") {
            return { exitCode: 0 };
          }
          const errors =
            "errors" in message && Array.isArray(message.errors)
              ? (message.errors as string[])
              : [];
          return {
            exitCode: 1,
            error: `agent result ${message.subtype}${errors.length ? ": " + errors.join(", ") : ""}`,
          };
        }
      }
      emit({ kind: "log", level: "warn", summary: "agent stream ended without result" });
      return { exitCode: 1, error: "agent stream ended without result" };
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      log.write({ type: "worker_error", error: msg });
      emit({ kind: "result", status: "failure", error: msg });
      return { exitCode: 1, error: msg };
    } finally {
      log.close();
    }
  })();

  return { query: q, completion };
}
