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
 * The tasks screen: the `issues/*.md` table (dependsOn edges + a cheap
 * resolved-glyph) plus plan/replan and running the whole project in one
 * coding-agent session — the `aep` skill owns discovery, ordering and
 * fan-out itself (mirrors prod's milestone loop), so there is no per-issue
 * run here. Deliberately NO file affordances — viewing, editing, and
 * hand-authoring issue files happen in the user's editor.
 */

import { stdout as output } from "node:process";
import * as clack from "@clack/prompts";
import { FsIssueStore } from "../ports/issue-store.js";
import { projectSlug } from "../ports/spec-workspace.js";
import { issueLooksResolved } from "../state/status.js";

export type TasksAction = { kind: "plan" } | { kind: "code" } | { kind: "back" };

/** Render the tasks table; returns the action the CLI should run. */
export async function tasksScreen(projectDir: string): Promise<TasksAction> {
  const store = new FsIssueStore(projectDir, projectSlug(projectDir));
  const issues = store.list();
  if (issues.length === 0) output.write("  (no issues yet — plan first; hand-authored issues/<n>.md files are picked up too)\n");
  let pendingCount = 0;
  for (const i of issues) {
    const resolved = issueLooksResolved(projectDir, i.component);
    if (!resolved) pendingCount += 1;
    output.write(
      `  ${resolved ? "✓" : "·"} #${i.issueNumber} [${i.component}] ${i.title}${i.dependsOn.length ? ` ⇠ ${i.dependsOn.join(", ")}` : ""}\n`,
    );
  }

  const choice = await clack.select<string>({
    message: "Tasks",
    options: [
      { value: "plan", label: issues.length ? "re-plan (adds tasks for uncovered components)" : "plan tasks" },
      ...(pendingCount > 0 ? [{ value: "code", label: `run — ${pendingCount} issue(s) look pending, one session` }] : []),
      { value: "back", label: "back" },
    ],
  });
  if (clack.isCancel(choice) || choice === "back") return { kind: "back" };
  if (choice === "plan") return { kind: "plan" };
  return { kind: "code" };
}
