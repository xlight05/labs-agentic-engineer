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
 * Pure programmatic scorers over `(expect, finalState, seed, results)`. No I/O,
 * no model calls; a missing file is a FAIL, never a throw. `finalState` is the
 * RECONSTRUCTED snapshot (the `applyToolCall` fold of the stream, §7);
 * `results` are the `OpResult`s harvested from the `tool-result` frames.
 */

import { parse as parseYaml } from "yaml";
import type { OpResult } from "@aep/contracts";
import type { Expect } from "./fixture.js";

export interface CheckResult {
  clause: string;
  pass: boolean;
  detail?: string;
}

type Files = Record<string, string>;

function mk(clause: string, pass: boolean, detail?: string): CheckResult {
  return detail === undefined ? { clause, pass } : { clause, pass, detail };
}

function short(s: string): string {
  const oneLine = s.replace(/\s+/g, " ").trim();
  return oneLine.length > 40 ? `${oneLine.slice(0, 40)}…` : oneLine;
}

/** One `CheckResult` per asserted clause (clauses left out of `expect` are not checked). */
export function scoreExpect(
  expect: Expect,
  finalState: Files,
  seed: Files,
  results: OpResult[],
  toolNames: string[] = [],
): CheckResult[] {
  const checks: CheckResult[] = [];

  for (const { path, text } of expect.filesContain ?? []) {
    const content = finalState[path];
    checks.push(
      mk(
        `filesContain ${path} ~ "${short(text)}"`,
        content !== undefined && content.includes(text),
        content === undefined ? "file absent" : undefined,
      ),
    );
  }

  for (const { path, text } of expect.filesNotContain ?? []) {
    const content = finalState[path];
    // Absent file trivially does not contain the text.
    checks.push(mk(`filesNotContain ${path} ~ "${short(text)}"`, content === undefined || !content.includes(text)));
  }

  for (const path of expect.filesAbsent ?? []) {
    checks.push(mk(`filesAbsent ${path}`, finalState[path] === undefined));
  }

  for (const { path, pattern } of expect.fileMatches ?? []) {
    const content = finalState[path];
    let pass = false;
    let detail: string | undefined;
    if (content === undefined) detail = "file absent";
    else {
      try {
        pass = new RegExp(pattern).test(content);
      } catch (e) {
        detail = `bad pattern: ${e instanceof Error ? e.message : String(e)}`;
      }
    }
    checks.push(mk(`fileMatches ${path} =~ /${short(pattern)}/`, pass, detail));
  }

  for (const path of expect.touched ?? []) {
    // Touched = changed relative to the pre-turn snapshot (add/edit/remove).
    checks.push(mk(`touched ${path}`, seed[path] !== finalState[path]));
  }

  for (const path of expect.yamlValid ?? []) {
    const content = finalState[path];
    let pass = false;
    let detail: string | undefined;
    if (content === undefined) detail = "file absent";
    else {
      try {
        parseYaml(content);
        pass = true;
      } catch (e) {
        detail = e instanceof Error ? e.message : String(e);
      }
    }
    checks.push(mk(`yamlValid ${path}`, pass, detail));
  }

  if (expect.noToolErrors === true) {
    const failed = results.filter((r) => !r.ok);
    checks.push(mk("noToolErrors", failed.length === 0, failed.length ? `${failed.length} failed op(s)` : undefined));
  }

  if (expect.preferEdit === true) {
    const edits = results.filter((r) => r.op === "edit").length;
    const adds = results.filter((r) => r.op === "add").length;
    checks.push(mk("preferEdit", edits >= adds, `edits=${edits} adds=${adds}`));
  }

  for (const toolName of expect.toolsUsed ?? []) {
    checks.push(mk(`toolsUsed ${toolName}`, toolNames.includes(toolName)));
  }

  return checks;
}

/** A clause set passes when every check passes (an empty set passes vacuously). */
export function allPass(checks: CheckResult[]): boolean {
  return checks.every((c) => c.pass);
}
