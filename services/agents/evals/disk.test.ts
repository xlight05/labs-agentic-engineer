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
import { mkdtempSync, rmSync, existsSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { DiskMirror } from "./disk.js";

function tmp(): string {
  return mkdtempSync(join(tmpdir(), "aep-disk-"));
}

test("write creates parent directories and the file", () => {
  const root = tmp();
  try {
    const m = new DiskMirror(root);
    m.write("specs/design/components/hello-api/openapi.yaml", "openapi: 3.0.3\n");
    const p = join(root, "specs/design/components/hello-api/openapi.yaml");
    assert.ok(existsSync(p));
    assert.equal(readFileSync(p, "utf8"), "openapi: 3.0.3\n");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("clear removes everything under root (so a removed file shows as absence)", () => {
  const root = tmp();
  try {
    const m = new DiskMirror(root);
    m.write("a.md", "x");
    m.write("nested/b.md", "y");
    assert.ok(existsSync(join(root, "a.md")));
    m.clear();
    assert.equal(existsSync(root), false);
    // a fresh write after clear re-creates the tree
    m.write("c.md", "z");
    assert.ok(existsSync(join(root, "c.md")));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("rejects paths that escape the root directory", () => {
  const root = tmp();
  try {
    const m = new DiskMirror(root);
    assert.throws(() => m.write("../escape.md", "nope"), /escapes the root/);
    assert.throws(() => m.write("../../escape.md", "nope"), /escapes the root/);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
