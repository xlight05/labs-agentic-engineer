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
 * The reviewable change (§7). Two pure helpers over a single stream part:
 *
 *  - `toChange` projects ONE `tool-result` part into a `Change` (browser UI).
 *  - `applyToolCall` folds ONE streamed `tool-call` into a `FileBundle` via the
 *    CANONICAL ops, so a consumer can RECONSTRUCT file state from the stream
 *    (tool results no longer carry `newContent`, §5). The eval uses this; the
 *    browser ports it. No second matcher, no delta accumulation.
 */

import type { Change, Op } from "@aep/contracts";
import type { OpResult } from "./bundle.js";
import type { FileBundle } from "./bundle.js";
import type { StreamPart } from "./stream-types.js";

/** Wire tool name → op. Mirrors the four file-mutation tools in `tool.ts`. */
const OP_BY_TOOL: Record<string, Op> = {
  addFile: "add",
  editFile: "edit",
  removeFile: "remove",
  setFrontmatterField: "frontmatter",
};

/**
 * True only for the four file-mutation tools — i.e. the tool-results that project
 * to a reviewable `Change` and fold into the bundle. Non-mutating tools like
 * `loadSkill` (ADR-0002) return false, so consumers exclude them from the `Change`
 * stream and the `OpResult` harvest (a `loadSkill` result is not an `OpResult`).
 */
export function isFileMutationTool(toolName: string): boolean {
  return Object.hasOwn(OP_BY_TOOL, toolName);
}

function isFrontmatterValue(x: unknown): x is string | number | boolean | string[] {
  return (
    typeof x === "string" ||
    typeof x === "number" ||
    typeof x === "boolean" ||
    (Array.isArray(x) && x.every((e) => typeof e === "string"))
  );
}

/**
 * Pure field projection of ONE `tool-result` part → a reviewable `Change`. The
 * part carries everything in one frame (`input` + `output`), so any consumer
 * builds it from the part it already forwards. The op prefers the authoritative
 * `result.op`, falling back to the tool name.
 */
export function toChange(part: StreamPart): Change {
  const input = (part.input ?? {}) as Record<string, unknown>;
  const result = part.output as OpResult;
  const toolName = part.toolName ?? "";
  const op: Op = result?.op ?? OP_BY_TOOL[toolName] ?? "edit";
  const path =
    typeof input.path === "string" ? input.path : (result?.path ?? "");

  const change: Change = {
    toolCallId: part.toolCallId ?? "",
    toolName,
    op,
    path,
    result,
  };
  if (typeof input.oldString === "string") change.oldString = input.oldString;
  if (typeof input.newString === "string") change.newString = input.newString;
  if (typeof input.content === "string") change.content = input.content;
  if (typeof input.key === "string") change.key = input.key;
  if (isFrontmatterValue(input.value)) change.value = input.value;
  return change;
}

/**
 * Reference applier: fold ONE streamed `tool-call` into `bundle` via the
 * canonical ops, reproducing the server's result with no second matcher. Fold
 * in stream order over a fresh `new FileBundle(files)` to reconstruct the final
 * files. Unknown / malformed tool calls are ignored.
 */
export function applyToolCall(bundle: FileBundle, part: StreamPart): void {
  const input = part.input;
  if (!input || typeof input !== "object") return;
  const v = input as Record<string, unknown>;
  switch (part.toolName) {
    case "addFile":
      if (typeof v.path === "string" && typeof v.content === "string") {
        bundle.addFile(v.path, v.content);
      }
      break;
    case "editFile":
      if (
        typeof v.path === "string" &&
        typeof v.oldString === "string" &&
        typeof v.newString === "string"
      ) {
        bundle.editFile(v.path, v.oldString, v.newString);
      }
      break;
    case "removeFile":
      if (typeof v.path === "string") bundle.removeFile(v.path);
      break;
    case "setFrontmatterField":
      if (typeof v.path === "string" && typeof v.key === "string" && isFrontmatterValue(v.value)) {
        bundle.setFrontmatterField(v.path, v.key, v.value);
      }
      break;
  }
}
