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
 * DiskMirror — a sandboxed real-filesystem writer, used ONLY by the eval's
 * gitignored preview dump (`preview.ts`). The runtime writes ZERO files; this
 * lives under `evals/` for that reason. Shrunk from the old disk-streaming mode
 * to just `write` + a recursive `clear` (so a removed file shows as absence).
 * Every path is sandboxed to `root` — the model supplies the keys, so a `..`
 * escape must never reach the real FS.
 */

import { mkdirSync, writeFileSync, rmSync, existsSync } from "node:fs";
import { dirname, resolve, sep } from "node:path";

export class DiskMirror {
  readonly root: string;
  private readonly rootAbs: string;

  constructor(root: string) {
    this.root = root;
    this.rootAbs = resolve(root);
  }

  /** Resolve a bundle key to an absolute path, refusing anything outside root. */
  private resolveKey(key: string): string {
    const abs = resolve(this.rootAbs, key);
    if (abs !== this.rootAbs && !abs.startsWith(this.rootAbs + sep)) {
      throw new Error(`path "${key}" escapes the root directory`);
    }
    return abs;
  }

  write(key: string, content: string): void {
    const abs = this.resolveKey(key);
    mkdirSync(dirname(abs), { recursive: true });
    writeFileSync(abs, content, "utf8");
  }

  /** Recursively remove everything under root (so a removed file shows as absence). */
  clear(): void {
    if (existsSync(this.rootAbs)) rmSync(this.rootAbs, { recursive: true, force: true });
  }
}
