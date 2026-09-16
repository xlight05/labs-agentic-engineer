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
 * Where each referential finding belongs on the Security page.
 *
 * The findings are computed in the BROWSER, from the same shared rules the
 * build gate runs, rather than fetched from the BFF — so they follow the
 * document as it is edited, which a request/response endpoint could not.
 *
 * `securityReferenceFindings` returns one flat list for the whole document; the
 * page has three places to put a sentence — on a matrix row, on a role card, or
 * above the matrix. The routing is by the finding's `params`, which is why the
 * gate carries them separately from the rendered message: a caller that knows
 * the layout can address the row the rule is about without re-parsing English.
 *
 * The rule, in one line: a finding that names a handle the matrix draws belongs
 * on that row; otherwise one that names a role belongs on that card; otherwise
 * one about the screen set belongs on the Screens block; otherwise it is about
 * the document as a whole.
 *
 * The screen bucket is the one routed by KEY rather than by params, and
 * deliberately: "which section of the page" is not a fact any parameter
 * carries — `component` appears on catalog findings too — while the set of
 * rules that are ABOUT screens is closed and named below. It matters because
 * the rule a reader most needs (a screen the wireframe draws and the document
 * does not gate) is invisible in the list of gated screens by construction:
 * the page can only draw the rows the document HAS.
 */

import type { SecurityReferenceFinding } from "@aep/agent-stream";

/** The findings split by where the page shows them. */
export interface RoutedFindings {
  /** Above the matrix — nothing narrower fits. */
  document: SecurityReferenceFinding[];
  /** On the Screens block: the rules about the screen set itself. */
  screens: SecurityReferenceFinding[];
  /** Keyed by the FULL `<resource>:<action>` handle of the row. */
  byHandle: Map<string, SecurityReferenceFinding[]>;
  /** Keyed by the role name, compared lowercase. */
  byRole: Map<string, SecurityReferenceFinding[]>;
}

/** The handle a finding is about, or undefined when it names none. */
function handleOf(finding: SecurityReferenceFinding): string | undefined {
  return finding.params["handle"];
}

/**
 * The rules that are about the screen set. `screen_without_read` is NOT one of
 * them: it names a role, and what the reader has to change is that role's
 * grants, so it belongs on the role card — which the role check above reaches
 * first anyway.
 */
const SCREEN_KEYS: ReadonlySet<string> = new Set([
  "screen_not_gated",
  "screen_unknown",
  "screen_component_unknown",
  "screen_requires_unknown_handle",
]);

/**
 * Route every finding to its place. `rows` is the set of handles the matrix
 * actually draws — a finding naming a handle that is NOT a row (a grant of an
 * undeclared handle, say) has no row to land on, so it falls through to the
 * role card or the document.
 */
export function routeFindings(
  findings: readonly SecurityReferenceFinding[],
  rows: ReadonlySet<string>,
): RoutedFindings {
  const routed: RoutedFindings = {
    document: [],
    screens: [],
    byHandle: new Map(),
    byRole: new Map(),
  };
  for (const finding of findings) {
    const handle = handleOf(finding);
    if (handle !== undefined && rows.has(handle)) {
      push(routed.byHandle, handle, finding);
      continue;
    }
    const role = finding.params["role"];
    if (role !== undefined) {
      push(routed.byRole, role.toLowerCase(), finding);
      continue;
    }
    if (SCREEN_KEYS.has(finding.key)) {
      routed.screens.push(finding);
      continue;
    }
    routed.document.push(finding);
  }
  return routed;
}

function push<K>(
  map: Map<K, SecurityReferenceFinding[]>,
  key: K,
  finding: SecurityReferenceFinding,
): void {
  const existing = map.get(key);
  if (existing) existing.push(finding);
  else map.set(key, [finding]);
}
