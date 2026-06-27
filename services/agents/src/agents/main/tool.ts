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
 * The file-mutation tools the main agent calls, built over a FileBundle.
 *
 * PROPERTY ORDER IS LOAD-BEARING. `path` is the first property in every schema
 * so the provider streams it first and a consumer can render the file header the
 * instant it resolves; the large string (`content` / `newString`) is last so it
 * streams delta-by-delta. The execute() return value IS what the model reads to
 * decide its next step.
 *
 * The Zod `inputSchema`s are the runtime validators; the corresponding wire
 * `*Input` types live in `@aep/contracts` (the source of truth). A compile-time
 * drift guard below asserts `z.infer<schema>` stays equal to each wire type.
 */

import { tool } from "ai";
import type { Tool } from "ai";
import { z } from "zod";
import type {
  AddFileInput,
  EditFileInput,
  RemoveFileInput,
  SetFrontmatterFieldInput,
  LoadSkillInput,
  LoadSkillResult,
  Skill,
} from "@aep/contracts";
import { FileBundle } from "./bundle.js";

export const ADD_FILE = "addFile" as const;
export const EDIT_FILE = "editFile" as const;
export const REMOVE_FILE = "removeFile" as const;
export const SET_FRONTMATTER_FIELD = "setFrontmatterField" as const;
/** Progressive-disclosure skill loader — registered only when skills are supplied (ADR-0002). */
export const LOAD_SKILL = "loadSkill" as const;
/** A tool for human-in-the-loop questions — implemented, but disabled. */
export const ASK_QUESTION = "ask_question" as const;

// --- Input schemas (runtime validators; their types are the wire `*Input`) ---

export const addFileInputSchema = z.object({
  path: z
    .string()
    .describe('New bundle path, e.g. "specs/design/components/foo/openapi.yaml". Must not already exist.'),
  content: z.string().describe("The full initial file body."),
});

export const editFileInputSchema = z.object({
  path: z.string().describe("Existing bundle path to edit."),
  oldString: z
    .string()
    .min(1)
    .describe("Verbatim snippet to replace, including its exact leading whitespace. Must occur exactly once."),
  newString: z.string().describe("Replacement text (may be empty to delete the snippet)."),
});

export const removeFileInputSchema = z.object({
  path: z.string().describe("Existing bundle path to delete."),
});

export const setFrontmatterFieldInputSchema = z.object({
  path: z.string().describe("Markdown file with leading --- frontmatter."),
  key: z.string().describe("Frontmatter key to set or add, e.g. 'buildpack' or 'skillsApplied'."),
  value: z
    .union([z.string(), z.number(), z.boolean(), z.array(z.string())])
    .describe("New value. Arrays render as a YAML block list."),
});

export const loadSkillInputSchema = z.object({
  name: z.string().describe("The skill name to load, exactly as listed in the Skills catalog."),
});

