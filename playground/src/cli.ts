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

/**
 * Entry point (docs/design/playground.md §7):
 *
 *   pnpm play                                → project picker → phase menu
 *   pnpm play <dir>                          → phase menu
 *   pnpm play <dir> requirements|design|chat → run one phase; exit code = result
 *   pnpm play <dir> tasks|code|check|undo    → later steps of the impl plan
 *
 * Flags: --idea "<text>", --target "<x>", --fresh, --silent.
 */

import "./devtools-default.js"; // MUST be first: sets AGENT_DEVTOOLS before the agents config loads
import { existsSync, statSync } from "node:fs";
import { parseArgs } from "node:util";
import { stdout as output } from "node:process";
import * as clack from "@clack/prompts";
import { loadRepoSkills } from "./kit/skills.js";
import { loadDotenv } from "@aep/agents/shared/env";
import {
  chatTurn,
  codeCommand,
  designCommand,
  requirementsCommand,
  tasksCommand,
  undoCommand,
  type CodeOptions,
  type PhaseOptions,
  type PhaseOutcome,
} from "./commands.js";
import { checkProject } from "./engine/check.js";
import { openSession, SKILLS_DIR } from "./engine/session.js";
import { expandProjectPath, projectDirError } from "./paths.js";
import { projectSlug } from "./ports/spec-workspace.js";
import { pickProject } from "./tui/picker.js";
import { phaseMenu, type MenuAction } from "./tui/phase-menu.js";
import { chatLoop } from "./tui/chat.js";
import { tasksScreen } from "./tui/tasks.js";

const COMMANDS = new Set(["requirements", "design", "tasks", "code", "chat", "check", "undo", "menu"]);

async function askIdea(): Promise<string | null> {
  const idea = await clack.text({
    message: "What should this project be? (the create prompt)",
    placeholder: "An online store for handmade ceramics",
  });
  return clack.isCancel(idea) ? null : idea;
}

function confirmCodingDir(projectDir: string): () => Promise<boolean> {
  return async () => {
    if (!process.stdin.isTTY) return false;
    const ok = await clack.confirm({
      message: `The coding agent runs with permissions BYPASSED and will write inside ${projectDir}. A restorable undo snapshot is taken first. Continue?`,
    });
    return !clack.isCancel(ok) && ok;
  };
}

function printCheckFindings(projectDir: string): boolean {
  let allOk = true;
  for (const f of checkProject(projectDir)) {
    output.write(f.ok ? `  ✓ ${f.path} — ${f.message}\n` : `  ✗ ${f.path} — ${f.message}\n`);
    if (!f.ok) allOk = false;
  }
  return allOk;
}

async function runHeadless(
  command: string,
  projectDir: string,
  opts: CodeOptions,
  commandArg?: string,
): Promise<number> {
  let outcome: PhaseOutcome;
  switch (command) {
    case "requirements":
      outcome = await requirementsCommand(projectDir, opts, process.stdin.isTTY ? askIdea : undefined);
      break;
    case "design":
      outcome = await designCommand(projectDir, opts);
      break;
    case "tasks":
      outcome = await tasksCommand(projectDir, opts);
      break;
    case "code":
      // One session works the whole project — the `aep` skill decides
      // discovery, ordering and fan-out (see its SKILL.md).
      outcome = await codeCommand(projectDir, opts, confirmCodingDir(projectDir));
      break;
    case "undo":
      outcome = undoCommand(projectDir, opts);
      break;
    case "chat": {
      // Headless one-shot chat turn: `play <dir> chat "message"` — same
      // general conversation as the TUI chat screen (scriptable follow-ups).
      if (!commandArg) {
        output.write('usage: play <dir> chat "<message>"\n');
        return 1;
      }
      const session = await openSession(projectDir, opts);
      try {
        outcome = await chatTurn(session, commandArg, opts);
      } finally {
        await session.close();
      }
      break;
    }
    case "check":
      return printCheckFindings(projectDir) ? 0 : 1;
    default:
      output.write(`"${command}" is not wired yet (see docs/design/playground.md §13)\n`);
      return 2;
  }
  if (!outcome.ok) {
    output.write(`✗ ${command}: ${outcome.detail ?? "failed"}\n`);
    return 1;
  }
  output.write(`✓ ${command} done\n`);
  return 0;
}

