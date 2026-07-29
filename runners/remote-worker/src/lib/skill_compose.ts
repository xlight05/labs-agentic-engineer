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

// Composes the base workflow plugin for ONE agent mode.
//
// There is a single authored `aep` skill (plugin/skills/aep/SKILL.md) serving
// both the platform's GitHub-backed runs and the playground's file-based local
// runs. The handful of places where the two genuinely differ — how the working
// set is discovered, how dependencies are ordered, branch identity, how a run
// finishes, and which platform-side machinery (MCP contract lookup, resolved
// dependency comments) exists at all — are marked inline:
//
//     <!-- mode:github -->
//     Discover the working set with `gh issue list --milestone …`
//     <!-- /mode -->
//     <!-- mode:local -->
//     Discover the working set by listing `issues/<n>.md`
//     <!-- /mode -->
//
// This module strips the blocks that don't apply and writes the resolved plugin
// to a scratch dir, which is what the SDK session actually loads. Everything
// outside a marked block is shared text and cannot drift between the two modes,
// which is the whole point: before this, local mode was a second plugin
// (`plugin-local`, skill `aep-local`) whose "shared" project conventions were
// copy-pasted and pinned by a parity test. See
// `runners/remote-worker/design/decisions/ADR-0001-one-mode-composed-skill.md`.
//
// Both modes go through the strip step, not just local. Markers are HTML
// comments, which are invisible in rendered markdown but perfectly readable to
// a model — leaving a `mode:local` block in a production session would inject
// "there is no remote, never run gh" into a run whose entire contract is
// opening a pull request. There is no free mode.

import fs from "node:fs";
import path from "node:path";

/**
 * Which flavour of the base workflow skill to compose.
 *
 * `github` — the platform's dispatched run: a repo clone, the issues API,
 * branch identity, one PR. `local` — the playground: a plain project dir,
 * `issues/*.md`, no remote, a progress note.
 */
export type AgentMode = "github" | "local";

const MODES: readonly AgentMode[] = ["github", "local"];

const BLOCK_OPEN = /^<!--\s*mode:([A-Za-z0-9_-]+)\s*-->$/;
const BLOCK_CLOSE = /^<!--\s*\/mode\s*-->$/;
const ANY_MARKER = /<!--\s*(?:mode:([A-Za-z0-9_-]+)|\/mode)\s*-->/g;

function isAgentMode(value: string): value is AgentMode {
  return (MODES as readonly string[]).includes(value);
}

function unknownMode(name: string): string {
  return `unknown mode "${name}" (expected ${MODES.map((m) => `"${m}"`).join(" or ")})`;
}

/**
 * Drop every mode region that isn't `mode`, keep the ones that are, and remove
 * the markers themselves.
 *
 * Markers work at two scales, same syntax:
 *
 *   BLOCK — the marker is alone on its line, and the region spans lines:
 *       <!-- mode:github -->
 *       Ask the issues API…
 *       <!-- /mode -->
 *
 *   INLINE — the marker has content beside it, and the region must open and
 *   close on that same line:
 *       git commit -m "…" <!-- mode:github -->&& git push<!-- /mode -->
 *
 * Inline exists so a two-word difference doesn't force two copies of a whole
 * paragraph. That duplication is the failure mode this whole design removes, and
 * block-only markers reintroduce it at small scale.
 *
 * Throws on malformed markup (unknown mode name, nesting, an unclosed region, a
 * stray close, or an inline region that swallows its entire line). A composed
 * body is what the agent is steered by, so a markup mistake must fail the run at
 * startup rather than silently ship a skill with the wrong half of a procedure
 * in it — or with both halves.
 */
export function stripModeBlocks(source: string, mode: AgentMode, sourceName = "SKILL.md"): string {
  const lines = source.split("\n");
  const out: string[] = [];
  let openMode: AgentMode | undefined;
  let openLine = 0;

  for (const [index, line] of lines.entries()) {
    const lineNo = index + 1;
    const trimmed = line.trim();

    const open = BLOCK_OPEN.exec(trimmed);
    if (open) {
      const name = open[1] ?? "";
      if (!isAgentMode(name)) {
        throw new Error(`${sourceName}:${lineNo}: ${unknownMode(name)}`);
      }
      if (openMode !== undefined) {
        throw new Error(
          `${sourceName}:${lineNo}: mode:${name} opened inside the mode:${openMode} block from line ${openLine} — mode blocks cannot nest`,
        );
      }
      openMode = name;
      openLine = lineNo;
      continue;
    }

    if (BLOCK_CLOSE.test(trimmed)) {
      if (openMode === undefined) {
        throw new Error(`${sourceName}:${lineNo}: <!-- /mode --> with no open mode block`);
      }
      openMode = undefined;
      continue;
    }

    if (openMode !== undefined && openMode !== mode) continue;
    const resolved = resolveInlineMarkers(line, mode, sourceName, lineNo);
    if (resolved !== undefined) out.push(resolved);
  }

  if (openMode !== undefined) {
    throw new Error(`${sourceName}: mode:${openMode} block opened on line ${openLine} is never closed`);
  }

  // Stripping a block that sat between two blank-line-separated paragraphs can
  // leave a run of blank lines behind. Collapse to at most one, so the composed
  // body reads like hand-written markdown regardless of how the source was laid
  // out. Prose spacing only — never inside a fenced code block.
  return collapseBlankRuns(out).join("\n");
}

