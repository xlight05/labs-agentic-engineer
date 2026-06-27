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
 * The fixture superset (§11). One shape covers three scenarios:
 *  - `{ turns: [x] }`                       — single-prompt preview
 *  - `{ messages: [...], turns: [next] }`   — captured-trace continuation (pre-seeds the store)
 *  - `{ turns: [a, b, c] }`                 — full live replay
 */

import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import type { ModelMessage } from "ai";

export interface Expect {
  filesContain?: { path: string; text: string }[];
  filesNotContain?: { path: string; text: string }[];
  filesAbsent?: string[];
  fileMatches?: { path: string; pattern: string }[];
  touched?: string[];
  noToolErrors?: boolean;
  preferEdit?: boolean;
  yamlValid?: string[];
  /** Each named tool must have been called this turn — e.g. `["loadSkill"]` (ADR-0002). */
  toolsUsed?: string[];
}

export interface Turn {
  prompt: string;
  /** Per-turn assertions; falls back to the fixture-level `expect`. */
  expect?: Expect;
  /** Override this turn's snapshot (divergence sim); else reconstructed from prior turns. */
  files?: Record<string, string>;
  /** Prepend the CURRENT-STATE-authoritative note (§10). */
  filesChangedExternally?: boolean;
}

export interface Fixture {
  name: string;
  /** Human note (e.g. "recorded trace — regen via --record"). */
  description?: string;
  difficulty?: string;
  /** Defaults to the suite's default seed (SEED_FILES). */
  seed?: Record<string, string>;
  /** Optional captured trace — pre-seeds the store before the continuation turn. */
  messages?: ModelMessage[];
  turns: Turn[];
  expect: Expect;
}

export function loadFixture(path: string): Fixture {
  return JSON.parse(readFileSync(path, "utf8")) as Fixture;
}

/** Load every `*.json` fixture in `dir`, sorted by name. */
export function loadFixtures(dir: string): Fixture[] {
  return readdirSync(dir)
    .filter((f) => f.endsWith(".json"))
    .sort()
    .map((f) => loadFixture(join(dir, f)));
}
