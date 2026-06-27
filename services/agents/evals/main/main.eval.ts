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
 * The main agent's eval suite definition: its name, fixtures dir, and default
 * seed corpus. Consumed by `cli.ts` (the runner). New agents add a sibling
 * `<agent>/<agent>.eval.ts` exporting their own `EvalSuite`.
 */

import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SEED_FILES } from "../../src/agents/main/prompt.js";
import type { EvalSuite } from "../harness.js";

const here = dirname(fileURLToPath(import.meta.url));

export const mainSuite: EvalSuite = {
  agent: "main",
  fixturesDir: join(here, "fixtures"),
  defaultSeed: SEED_FILES,
  // Repo-root `skills/` (services/agents/evals/main → up four). The harness reads
  // the whole library and pushes it on every turn (ADR-0002).
  skillsDir: join(here, "..", "..", "..", "..", "skills"),
};

export default mainSuite;
