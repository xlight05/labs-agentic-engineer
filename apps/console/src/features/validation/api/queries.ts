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

import { useQuery } from "@tanstack/react-query";
import { fetchSpecFileContent } from "../../spec/api/queries";
import { validationKeys } from "./keys";

// The two files the Validation page joins, both read at HEAD via the Files API
// (report.json is reachable through the read-only allow-list on read-file).
//
// The report's path is also carried on the RUN (RunValidation.reportPath), which
// is authoritative — the runner writes the path it actually committed. This
// constant is the fallback for a run that recorded no path.
export const CRITERIA_PATH = "specs/validation/validation-criteria.json";
export const REPORT_PATH = "tests/validation/report.json";

// Fetch one validation artifact's content at HEAD. Reuses the spec Files reader
// (path-agnostic; `sha` only feeds its cache key, never the request — we key our
// own query instead). Keyed by (path, version) so a newly merged run refetches;
// retry is off since a 404 (no report yet) is deterministic, not transient.
function useValidationFile(
  projectName: string,
  path: string,
  version: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: validationKeys.file(projectName, path, version),
    enabled,
    retry: false,
    staleTime: 30_000,
    queryFn: () => fetchSpecFileContent(projectName, { path, sha: "" }),
  });
}

/** The acceptance oracle (specs/validation/validation-criteria.json). */
export function useValidationCriteria(
  projectName: string,
  version: string,
  enabled: boolean,
) {
  return useValidationFile(projectName, CRITERIA_PATH, version, enabled);
}

/** The runner's run report, at the path the run recorded (or the default). */
export function useValidationReport(
  projectName: string,
  version: string,
  enabled: boolean,
  reportPath?: string,
) {
  return useValidationFile(
    projectName,
    reportPath || REPORT_PATH,
    version,
    enabled,
  );
}
