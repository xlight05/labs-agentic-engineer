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

// Per-task skills resolution from a local `org-skills` clone.
//
// Replaces the retired S2S skills-pull (GET /internal/v1/executions/{id}/skills):
// the runner already clones the project repo and holds an org-wide GitHub PAT,
// so it clones the org's `org-skills` repo too and resolves the design's applied
// skills locally — no second BFF round-trip, one git-based delivery mechanism.
//
// Flow (per task):
//   1. Read skillsApplied[] from the PROJECT clone, at the scope the run works
//      at: one component's design.json, or — for a milestone cycle, which spans
//      the whole milestone and may touch any component — the union across every
//      specs/design/components/*/design.json.
//   2. git clone --depth 1 the org-skills repo into a scratch dir OUTSIDE the
//      work tree (so its nested .git never enters the agent's git status).
//   3. For each bare name resolve skills/<name>/SKILL.md (+ references/*.md) and
//      its kind (frontmatter metadata.aep.kind; absent → "org"). Missing names
//      are dropped (parity with the old server-side ResolveMany warn-and-skip).
//
// The resulting SkillResolution[] feeds materializeSkills unchanged.
//
// Scope is the caller's to state, never inferred from AEP_COMPONENT_NAME: a
// milestone Job carries a sentinel there, so reading a design file by that name
// silently resolves nothing. See SkillsScope.

import fs from "node:fs";
import path from "node:path";
import { parse as parseYaml } from "yaml";
import { cloneWithToken } from "./git_clone.js";
import type { SkillKind, SkillResolution } from "./skills_materializer.js";

const SKILLS_ROOT = "skills";
const KNOWN_KINDS: readonly SkillKind[] = ["platform", "org", "custom", "imported"];

// Leading YAML frontmatter fence (LF-normalized), same grammar the BFF and
// @aep/design-projection use.
const FRONTMATTER_RE = /^---\n([\s\S]*?)\n---/;

/**
 * Which design files contribute `skillsApplied` to this run.
 *
 * `component` is the single-component read: one named component is being built,
 * so only its design.json applies. `project` is the union across every
 * component — the milestone loop works a whole milestone on one branch and may
 * touch any component in it, so there is no single design.json to read.
 *
 * A milestone Job's AEP_COMPONENT_NAME is the `aep-milestone` sentinel (it only
 * has to be a valid k8s label value), so it must never be used to select a
 * design file; a run at milestone scope passes `project` instead.
 */
export type SkillsScope =
  | { kind: "component"; componentName: string }
  | { kind: "project" };

export interface ResolveTaskSkillsArgs {
  /** The project work tree (holds specs/design/components/<name>/design.json). */
  workspace: string;
  /** Which component design files contribute skillsApplied. */
  scope: SkillsScope;
  /** AEP_SKILLS_REPO_URL — the org's `org-skills` clone URL. */
  skillsRepoURL: string;
  /** Org-wide GitHub PAT (x-access-token) for the clone. */
  pat: string;
  /** Scratch dir to clone org-skills into — MUST be outside `workspace`. */
  scratchDir: string;
  /** Per-line log sink; defaults to console.log. */
  log?: (line: string) => void;
  /** Injected for tests; defaults to the real `git clone --depth 1`. */
  clone?: (repoURL: string, pat: string, destDir: string) => Promise<void>;
}

/**
 * Resolve the run's applied skills from a fresh org-skills clone. Returns an
 * empty list (NOT an error) when the design applies no skills or the design
 * files are absent. Throws only on a clone failure — the caller degrades to the
 * base plugin.
 */
export async function resolveTaskSkills(args: ResolveTaskSkillsArgs): Promise<SkillResolution[]> {
  const log = args.log ?? ((l: string) => console.log(l));

  const names =
    args.scope.kind === "project"
      ? await readProjectSkillsApplied(args.workspace, log)
      : await readSkillsApplied(args.workspace, args.scope.componentName, log);
  if (names.length === 0) {
    log("[skills-resolve] design applies no skills — nothing to materialise");
    return [];
  }

  const clone = args.clone ?? cloneSkillsRepo;
  await clone(args.skillsRepoURL, args.pat, args.scratchDir);

  return resolveSkillsFromClone(args.scratchDir, names, log);
}