async function runMenu(projectDir: string, opts: PhaseOptions): Promise<number> {
  clack.intro("AEP playground");
  for (;;) {
    const skillCount = loadRepoSkills(SKILLS_DIR).length;
    const action: MenuAction = await phaseMenu(projectDir, projectSlug(projectDir), skillCount);
    if (action === "quit") break;
    if (action === "code") {
      // One session works the whole project (VS Code is the file browser —
      // no per-issue picking, no review detour).
      await runHeadless("code", projectDir, opts);
      continue;
    }
    if (action === "tasks") {
      const tasksAction = await tasksScreen(projectDir);
      if (tasksAction.kind === "plan") await runHeadless("tasks", projectDir, opts);
      if (tasksAction.kind === "code") await runHeadless("code", projectDir, opts);
      continue;
    }
    if (action === "chat") {
      const session = await openSession(projectDir, opts);
      try {
        const next = await chatLoop(session, opts);
        if (next === "quit") break;
      } finally {
        await session.close();
      }
      continue;
    }
    const code = await runHeadless(action, projectDir, opts);
    if (code === 2) continue; // unwired action — back to the menu
  }
  clack.outro("bye");
  return 0;
}

async function main(): Promise<number> {
  // Load deployments/.env up front: the coding path spawns the runner with
  // the CLI's env (no openSession), so ANTHROPIC_API_KEY must be resolved here.
  loadDotenv();
  const { values, positionals } = parseArgs({
    args: process.argv.slice(2),
    options: {
      idea: { type: "string" },
      target: { type: "string" },
      fresh: { type: "boolean" },
      silent: { type: "boolean" },
      restore: { type: "boolean" },
      yes: { type: "boolean" },
    },
    allowPositionals: true,
  });

  const opts: CodeOptions = {
    ...(values.idea ? { idea: values.idea } : {}),
    ...(values.target ? { target: values.target } : {}),
    ...(values.fresh ? { fresh: true } : {}),
    ...(values.silent ? { silent: true } : {}),
    ...(values.restore ? { restore: true } : {}),
    ...(values.yes ? { yes: true } : {}),
  };

  let [dirArg, command, commandArg] = positionals as [string | undefined, string | undefined, string | undefined];
  // `pnpm play requirements` inside a project dir: first positional is a command.
  if (dirArg && COMMANDS.has(dirArg) && !existsSync(expandProjectPath(dirArg))) {
    commandArg = command;
    command = dirArg;
    dirArg = undefined;
  }

  // paths.ts is the single fence: relative paths resolve against the user's
  // invocation dir (INIT_CWD — pnpm rewrites the process cwd to the package),
  // and inside the repo only the gitignored playground/projects/ subtree is
  // a legal project home.
  let projectDir = dirArg ? expandProjectPath(dirArg) : null;
  if (projectDir && (!existsSync(projectDir) || !statSync(projectDir).isDirectory())) {
    output.write(`not a directory: ${projectDir}\n`);
    return 1;
  }
  if (!projectDir) {
    if (command && !process.stdin.isTTY) {
      output.write("a project directory is required in headless mode\n");
      return 1;
    }
    projectDir = await pickProject();
    if (!projectDir) return 0;
  }
  // Fence EVERY resolved project dir — CLI argument or picker result (stale
  // recents could carry a pre-fence path).
  const fenceError = projectDirError(projectDir);
  if (fenceError) {
    output.write(`${fenceError}\n`);
    return 1;
  }

  if (command && command !== "menu") return runHeadless(command, projectDir, opts, commandArg);
  return runMenu(projectDir, opts);
}

main().then(
  (code) => {
    process.exitCode = code;
  },
  (err: unknown) => {
    output.write(`playground error: ${err instanceof Error ? (err.stack ?? err.message) : String(err)}\n`);
    process.exitCode = 1;
  },
);
