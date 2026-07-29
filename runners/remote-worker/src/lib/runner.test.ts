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
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { buildMcpOptions, resolveBaseAgentConfig } from "./runner.js";

// D9 secure search (Task 12) — WebSearch joins the base tool set (gated by
// the PreToolUse DLP hook wired in runClaudeQuery; see websearch_dlp.ts).
// WebFetch joins it too (see webfetch_guard.ts's PreToolUse SSRF + secret
// guard, wired the same way) — fail-closed, so this is safe to enable.
// Task joins it for the milestone run loop's subagent fan-out (design §9.3).
const BASE_TOOLS = ["Read", "Write", "Edit", "Bash", "Glob", "Grep", "WebSearch", "WebFetch", "Task"];
const MCP_TOOLS = [
  "mcp__aep__list_org_component_endpoints",
  "mcp__aep__get_remote_git_file_contents",
  "mcp__aep__search_remote_git_code",
];

test("buildMcpOptions: registers the aep MCP server and tools when both envs are set", () => {
  const result = buildMcpOptions("https://bff.example.com/internal/v1/mcp", "mcp-token-xyz");

  assert.deepEqual(result.mcpServers, {
    aep: {
      type: "http",
      url: "https://bff.example.com/internal/v1/mcp",
      headers: { Authorization: "Bearer mcp-token-xyz" },
    },
  });
  assert.deepEqual(result.allowedTools, [...BASE_TOOLS, ...MCP_TOOLS]);
});

test("buildMcpOptions: omits mcpServers and MCP tools when the token is missing", () => {
  const result = buildMcpOptions("https://bff.example.com/internal/v1/mcp", undefined);

  assert.equal(result.mcpServers, undefined);
  assert.deepEqual(result.allowedTools, BASE_TOOLS);
});

test("buildMcpOptions: omits mcpServers and MCP tools when the url is missing", () => {
  const result = buildMcpOptions(undefined, "mcp-token-xyz");

  assert.equal(result.mcpServers, undefined);
  assert.deepEqual(result.allowedTools, BASE_TOOLS);
});

test("buildMcpOptions: omits mcpServers and MCP tools when both are empty strings", () => {
  const result = buildMcpOptions("", "");

  assert.equal(result.mcpServers, undefined);
  assert.deepEqual(result.allowedTools, BASE_TOOLS);
});

test("buildMcpOptions: omits mcpServers and MCP tools when both are undefined", () => {
  const result = buildMcpOptions(undefined, undefined);

  assert.equal(result.mcpServers, undefined);
  assert.deepEqual(result.allowedTools, BASE_TOOLS);
});

test("buildMcpOptions: allowedTools includes both WebSearch and WebFetch (D9)", () => {
  const result = buildMcpOptions(undefined, undefined);

  assert.ok(result.allowedTools.includes("WebSearch"));
  assert.ok(result.allowedTools.includes("WebFetch"));
});

// The milestone run loop fans big, independent issues out to subagents; without
// Task in allowedTools the `aep` skill's fan-out section is unexecutable.
test("buildMcpOptions: allowedTools includes Task, with and without MCP", () => {
  assert.ok(buildMcpOptions(undefined, undefined).allowedTools.includes("Task"));
  assert.ok(
    buildMcpOptions("https://bff.example.com/internal/v1/mcp", "mcp-token-xyz").allowedTools.includes("Task"),
  );
});

// Subagents inherit the parent's allowedTools, so the git tools stay in the set
// and the main-agent-is-sole-git-writer rule is enforced by the skill's
// deny-list, not by the tool list. Pinned so a future "just drop Bash for
// subagents" idea has to confront that the seam does not exist here.
test("buildMcpOptions: Bash stays in the base set alongside Task", () => {
  const tools = buildMcpOptions(undefined, undefined).allowedTools;
  assert.ok(tools.includes("Bash"));
  assert.ok(tools.includes("Task"));
});

// --- resolveBaseAgentConfig: production behavior is what you get when a caller
// passes nothing at all ------------------------------------------------------

const SHIPPED_PLUGIN = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../plugin");
const TASK_ID = "11111111-2222-3333-4444-555555555555";

test("resolveBaseAgentConfig: defaults pin today's plugin + preload exactly", () => {
  const impl = resolveBaseAgentConfig(undefined, "implementation", TASK_ID);
  assert.equal(impl.sourcePluginPath, SHIPPED_PLUGIN);
  assert.deepEqual(impl.preload, ["aep:aep"]);

  const val = resolveBaseAgentConfig(undefined, "validation", TASK_ID);
  assert.equal(val.sourcePluginPath, SHIPPED_PLUGIN);
  assert.deepEqual(val.preload, ["aep:aep", "aep:aep-validation"]);
});

// Mode is stated, never inferred — and the default is the production one, so a
// new entrypoint that forgets to state it gets the safe failure: a local run
// told to use `gh` dies on the first call, where a production run told there is
// no remote would quietly finish without opening its PR.
test("resolveBaseAgentConfig: mode defaults to github", () => {
  assert.equal(resolveBaseAgentConfig(undefined, "implementation", TASK_ID).mode, "github");
  assert.equal(resolveBaseAgentConfig(undefined, "validation", TASK_ID).mode, "github");
});

// The composed plugin must never land inside the workspace: in production that
// is a git clone the agent commits from, and in the playground it is the
// developer's own project directory.
test("resolveBaseAgentConfig: the default compose dir is a per-task dir under the OS temp dir", () => {
  const resolved = resolveBaseAgentConfig(undefined, "implementation", TASK_ID);
  assert.equal(resolved.composeDir, path.join(os.tmpdir(), "aep-base-plugin", TASK_ID));
});

test("resolveBaseAgentConfig: the playground's overrides ride through", () => {
  const local = resolveBaseAgentConfig(
    { basePluginPath: "/x/plugin", mode: "local", composeDir: "/x/run/base-plugin" },
    "implementation",
    TASK_ID,
  );
  assert.equal(local.sourcePluginPath, "/x/plugin");
  assert.equal(local.mode, "local");
  assert.equal(local.composeDir, "/x/run/base-plugin");
  // Local mode preloads the SAME skill identity as production — one plugin, one
  // skill name, only the composed body differs.
  assert.deepEqual(local.preload, ["aep:aep"]);
});

test("resolveBaseAgentConfig: an explicit basePreload owns the FULL list (no validation append)", () => {
  const pinned = resolveBaseAgentConfig({ basePreload: ["aep:aep"] }, "validation", TASK_ID);
  assert.deepEqual(pinned.preload, ["aep:aep"]);
});