/**
 * Resolve inline (same-line) mode regions within one retained line. Returns
 * `undefined` when the whole line was conditional and this mode drops it.
 *
 * An inline region must close on the line it opened on: a marker with content
 * beside it is by definition not a block marker, so an unclosed one is a typo,
 * not a multi-line region.
 */
function resolveInlineMarkers(
  line: string,
  mode: AgentMode,
  sourceName: string,
  lineNo: number,
): string | undefined {
  if (!line.includes("<!--")) return line;

  const re = new RegExp(ANY_MARKER.source, "g");
  let out = "";
  let cursor = 0;
  let open: AgentMode | undefined;
  let match: RegExpExecArray | null;

  while ((match = re.exec(line)) !== null) {
    if (open === undefined || open === mode) out += line.slice(cursor, match.index);
    cursor = match.index + match[0].length;

    const name = match[1];
    if (name === undefined) {
      if (open === undefined) {
        throw new Error(`${sourceName}:${lineNo}: inline <!-- /mode --> with no open mode region`);
      }
      open = undefined;
      continue;
    }
    if (!isAgentMode(name)) {
      throw new Error(`${sourceName}:${lineNo}: ${unknownMode(name)}`);
    }
    if (open !== undefined) {
      throw new Error(`${sourceName}:${lineNo}: inline mode:${name} opened inside mode:${open} — mode regions cannot nest`);
    }
    open = name;
  }

  if (open !== undefined) {
    throw new Error(
      `${sourceName}:${lineNo}: inline mode:${open} is not closed on its own line — put the marker alone on its own line to span several lines`,
    );
  }

  out = (out + line.slice(cursor)).trimEnd();

  // The whole line was one region and this isn't its mode — drop it, the same
  // way the block form drops its lines. Keeping a whitespace-only line would
  // break up a code fence or a list for no reason.
  if (out.trim() === "" && line.trim() !== "") return undefined;

  // What's left is a list bullet or an ordinal with nothing after it: the item
  // now reads as a truncated instruction rather than an absent one. Wrap the
  // whole item in the block form instead.
  if (/^([-*+]|\d+[.)])$/.test(out.trim())) {
    throw new Error(
      `${sourceName}:${lineNo}: stripping inline markers left a dangling list marker (${out.trim()}) — use the block form for a whole-item conditional`,
    );
  }
  return out;
}

function collapseBlankRuns(lines: string[]): string[] {
  const out: string[] = [];
  let inFence = false;
  let blanks = 0;
  for (const line of lines) {
    if (/^\s*(```|~~~)/.test(line)) inFence = !inFence;
    if (!inFence && line.trim() === "") {
      blanks += 1;
      if (blanks > 1) continue;
    } else {
      blanks = 0;
    }
    out.push(line);
  }
  return out;
}

/** The base plugin's workflow skill — the one file that carries mode blocks. */
const BASE_SKILL_RELPATH = path.join("skills", "aep", "SKILL.md");

export interface ComposeBasePluginArgs {
  /** The authored plugin dir (read-only; never mutated). */
  sourceDir: string;
  /** Where to write the composed plugin. Replaced if it already exists. */
  destDir: string;
  mode: AgentMode;
}

/**
 * Copy the authored base plugin to `destDir` with its workflow skill composed
 * for `mode`, and return `destDir` (the path to hand the SDK as a plugin).
 *
 * The whole plugin is copied, not just the composed skill: a Claude Code plugin
 * is loaded as one directory, and the sibling skills (`aep-validation`,
 * `playwright-cli`) have to come along. It is ~200K, and copying per run is
 * also what keeps the dev flow's bind-mounted plugin dir live-editable — each
 * run composes from whatever is on disk at that moment.
 *
 * Synchronous by design: this runs once at session startup, before the first
 * token, and keeps `runClaudeQuery` a plain synchronous call.
 */
export function composeBasePlugin(args: ComposeBasePluginArgs): string {
  const { sourceDir, destDir, mode } = args;
  const sourceSkill = path.join(sourceDir, BASE_SKILL_RELPATH);
  if (!fs.existsSync(sourceSkill)) {
    throw new Error(`base plugin has no ${BASE_SKILL_RELPATH}: ${sourceDir}`);
  }

  // Compose BEFORE touching the destination: a markup error must not leave a
  // half-written plugin dir behind for a retry to load.
  const composed = stripModeBlocks(
    fs.readFileSync(sourceSkill, "utf8"),
    mode,
    path.join(path.basename(sourceDir), BASE_SKILL_RELPATH),
  );

  fs.rmSync(destDir, { recursive: true, force: true });
  fs.mkdirSync(path.dirname(destDir), { recursive: true });
  fs.cpSync(sourceDir, destDir, { recursive: true });
  fs.writeFileSync(path.join(destDir, BASE_SKILL_RELPATH), composed, { mode: 0o644 });

  return destDir;
}
