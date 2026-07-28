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
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import {
  readSkillsApplied,
  readProjectSkillsApplied,
  resolveSkillsFromClone,
  resolveKind,
  resolveTaskSkills,
} from "./skills_resolver.js";

// tmpTree materialises a { relPath: content } map under a fresh temp dir and
// returns the root. Directories are created as needed.
async function tmpTree(files: Record<string, string>): Promise<string> {
  const root = await fs.promises.mkdtemp(path.join(os.tmpdir(), "aep-skills-test-"));
  for (const [rel, content] of Object.entries(files)) {
    const full = path.join(root, rel);
    await fs.promises.mkdir(path.dirname(full), { recursive: true });
    await fs.promises.writeFile(full, content);
  }
  return root;
}

const skillMD = (name: string, kind?: string): string => {
  const meta = kind ? `metadata:\n  aep:\n    kind: ${kind}\n` : "";
  return `---\nname: ${name}\ndescription: does ${name}.\n${meta}---\n\n# ${name}\n`;
};

// ---- readSkillsApplied ------------------------------------------------------

test("readSkillsApplied: parses the component design.json skillsApplied array", async () => {
  const ws = await tmpTree({
    "specs/design/components/api/design.json": JSON.stringify({
      skillsApplied: ["go", "react-webapp"],
    }),
  });
  assert.deepEqual(await readSkillsApplied(ws, "api"), ["go", "react-webapp"]);
});

test("readSkillsApplied: absent design.json → []", async () => {
  const ws = await tmpTree({ "README.md": "no design here" });
  assert.deepEqual(await readSkillsApplied(ws, "api"), []);
});

test("readSkillsApplied: design.json with no skillsApplied → []", async () => {
  const ws = await tmpTree({
    "specs/design/components/api/design.json": JSON.stringify({ title: "x" }),
  });
  assert.deepEqual(await readSkillsApplied(ws, "api"), []);
});

test("readSkillsApplied: malformed design.json → []", async () => {
  const ws = await tmpTree({
    "specs/design/components/api/design.json": "{ not valid json",
  });
  assert.deepEqual(await readSkillsApplied(ws, "api"), []);
});

test("readSkillsApplied: non-string entries are filtered out", async () => {
  const ws = await tmpTree({
    "specs/design/components/api/design.json": JSON.stringify({
      skillsApplied: ["go", 42, null],
    }),
  });
  assert.deepEqual(await readSkillsApplied(ws, "api"), ["go"]);
});

test("readSkillsApplied: reads only the named component, not others", async () => {
  const ws = await tmpTree({
    "specs/design/components/api/design.json": JSON.stringify({ skillsApplied: ["go"] }),
    "specs/design/components/webapp/design.json": JSON.stringify({
      skillsApplied: ["react-webapp"],
    }),
  });
  assert.deepEqual(await readSkillsApplied(ws, "api"), ["go"]);
  assert.deepEqual(await readSkillsApplied(ws, "webapp"), ["react-webapp"]);
});

// ---- readProjectSkillsApplied (milestone scope) -----------------------------

test("readProjectSkillsApplied: unions every component, de-duplicated, in component order", async () => {
  const ws = await tmpTree({
    "specs/design/components/webapp/design.json": JSON.stringify({
      skillsApplied: ["react-webapp", "go"],
    }),
    "specs/design/components/api/design.json": JSON.stringify({ skillsApplied: ["go"] }),
    "specs/design/components/worker/design.json": JSON.stringify({ skillsApplied: ["go", "temporal"] }),
  });
  // Sorted component order (api, webapp, worker); "go" appears once, first-seen.
  assert.deepEqual(await readProjectSkillsApplied(ws), ["go", "react-webapp", "temporal"]);
});

test("readProjectSkillsApplied: does NOT read a component named after the milestone sentinel", async () => {
  // The regression: a milestone Job carries AEP_COMPONENT_NAME=aep-milestone,
  // which never names a real component — the union must still find the skills.
  const ws = await tmpTree({
    "specs/design/components/workout-tracker-webapp/design.json": JSON.stringify({
      skillsApplied: ["react-webapp"],
    }),
  });
  assert.deepEqual(await readSkillsApplied(ws, "aep-milestone"), []);
  assert.deepEqual(await readProjectSkillsApplied(ws), ["react-webapp"]);
});

