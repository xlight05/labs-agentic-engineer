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
 * The eval's skill resolver (ADR-0002). The SERVICE never reads skills from disk;
 * the caller resolves them and pushes them in the turn payload. In evals THIS is
 * that caller: it reads repo-root `skills/<id>/SKILL.md`, parses the frontmatter
 * for `name`+`description`, takes the body as `content`, and returns the whole
 * library as `Skill[]` to attach to every turn. Only `evals/` touches the fs.
 */

import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { parse as parseYaml } from "yaml";
import type { Skill } from "@aep/contracts";
// Reuse the bundle's single frontmatter grammar + LF canonicalizer so the
// SKILL.md fence parsing can't drift from the spec-file fence parsing.
import { FRONTMATTER_RE, lf } from "../src/agents/main/bundle.js";

/** Split a `SKILL.md` into its YAML frontmatter block and markdown body. */
function splitFrontmatter(raw: string): { frontmatter: string; body: string } {
  const text = lf(raw);
  const m = FRONTMATTER_RE.exec(text);
  if (!m) return { frontmatter: "", body: text };
  return { frontmatter: m[1] ?? "", body: text.slice(m[0].length) };
}

/** Parse one `SKILL.md` into a `Skill`; `name` falls back to the directory id. */
export function parseSkill(dirId: string, raw: string): Skill {
  const { frontmatter, body } = splitFrontmatter(raw);
  let fm: Record<string, unknown> = {};
  if (frontmatter.trim() !== "") {
    const parsed = parseYaml(frontmatter) as unknown;
    if (parsed && typeof parsed === "object") fm = parsed as Record<string, unknown>;
  }
  const name = typeof fm.name === "string" && fm.name.trim() !== "" ? fm.name : dirId;
  const description = typeof fm.description === "string" ? fm.description : "";
  return { name, description, content: body.trim() };
}

/**
 * Load the whole skill library from `skillsDir` (repo-root `skills/`): every
 * immediate subdirectory holding a `SKILL.md`, sorted by id. Returns `[]` when
 * the directory is absent, so a checkout without `skills/` runs skill-free.
 */
export function loadRepoSkills(skillsDir: string): Skill[] {
  if (!existsSync(skillsDir)) return [];
  return readdirSync(skillsDir, { withFileTypes: true })
    .filter((d) => d.isDirectory())
    .sort((a, b) => a.name.localeCompare(b.name))
    .flatMap((d) => {
      try {
        return [parseSkill(d.name, readFileSync(join(skillsDir, d.name, "SKILL.md"), "utf8"))];
      } catch {
        return []; // a directory without a readable SKILL.md is simply not a skill
      }
    });
}
