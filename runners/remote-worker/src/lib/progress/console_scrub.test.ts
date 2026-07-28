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

import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { installConsoleScrubber, type ConsoleLike } from "./console_scrub.js";
import { scrubber } from "./scrubber.js";

// A token whose SHAPE the scrubber does not recognise, so these tests prove the
// literal-enrollment path rather than the regex path.
const OPAQUE_TOKEN = "aQ7fL2mZ9xR4tY6uP1sD3gH5jK8nB0vC";

function fakeConsole(): { console: ConsoleLike; lines: string[] } {
  const lines: string[] = [];
  const sink = (...args: unknown[]): void => {
    lines.push(args.map(String).join(" "));
  };
  return {
    console: { log: sink, info: sink, warn: sink, error: sink, debug: sink },
    lines,
  };
}

beforeEach(() => {
  scrubber.reset();
});

test("installConsoleScrubber: redacts an enrolled literal on every method", () => {
  const { console: c, lines } = fakeConsole();
  installConsoleScrubber(c);
  scrubber.addLiteral(OPAQUE_TOKEN);

  c.log(`clone used ${OPAQUE_TOKEN}`);
  c.warn(`warn ${OPAQUE_TOKEN}`);
  c.error(`error ${OPAQUE_TOKEN}`);
  c.info(`info ${OPAQUE_TOKEN}`);
  c.debug(`debug ${OPAQUE_TOKEN}`);

  assert.equal(lines.length, 5);
  for (const line of lines) {
    assert.ok(!line.includes(OPAQUE_TOKEN), `leaked: ${line}`);
    assert.match(line, /\[REDACTED\]/);
  }
});

test("installConsoleScrubber: literals enrolled after install still redact", () => {
  // The git token is minted mid-run, well after the wrapper is installed.
  const { console: c, lines } = fakeConsole();
  installConsoleScrubber(c);

  c.log(`before ${OPAQUE_TOKEN}`);
  scrubber.addLiteral(OPAQUE_TOKEN);
  c.log(`after ${OPAQUE_TOKEN}`);

  assert.ok(lines[0].includes(OPAQUE_TOKEN), "pre-enrollment line is not covered by a literal");
  assert.ok(!lines[1].includes(OPAQUE_TOKEN), `leaked after enrollment: ${lines[1]}`);
});

test("installConsoleScrubber: scrubs a token inside an Error's stack", () => {
  const { console: c, lines } = fakeConsole();
  installConsoleScrubber(c);
  scrubber.addLiteral(OPAQUE_TOKEN);

  // The shape console.error("...:", err) takes at the runner's entry points.
  c.error("[oneshot] unhandled error:", new Error(`Command failed: git clone ${OPAQUE_TOKEN}`));

  assert.equal(lines.length, 1);
  assert.ok(!lines[0].includes(OPAQUE_TOKEN), `leaked: ${lines[0]}`);
  assert.match(lines[0], /\[oneshot\] unhandled error:/);
  // The stack survives — redaction must not cost us the diagnostic.
  assert.match(lines[0], /Error: Command failed/);
});

test("installConsoleScrubber: preserves printf-style formatting", () => {
  const { console: c, lines } = fakeConsole();
  installConsoleScrubber(c);
  c.log("materialised %d skill(s) for %s", 3, "api");
  assert.equal(lines[0], "materialised 3 skill(s) for api");
});

test("installConsoleScrubber: leaves ordinary lines untouched", () => {
  const { console: c, lines } = fakeConsole();
  installConsoleScrubber(c);
  scrubber.addLiteral(OPAQUE_TOKEN);
  c.log("[oneshot] no per-task skills to materialise");
  assert.equal(lines[0], "[oneshot] no per-task skills to materialise");
});

test("installConsoleScrubber: is idempotent — no double wrapping", () => {
  const { console: c, lines } = fakeConsole();
  installConsoleScrubber(c);
  installConsoleScrubber(c);
  scrubber.addLiteral(OPAQUE_TOKEN);

  c.log(`x ${OPAQUE_TOKEN} y`);
  assert.equal(lines.length, 1);
  assert.equal(lines[0], "x [REDACTED] y");
});