// The component's authored design file; its `skillsApplied` key lists the
// skills THIS component's build needs (per-component — the project design.md
// no longer carries skills).
const componentDesignRel = (component: string) =>
  `specs/design/components/${component}/design.json`;

// Reads the building component's design.json and returns its `skillsApplied`
// (bare skill names). Absent file / absent field → []. Malformed JSON → [].
export async function readSkillsApplied(
  workspace: string,
  componentName: string,
  log: (line: string) => void = () => {},
): Promise<string[]> {
  const rel = componentDesignRel(componentName);
  let raw: string;
  try {
    raw = await fs.promises.readFile(path.join(workspace, rel), "utf-8");
  } catch {
    log(`[skills-resolve] ⚠️  ${rel} not found — proceeding with no applied skills`);
    return [];
  }
  try {
    const parsed = JSON.parse(raw) as { skillsApplied?: unknown } | null;
    const applied = parsed?.skillsApplied;
    if (Array.isArray(applied)) {
      return applied.filter((s): s is string => typeof s === "string");
    }
  } catch {
    /* malformed design.json → no skills */
  }
  return [];
}

// The directory every component's authored design file lives under.
const COMPONENTS_DIR = "specs/design/components";

// Reads the union of `skillsApplied` across every component's design.json —
// the milestone-scope read. Components are visited in sorted order and names
// de-duplicated first-seen, so the result is deterministic for a given tree.
// An absent components directory → [] (a project with no designed components
// yet); an individual component's absent/malformed design.json contributes
// nothing, exactly as at component scope.
export async function readProjectSkillsApplied(
  workspace: string,
  log: (line: string) => void = () => {},
): Promise<string[]> {
  let entries: fs.Dirent[];
  try {
    entries = await fs.promises.readdir(path.join(workspace, COMPONENTS_DIR), {
      withFileTypes: true,
    });
  } catch {
    log(`[skills-resolve] ⚠️  ${COMPONENTS_DIR}/ not found — proceeding with no applied skills`);
    return [];
  }

  const components = entries
    .filter((e) => e.isDirectory() && !e.name.startsWith("."))
    .map((e) => e.name)
    .sort();

  const union: string[] = [];
  const seen = new Set<string>();
  const contributors: string[] = [];
  for (const component of components) {
    // Quiet per-component read: at project scope a component without a
    // design.json is ordinary, not the warning-worthy case it is at component
    // scope, where the named component's file is the one thing being asked for.
    const names = await readSkillsApplied(workspace, component);
    if (names.length > 0) contributors.push(component);
    for (const name of names) {
      if (seen.has(name)) continue;
      seen.add(name);
      union.push(name);
    }
  }

  log(
    `[skills-resolve] milestone scope: ${union.length} skill(s) across ${contributors.length}/${components.length} component(s)` +
      (contributors.length > 0 ? ` (${contributors.join(", ")})` : ""),
  );
  return union;
}

// resolveSkillsFromClone maps each bare name to skills/<name>/SKILL.md (+ any
// references/*.md) in the flat org-skills layout, deriving the kind from the
// SKILL.md frontmatter. Missing/unsafe names are dropped with a warning.
export async function resolveSkillsFromClone(
  cloneDir: string,
  names: string[],
  log: (line: string) => void = () => {},
): Promise<SkillResolution[]> {
  const out: SkillResolution[] = [];
  for (const name of names) {
    if (!isSafeSkillName(name)) {
      log(`[skills-resolve] skipping unsafe skill name ${JSON.stringify(name)}`);
      continue;
    }
    const skillDir = path.join(cloneDir, SKILLS_ROOT, name);
    let skillMd: string;
    try {
      skillMd = await fs.promises.readFile(path.join(skillDir, "SKILL.md"), "utf-8");
    } catch {
      log(`[skills-resolve] applied skill ${JSON.stringify(name)} not found in org-skills — skipping`);
      continue;
    }
    const kind = resolveKind(skillMd);
    const references = await readReferences(skillDir);
    out.push({ materializedName: `${kind}-${name}`, kind, skillMd, references });
  }
  return out;
}

