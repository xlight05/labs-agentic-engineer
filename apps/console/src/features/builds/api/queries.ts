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
import { client } from "../../../api/client";
import { buildKeys } from "./keys";
import { apiErrorMessage } from "../../../api/errors";

// Same cadence as the task list: the builds page is where the user watches a
// running build move; a settled history doesn't need refreshing.
const BUILDS_POLL_MS = 5_000;

// The project's builds, newest first — one entry per built spec version tag
// (#185). Polls while any build is still moving, stops once all are terminal.
export function useBuilds(projectName: string) {
  return useQuery({
    queryKey: buildKeys.list(projectName),
    queryFn: async () => {
      const { data, error } = await client.GET(
        "/projects/{projectName}/builds",
        { params: { path: { projectName } } },
      );
      if (error || data === undefined) {
        throw new Error(apiErrorMessage(error, "Failed to load builds"));
      }
      return data.builds ?? [];
    },
    refetchInterval: (query) => {
      const builds = query.state.data;
      if (!builds) return BUILDS_POLL_MS; // no data yet (or errored) — keep trying
      return builds.some(
        (b) => b.status === "in_progress" || b.status === "started",
      )
        ? BUILDS_POLL_MS
        : false;
    },
  });
}
