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
 * FS-derived phase status for the home menu (docs/design/playground.md §7).
 * Pure reads — the files ARE the state; nothing here caches or writes.
 */

import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { listComponents } from "../engine/gates.js";
import { FsIssueStore } from "../ports/issue-store.js";
import { projectSlug } from "../ports/spec-workspace.js";

export interface RequirementsStatus {
  present: boolean;
  sizeBytes: number;
  modifiedAt?: Date;
}

export function requirementsStatus(projectDir: string): RequirementsStatus {
  const file = join(projectDir, "specs/requirements/requirements.md");
  if (!existsSync(file)) return { present: false, sizeBytes: 0 };
  const st = statSync(file);
  return { present: st.size > 0, sizeBytes: st.size, modifiedAt: st.mtime };
}

export interface DesignStatus {
  components: string[];
  /** Union of every component design.json's skillsApplied. */
  skillsApplied: string[];
}

export function designStatus(projectDir: string): DesignStatus {
  const components = listComponents(projectDir);
  const skills = new Set<string>();
  for (const name of components) {
    const file = join(projectDir, "specs/design/components", name, "design.json");
    if (!existsSync(file)) continue;
    try {
      const parsed = JSON.parse(readFileSync(file, "utf8")) as { skillsApplied?: unknown };
      if (Array.isArray(parsed.skillsApplied)) {
        for (const s of parsed.skillsApplied) if (typeof s === "string") skills.add(s);
      }
    } catch {
      // unparseable design.json — the check command reports it; status stays quiet
    }
  }
  return { components, skillsApplied: [...skills].sort() };
}

/**
 * A component's App Path, from its `design.json` — undefined when the design
 * is missing or unparseable.
 */
function appPathFor(projectDir: string, component: string): string | undefined {
  const file = join(projectDir, "specs/design/components", component, "design.json");
  if (!existsSync(file)) return undefined;
  try {
    const parsed = JSON.parse(readFileSync(file, "utf8")) as { appPath?: unknown };
    return typeof parsed.appPath === "string" ? parsed.appPath : undefined;
  } catch {
    return undefined;
  }
}

/**
 * A cheap, TUI-only proxy for "looks done": does the component's App Path
 * exist and hold any files. This is NOT the real judgment — the `aep`
 * skill's own discovery step (does the App Path actually satisfy the issue)
 * is authoritative and runs at coding-run time. This is just good enough for
 * a list glyph, so the menu doesn't need to ask an agent to render.
 */
export function issueLooksResolved(projectDir: string, component: string): boolean {
  const appPath = appPathFor(projectDir, component);
  if (!appPath) return false;
  const dir = join(projectDir, appPath);
  if (!existsSync(dir) || !statSync(dir).isDirectory()) return false;
  return readdirSync(dir).length > 0;
}

export interface IssueSummary {
  file: string;
  issueNumber: number;
  component: string;
  title: string;
  dependsOn: string[];
  /** See `issueLooksResolved` — a display proxy, not ground truth. */
  resolved: boolean;
}

/**
 * Summary view of every parseable `issues/<n>.md` — ONE parser for the format
 * (the issue store's), so the menu counts and the tasks screen never disagree.
 */
export function listIssueSummaries(projectDir: string): IssueSummary[] {
  return new FsIssueStore(projectDir, projectSlug(projectDir)).list().map((i) => ({
    file: i.file,
    issueNumber: i.issueNumber,
    component: i.component,
    title: i.title,
    dependsOn: i.dependsOn,
    resolved: issueLooksResolved(projectDir, i.component),
  }));
}
