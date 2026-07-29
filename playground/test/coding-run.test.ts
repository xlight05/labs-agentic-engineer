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

/** Coding-run flow units: undo round trip, gates, timeline rendering. */

import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { renderProgressLine } from "../src/engine/coding-run.js";
import { renderTaskContextFile } from "../src/ports/issue-store.js";
import { takeUndoSnapshot, restoreUndoSnapshot, listUndoSnapshots } from "../src/state/undo.js";
import { codeCommand } from "../src/commands.js";

function tempProject(): string {
  const dir = mkdtempSync(join(tmpdir(), "aep-play-code-"));
  mkdirSync(join(dir, "issues"), { recursive: true });
  writeFileSync(
    join(dir, "issues", "3.md"),
    renderTaskContextFile({
      issueNumber: 3,
      component: "user-service",
      title: "Implement the user service",
      dependsOn: ["auth-service"],
      body: "scope",
    }),
  );
  return dir;
}

test("undo snapshot + restore round-trips edits and removes new files", () => {
  const dir = tempProject();
  try {
    mkdirSync(join(dir, "src"));
    writeFileSync(join(dir, "src", "main.go"), "package main\n");
    const snap = takeUndoSnapshot(dir);
    assert.ok(existsSync(snap));
    assert.equal(listUndoSnapshots(dir)[0], snap);

    // Agent damage: edit a file, add a new one, delete another.
    writeFileSync(join(dir, "src", "main.go"), "package broken\n");
    writeFileSync(join(dir, "src", "junk.go"), "junk\n");
    rmSync(join(dir, "issues", "3.md"));

    const restored = restoreUndoSnapshot(dir);
    assert.equal(restored, snap);
    assert.equal(readFileSync(join(dir, "src", "main.go"), "utf8"), "package main\n");
    assert.ok(!existsSync(join(dir, "src", "junk.go")), "post-snapshot file removed");
    assert.ok(existsSync(join(dir, "issues", "3.md")), "deleted file restored");
    // The state dir itself is never part of the snapshot scope.
    assert.ok(existsSync(join(dir, ".aep-playground", "undo")));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("codeCommand gates: no issues fails; unconfirmed headless run fails before any snapshot", async () => {
  const empty = mkdtempSync(join(tmpdir(), "aep-play-code-empty-"));
  try {
    const noIssues = await codeCommand(empty, { silent: true });
    assert.equal(noIssues.ok, false);
    assert.match(noIssues.detail ?? "", /nothing to run/);
  } finally {
    rmSync(empty, { recursive: true, force: true });
  }

  const dir = tempProject();
  try {
    const unconfirmed = await codeCommand(dir, { silent: true });
    assert.equal(unconfirmed.ok, false);
    assert.match(unconfirmed.detail ?? "", /not confirmed/);
    assert.equal(listUndoSnapshots(dir).length, 0, "no snapshot before consent");
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("renderProgressLine maps the NDJSON vocabulary to timeline lines", () => {
  assert.equal(renderProgressLine({ kind: "phase", phase: "workspace_ready" }), "  ▸ workspace ready");
  assert.equal(renderProgressLine({ kind: "tool_use", tool: "Bash", summary: "go test ./..." }), "  $ go test ./...");
  assert.match(renderProgressLine({ kind: "result", status: "success" }), /result success/);
  assert.match(renderProgressLine({ kind: "result", status: "failure", error: "boom" }), /failure — boom/);
  assert.match(renderProgressLine({ kind: "gh_action", command: "pr create" }), /local mode has no GitHub/);
});
