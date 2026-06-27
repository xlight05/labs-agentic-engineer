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
import { parse as parseYaml } from "yaml";
import { FileBundle, type OpErr, type OpOk } from "./bundle.js";
import { SEED_FILES } from "./prompt.js";

const OPENAPI = "specs/design/components/hello-api/openapi.yaml";
const DESIGN = "specs/design/design.md";
const REQUIREMENTS = "specs/requirements/requirements.md";

function fresh(): FileBundle {
  return new FileBundle(SEED_FILES);
}
function expectOk(r: OpOk | OpErr): OpOk {
  assert.equal(r.ok, true, `expected ok, got ${JSON.stringify(r)}`);
  return r as OpOk;
}
function expectErr(r: OpOk | OpErr): OpErr {
  assert.equal(r.ok, false, `expected err, got ${JSON.stringify(r)}`);
  return r as OpErr;
}

test("editFile: ambiguous anchor returns NOT_UNIQUE with candidate line numbers", () => {
  const b = fresh();
  const r = expectErr(b.editFile(OPENAPI, "Hello, World!", "Hi there!"));
  assert.equal(r.code, "NOT_UNIQUE");
  assert.ok((r.count ?? 0) >= 2, "expected >= 2 matches in openapi.yaml");
  assert.ok(r.candidates && r.candidates.length >= 2, "expected candidate lines echoed");
  for (const c of r.candidates!) assert.ok(c.line > 0 && typeof c.text === "string");
  // Rejected: bundle is unchanged.
  assert.ok(b.read(OPENAPI)!.includes("Hello, World!"));
});

test("editFile: a unique anchor applies, and re-running is idempotent (ALREADY_APPLIED)", () => {
  const b = fresh();
  const anchor = 'example: "Hello, World!"';
  const ok = expectOk(b.editFile(OPENAPI, anchor, 'example: "Hi there!"'));
  assert.equal(ok.status, "applied");
  assert.ok(b.read(OPENAPI)!.includes('example: "Hi there!"'));

  const again = expectOk(b.editFile(OPENAPI, anchor, 'example: "Hi there!"'));
  assert.equal(again.status, "already-applied", "second identical edit must not wedge the loop");
});

test("editFile: missing snippet returns NOT_FOUND with closest-line hints", () => {
  const b = fresh();
  const r = expectErr(b.editFile(OPENAPI, "operationId: getHelloWorld", "operationId: zzzAbsent"));
  assert.equal(r.code, "NOT_FOUND");
  assert.ok(r.candidates && r.candidates.some((c) => c.text.includes("operationId")));
});

test("editFile: a result that would break YAML is rejected, bundle untouched", () => {
  const b = fresh();
  const before = b.read(OPENAPI)!;
  const r = expectErr(b.editFile(OPENAPI, "openapi: 3.0.3", 'openapi: 3.0.3\nbad: "unterminated'));
  assert.equal(r.code, "INVALID_YAML");
  assert.equal(b.read(OPENAPI), before, "rejected edit must leave the file byte-for-byte unchanged");
});

test("editFile: newString with $-patterns is inserted literally (no replace() interpretation)", () => {
  const b = fresh();
  expectOk(b.editFile(REQUIREMENTS, "a hello world response", "a $& response"));
  assert.ok(b.read(REQUIREMENTS)!.includes("a $& response"));
});

test("addFile: create, NOOP on identical re-add, ALREADY_EXISTS on conflicting re-add", () => {
  const b = fresh();
  const path = "specs/design/components/hello-api/notes.md";
  assert.equal(expectOk(b.addFile(path, "hi\n")).status, "applied");
  assert.equal(expectOk(b.addFile(path, "hi\n")).status, "noop");
  assert.equal(expectErr(b.addFile(path, "different\n")).code, "ALREADY_EXISTS");
});

test("addFile: invalid YAML body is rejected and not created", () => {
  const b = fresh();
  const path = "specs/design/components/hello-api/broken.yaml";
  assert.equal(expectErr(b.addFile(path, "foo: : :\n")).code, "INVALID_YAML");
  assert.equal(b.has(path), false);
});

test("removeFile: protects roots, deletes components, NOOP on absent", () => {
  const b = fresh();
  assert.equal(expectErr(b.removeFile(REQUIREMENTS)).code, "PROTECTED_PATH");
  assert.equal(expectErr(b.removeFile(DESIGN)).code, "PROTECTED_PATH");
  assert.equal(expectOk(b.removeFile(OPENAPI)).status, "applied");
  assert.equal(b.has(OPENAPI), false);
  assert.equal(expectOk(b.removeFile(OPENAPI)).status, "noop");
});

test("setFrontmatterField: sets array + scalar, renders valid YAML, preserves body", () => {
  const b = fresh();
  expectOk(b.setFrontmatterField(DESIGN, "skillsApplied", ["go", "docker"]));
  expectOk(b.setFrontmatterField(DESIGN, "language", "TypeScript"));
  // OpOk no longer carries newContent (§5); read the post-op content directly.
  const content = b.read(DESIGN)!;
  const fm = parseYaml(content.split("\n---")[0]!.replace(/^---\n/, "")) as Record<string, unknown>;
  assert.deepEqual(fm.skillsApplied, ["go", "docker"]);
  assert.equal(fm.language, "TypeScript");
  assert.ok(content.includes("A simple public API service"), "markdown body preserved");
});

test("setFrontmatterField: file without frontmatter returns NO_FRONTMATTER", () => {
  const b = fresh();
  assert.equal(expectErr(b.setFrontmatterField(OPENAPI, "x", "y")).code, "NO_FRONTMATTER");
});