test("readProjectSkillsApplied: absent components dir → [] with a warning", async () => {
  const ws = await tmpTree({ "README.md": "no specs here" });
  const lines: string[] = [];
  assert.deepEqual(await readProjectSkillsApplied(ws, (l) => lines.push(l)), []);
  assert.ok(
    lines.some((l) => l.includes("specs/design/components/ not found")),
    `expected a not-found warning, got ${JSON.stringify(lines)}`,
  );
});

test("readProjectSkillsApplied: components without / with malformed design.json contribute nothing", async () => {
  const ws = await tmpTree({
    "specs/design/components/api/design.json": JSON.stringify({ skillsApplied: ["go"] }),
    "specs/design/components/broken/design.json": "{ not json",
    "specs/design/components/undesigned/README.md": "no design.json yet",
  });
  assert.deepEqual(await readProjectSkillsApplied(ws), ["go"]);
});

test("readProjectSkillsApplied: skips dot-dirs and stray files", async () => {
  const ws = await tmpTree({
    "specs/design/components/api/design.json": JSON.stringify({ skillsApplied: ["go"] }),
    "specs/design/components/.cache/design.json": JSON.stringify({ skillsApplied: ["leaked"] }),
    "specs/design/components/notes.md": "stray file",
  });
  assert.deepEqual(await readProjectSkillsApplied(ws), ["go"]);
});

// ---- resolveKind ------------------------------------------------------------

test("resolveKind: known kinds pass through; absent/unknown → org", () => {
  assert.equal(resolveKind(skillMD("s", "platform")), "platform");
  assert.equal(resolveKind(skillMD("s", "org")), "org");
  assert.equal(resolveKind(skillMD("s", "custom")), "custom");
  assert.equal(resolveKind(skillMD("s", "imported")), "imported");
  assert.equal(resolveKind(skillMD("s")), "org"); // unmarked
  assert.equal(resolveKind(skillMD("s", "wat")), "org"); // unknown
  assert.equal(resolveKind(skillMD("s", "  platform  ")), "platform"); // trimmed
  assert.equal(resolveKind("no frontmatter here"), "org");
});

// ---- resolveSkillsFromClone -------------------------------------------------

test("resolveSkillsFromClone: builds materializedName from kind + reads references", async () => {
  const clone = await tmpTree({
    "skills/go/SKILL.md": skillMD("go", "org"),
    "skills/go/references/style.md": "# go style",
    "skills/payments/SKILL.md": skillMD("payments", "custom"),
  });

  const out = await resolveSkillsFromClone(clone, ["go", "payments"]);
  assert.equal(out.length, 2);

  const go = out.find((s) => s.materializedName === "org-go");
  assert.ok(go, "expected org-go");
  assert.equal(go!.kind, "org");
  assert.equal(go!.references["references/style.md"]?.toString("utf-8"), "# go style");

  const pay = out.find((s) => s.materializedName === "custom-payments");
  assert.ok(pay, "expected custom-payments");
  assert.equal(pay!.kind, "custom");
  assert.deepEqual(pay!.references, {}); // no aux files
});

test("resolveSkillsFromClone: recursively reads the full skill structure as Buffers, skipping SKILL.md and dot-entries", async () => {
  const clone = await tmpTree({
    "skills/full/SKILL.md": skillMD("full", "org"),
    "skills/full/references/a.md": "# ref a",
    "skills/full/scripts/run.mjs": "console.log('hi');\n",
    "skills/full/.gitkeep": "should be skipped",
  });
  // Write a nested dotdir file and a genuine binary asset the string-based
  // tmpTree helper can't express.
  await fs.promises.mkdir(path.join(clone, "skills/full/.hidden"), { recursive: true });
  await fs.promises.writeFile(path.join(clone, "skills/full/.hidden/secret.md"), "nope");
  await fs.promises.mkdir(path.join(clone, "skills/full/assets"), { recursive: true });
  const binary = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff]);
  await fs.promises.writeFile(path.join(clone, "skills/full/assets/logo.png"), binary);

  const out = await resolveSkillsFromClone(clone, ["full"]);
  assert.equal(out.length, 1);
  const refs = out[0].references;

  assert.ok(refs["references/a.md"] instanceof Buffer);
  assert.equal(refs["references/a.md"]!.toString("utf-8"), "# ref a");
  assert.ok(refs["scripts/run.mjs"] instanceof Buffer);
  assert.equal(refs["scripts/run.mjs"]!.toString("utf-8"), "console.log('hi');\n");
  assert.ok(refs["assets/logo.png"] instanceof Buffer);
  assert.ok(Buffer.compare(refs["assets/logo.png"]!, binary) === 0);

  // SKILL.md itself and dot-entries must never appear among the aux files.
  assert.equal(refs["SKILL.md"], undefined);
  assert.equal(refs[".gitkeep"], undefined);
  assert.equal(refs[".hidden/secret.md"], undefined);
  assert.deepEqual(Object.keys(refs).sort(), ["assets/logo.png", "references/a.md", "scripts/run.mjs"]);
});

