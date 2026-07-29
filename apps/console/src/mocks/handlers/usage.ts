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

import { http, HttpResponse } from "msw";
import {
  orgUsage,
  usageLoadError,
  type UsageScenario,
} from "../fixtures/usage";

// Scenario knob (devtools):
//   localStorage.setItem('aep:mock:usage', 'default' | 'empty' | 'error')
function scenario(): UsageScenario {
  return (
    (localStorage.getItem("aep:mock:usage") as UsageScenario | null) ??
    "default"
  );
}

// Settings → Usage (#291): the org-wide per-project roll-up.
export const usageHandlers = [
  http.get("*/api/v1/usage/projects", () => {
    const s = scenario();
    if (s === "error") {
      return HttpResponse.json(usageLoadError, { status: 500 });
    }
    return HttpResponse.json(orgUsage[s]);
  }),
];
