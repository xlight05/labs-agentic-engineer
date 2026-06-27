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
 * The single .env loader. `config.ts` composes this BEFORE reading any
 * `process.env`, so a value set only in `.env` (not the ambient env) is honored
 * — not silently dropped. The walk is path-independent (it finds the nearest
 * `.env` going up to the monorepo root), so it survives moving the package.
 */

import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

let loaded = false;

/**
 * Load the nearest `.env` (walking up from this file to the filesystem root)
 * into `process.env`, once. No-op if already loaded or no `.env` is found —
 * ambient env then wins, which is what we want.
 */
export function loadDotenv(): void {
  if (loaded) return;
  loaded = true;
  let dir = fileURLToPath(new URL(".", import.meta.url));
  for (let i = 0; i < 12; i++) {
    const candidate = join(dir, ".env");
    if (existsSync(candidate)) {
      process.loadEnvFile(candidate);
      break;
    }
    const parent = dirname(dir);
    if (parent === dir) break; // reached filesystem root
    dir = parent;
  }
}

/**
 * Parse a positive-integer env var, falling back when it is unset, empty, or
 * non-numeric. `Number("")` is 0 and `Number("abc")`/`parseInt("twenty")` are
 * NaN — both must NOT silently become a degenerate value (a 0 port, a NaN step
 * budget that disables the cap).
 */
export function intEnv(value: string | undefined, fallback: number): number {
  const n = Number(value);
  return Number.isInteger(n) && n > 0 ? n : fallback;
}

/**
 * Return `ANTHROPIC_API_KEY`. Prefer the ambient env; otherwise load the
 * nearest `.env`. Throws when unset (the composition root surfaces this).
 */
export function loadAnthropicKey(): string {
  if (!process.env.ANTHROPIC_API_KEY) loadDotenv();
  const key = process.env.ANTHROPIC_API_KEY;
  if (!key) {
    throw new Error(
      "ANTHROPIC_API_KEY is not set. Export it, or add it to a .env at the monorepo root (see .env.example).",
    );
  }
  return key;
}