// --- Drift guard: Zod schema ⇄ @aep/contracts wire type ---------------------
// Compile-time only. If a schema's inferred input diverges from its wire type,
// the corresponding `true` is no longer assignable and this fails to compile,
// forcing the schema and contract back in sync. No meaningful runtime effect.
type Equal<A, B> =
  (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2 ? true : false;

const _drift: [
  Equal<z.infer<typeof addFileInputSchema>, AddFileInput>,
  Equal<z.infer<typeof editFileInputSchema>, EditFileInput>,
  Equal<z.infer<typeof removeFileInputSchema>, RemoveFileInput>,
  Equal<z.infer<typeof setFrontmatterFieldInputSchema>, SetFrontmatterFieldInput>,
  Equal<z.infer<typeof loadSkillInputSchema>, LoadSkillInput>,
] = [true, true, true, true, true];
void _drift;

// --- ask_question (HITL, Option B) — implemented but NOT registered ----------
// HAS an execute() returning a RESOLVED placeholder, so the transcript ends
// fully-resolved (no dangling tool_use → no MissingToolResultsError on
// persist/replay). Enabling HITL = uncomment the registration line in
// buildTools AND the paired `hasToolCall("ask_question")` stop condition at the
// call site. The user's answer arrives as the NEXT turn's plain user message.
// No test covers this path while disabled (§5).
export const askQuestionInputSchema = z.object({
  question: z.string().describe("The clarifying question to ask the user."),
});

export const askQuestionTool: Tool = tool({
  description:
    "Ask the user a clarifying question when the instruction is ambiguous and you cannot proceed safely. " +
    "Ends your turn; the user's answer arrives as the next message.",
  inputSchema: askQuestionInputSchema,
  execute: async ({ question }) => ({ status: "awaiting_user_response" as const, question }),
});

/**
 * Build the tool set bound to one bundle for the duration of a turn. When
 * `skills` is non-empty, also registers `loadSkill` (ADR-0002): it closes over a
 * name→content map and returns a skill's body on demand, or a self-correctable
 * miss listing the available names. No skills → no `loadSkill` (the catalog is
 * likewise omitted from the prompt), so a skill-free turn is byte-identical.
 */
export function buildTools(bundle: FileBundle, skills: readonly Skill[] = []): Record<string, Tool> {
  const tools: Record<string, Tool> = {
    [ADD_FILE]: tool({
      description:
        "Create a NEW file. The only tool that emits a whole body — use it for files that do not exist yet, " +
        "or (after removeFile) to replace a file wholesale. Errors with ALREADY_EXISTS if the path is already present.",
      inputSchema: addFileInputSchema,
      execute: async ({ path, content }) => bundle.addFile(path, content),
    }),

    [EDIT_FILE]: tool({
      description:
        "Change part of an existing file by replacing oldString with newString. oldString must be copied VERBATIM " +
        "from the file (including leading indentation and newlines) and must match EXACTLY ONE location. On NOT_UNIQUE, " +
        "broaden the anchor with surrounding lines; on NOT_FOUND, re-copy the snippet exactly. Use this for prose AND " +
        "openapi.yaml; for frontmatter keys prefer setFrontmatterField.",
      inputSchema: editFileInputSchema,
      execute: async ({ path, oldString, newString }) => bundle.editFile(path, oldString, newString),
    }),

    [REMOVE_FILE]: tool({
      description:
        "Delete a file. Idempotent (deleting an absent path is a NOOP success). Refuses to delete the structural roots " +
        "(requirements.md, design.md) with PROTECTED_PATH.",
      inputSchema: removeFileInputSchema,
      execute: async ({ path }) => bundle.removeFile(path),
    }),

    [SET_FRONTMATTER_FIELD]: tool({
      description:
        "Set a single YAML frontmatter field on a markdown file (the keys between the leading --- fences, e.g. " +
        "language, buildpack, skillsApplied). Owns the rendering so list/array values never need fragile multi-line " +
        "anchoring. Requires existing frontmatter (NO_FRONTMATTER otherwise).",
      inputSchema: setFrontmatterFieldInputSchema,
      execute: async ({ path, key, value }) => bundle.setFrontmatterField(path, key, value),
    }),

    // ask_question — human-in-the-loop follow-up. Implemented (Phase 5) but NOT
    // registered: enabling HITL = uncomment the line below AND the paired
    // hasToolCall("ask_question") stop condition at the call site. See §5/§10.
    // [ASK_QUESTION]: askQuestionTool,
  };

  if (skills.length > 0) {
    const byName = new Map(skills.map((s) => [s.name, s.content] as const));
    const available = skills.map((s) => s.name);
    tools[LOAD_SKILL] = tool({
      description:
        "Read the full guidance body of a skill by name (names are listed in the Skills catalog at the end of your " +
        "instructions). Call this before relying on a skill. Returns the skill's content, or an error listing the " +
        "available names if the name is unknown — re-call with one of those.",
      inputSchema: loadSkillInputSchema,
      execute: async ({ name }): Promise<LoadSkillResult> => {
        const content = byName.get(name);
        return content === undefined
          ? { ok: false, name, error: `unknown skill: ${name}`, available }
          : { ok: true, name, content };
      },
    });
  }

  return tools;
}