test("resolveSkillsFromClone: unmarked SKILL.md resolves as org kind", async () => {
  const clone = await tmpTree({ "skills/mystery/SKILL.md": skillMD("mystery") });
  const out = await resolveSkillsFromClone(clone, ["mystery"]);
  assert.equal(out.length, 1);
  assert.equal(out[0].kind, "org");
  assert.equal(out[0].materializedName, "org-mystery");
});

test("resolveSkillsFromClone: missing names are dropped (warn-and-skip parity)", async () => {
  const clone = await tmpTree({ "skills/go/SKILL.md": skillMD("go", "org") });
  const out = await resolveSkillsFromClone(clone, ["go", "does-not-exist"]);
  assert.deepEqual(out.map((s) => s.materializedName), ["org-go"]);
});

test("resolveSkillsFromClone: path-traversal names are rejected", async () => {
  const clone = await tmpTree({ "skills/go/SKILL.md": skillMD("go", "org") });
  const out = await resolveSkillsFromClone(clone, ["../secrets", "a/b", "go"]);
  assert.deepEqual(out.map((s) => s.materializedName), ["org-go"]);
});

// ---- resolveTaskSkills (orchestrator, injected clone) -----------------------

test("resolveTaskSkills: end-to-end with an injected clone", async () => {
  const ws = await tmpTree({
    "specs/design/components/api/design.json": JSON.stringify({ skillsApplied: ["go"] }),
  });
  const cloneSrc = await tmpTree({ "skills/go/SKILL.md": skillMD("go", "org") });
  const scratchDir = path.join(os.tmpdir(), "aep-skills-orch", "task-1");

  let clonedRepoURL: string | undefined;
  let cloneCount = 0;
  const out = await resolveTaskSkills({
    workspace: ws,
    scope: { kind: "component", componentName: "api" },
    skillsRepoURL: "https://github.com/acme/org-skills",
    pat: "tok",
    scratchDir,
    clone: async (repoURL, _pat, dest) => {
      clonedRepoURL = repoURL;
      cloneCount += 1;
      // Fake the clone: copy the fixture tree into the scratch dir.
      await fs.promises.cp(cloneSrc, dest, { recursive: true });
    },
  });

  assert.equal(cloneCount, 1, "clone must be invoked once when skills are applied");
  assert.equal(clonedRepoURL, "https://github.com/acme/org-skills");
  assert.equal(out.length, 1);
  assert.equal(out[0].materializedName, "org-go");
});

test("resolveTaskSkills: no applied skills → no clone, empty result", async () => {
  const ws = await tmpTree({
    "specs/design/components/api/design.json": JSON.stringify({ title: "x" }),
  });
  let cloned = false;
  const out = await resolveTaskSkills({
    workspace: ws,
    scope: { kind: "component", componentName: "api" },
    skillsRepoURL: "https://github.com/acme/org-skills",
    pat: "tok",
    scratchDir: path.join(os.tmpdir(), "aep-skills-noop", "task-2"),
    clone: async () => {
      cloned = true;
    },
  });
  assert.equal(cloned, false, "clone must be skipped when no skills are applied");
  assert.deepEqual(out, []);
});

test("resolveTaskSkills: project scope materialises skills from every component", async () => {
  const ws = await tmpTree({
    "specs/design/components/api/design.json": JSON.stringify({ skillsApplied: ["go"] }),
    "specs/design/components/webapp/design.json": JSON.stringify({ skillsApplied: ["react-webapp"] }),
  });
  const cloneSrc = await tmpTree({
    "skills/go/SKILL.md": skillMD("go", "org"),
    "skills/react-webapp/SKILL.md": skillMD("react-webapp", "org"),
  });

  const out = await resolveTaskSkills({
    workspace: ws,
    scope: { kind: "project" },
    skillsRepoURL: "https://github.com/acme/org-skills",
    pat: "tok",
    scratchDir: path.join(os.tmpdir(), "aep-skills-project", "cycle-1"),
    clone: async (_repoURL, _pat, dest) => {
      await fs.promises.cp(cloneSrc, dest, { recursive: true });
    },
  });

  assert.deepEqual(out.map((s) => s.materializedName), ["org-go", "org-react-webapp"]);
});
