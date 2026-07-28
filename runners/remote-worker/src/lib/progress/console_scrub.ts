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

// Routes the runner's console output through the scrubber.
//
// `console.*` is not a private debug channel here: the BFF tails the agent
// pod's stdout/stderr and forwards every line that isn't a progress NDJSON
// envelope into the console build log as a `log` event (see
// delivery/codingagent/agent_progress.go). So console is as user-facing as
// emit() and needs the same redaction.
//
// This wraps the console methods once at process entry rather than asking each
// call site to remember, which also covers output we don't author — the Agent
// SDK, git's own stderr as relayed by child_process errors, and any dependency
// that logs. Scrubbing is applied per call, so literals enrolled later (the
// git token, minted mid-run) still redact earlier-wrapped methods.

import { format } from "node:util";
import { scrubber } from "./scrubber.js";

type ConsoleMethod = "log" | "info" | "warn" | "error" | "debug";

const METHODS: readonly ConsoleMethod[] = ["log", "info", "warn", "error", "debug"];

export type ConsoleLike = Pick<Console, ConsoleMethod>;

// Wrapping the same console twice would scrub twice — harmless but pointless,
// and it would stack a wrapper per call in tests.
const wrapped = new WeakSet<object>();

export function installConsoleScrubber(target: ConsoleLike = console): void {
  if (wrapped.has(target)) return;
  wrapped.add(target);
  for (const method of METHODS) {
    const original = target[method].bind(target);
    // util.format reproduces console's own rendering, including printf-style
    // specifiers and Error stacks, so nothing is lost by collapsing the args
    // to one string before scrubbing.
    target[method] = (...args: unknown[]): void => {
      original(scrubber.scrub(format(...args)));
    };
  }
}