// cloneSkillsRepo shallow-clones the org-skills repo into destDir, authing with
// the org-wide PAT via the askpass shim (file:// origins — tests — pass an empty
// token and clone unauthed). Wipes destDir first so a resumed pod's stale dir
// never blocks `git clone`.
async function cloneSkillsRepo(repoURL: string, pat: string, destDir: string): Promise<void> {
  await fs.promises.rm(destDir, { recursive: true, force: true });
  await fs.promises.mkdir(path.dirname(destDir), { recursive: true });
  await cloneWithToken({
    repoUrl: repoURL,
    destDir,
    token: pat,
    // Sibling of the clone target, inside the caller's scratch dir — never the
    // work tree, and removed with the scratch dir.
    shimDir: path.dirname(destDir),
    depth1: true,
  });
}

// readReferences recursively walks the full skill dir (any depth) and returns
// every aux file — everything except SKILL.md itself and dot-entries (dotfiles
// / dot-dirs, e.g. .git, .DS_Store) — keyed by its path relative to skillDir,
// read as a Buffer so binary assets (images, wasm, …) survive byte-faithfully.
// Keys match the materializer's contract (e.g. "references/style.md",
// "scripts/run.mjs", "assets/logo.png").
async function readReferences(skillDir: string): Promise<Record<string, Buffer>> {
  const refs: Record<string, Buffer> = {};
  await walk(skillDir, "");
  return refs;

  async function walk(dir: string, relPrefix: string): Promise<void> {
    let entries: fs.Dirent[];
    try {
      entries = await fs.promises.readdir(dir, { withFileTypes: true });
    } catch {
      return; // dir vanished / unreadable — nothing to add
    }
    for (const e of entries) {
      if (e.name.startsWith(".")) continue; // skip dotfiles/dot-dirs
      const rel = relPrefix ? `${relPrefix}/${e.name}` : e.name;
      if (relPrefix === "" && e.name === "SKILL.md") continue; // handled separately
      const full = path.join(dir, e.name);
      if (e.isDirectory()) {
        await walk(full, rel);
      } else if (e.isFile()) {
        refs[rel] = await fs.promises.readFile(full);
      }
    }
  }
}

// resolveKind mirrors the BFF's frontmatterKind: trimmed metadata.aep.kind when
// it names a known kind, else "org" (an unmarked SKILL.md is an org skill).
export function resolveKind(skillMd: string): SkillKind {
  const block = frontmatterBlock(skillMd);
  if (!block) return "org";
  try {
    const fm = parseYaml(block) as { metadata?: { aep?: { kind?: unknown } } } | null;
    const raw = fm?.metadata?.aep?.kind;
    const k = typeof raw === "string" ? raw.trim() : "";
    if ((KNOWN_KINDS as readonly string[]).includes(k)) return k as SkillKind;
  } catch {
    /* malformed frontmatter → org */
  }
  return "org";
}

// frontmatterBlock returns the YAML between the leading `---` fences, or null.
function frontmatterBlock(raw: string): string | null {
  const m = FRONTMATTER_RE.exec(raw.replace(/\r\n/g, "\n").replace(/^﻿/, "").trimStart());
  return m && m[1] ? m[1] : null;
}

// isSafeSkillName rejects empties and any path-traversal shapes — a bare name
// maps to skills/<name>/ and must not escape the clone.
function isSafeSkillName(name: string): boolean {
  return (
    name.trim() !== "" &&
    !name.includes("/") &&
    !name.includes("\\") &&
    !name.includes("..")
  );
}
