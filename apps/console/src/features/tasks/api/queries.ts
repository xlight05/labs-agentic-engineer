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
import { taskKeys } from "./keys";
import { isActiveStatus } from "./status";
import { apiErrorMessage } from "../../../api/errors";

// Poll while any task is non-terminal, stop once everything settles (#173
// decisions). 5s: the list is where the user watches chips go green.
const TASKS_POLL_MS = 5_000;

// The task list (state=all — a merged PR auto-closes the task's GitHub
// issue, so `open` would hide Done and build-failed tasks). `tag` scopes the
// read to one build's lineage (aep:spec/<tag>, #185); omitted = all versions.
export function useAllTasks(projectName: string, tag?: string) {
  return useQuery({
    queryKey: taskKeys.list(projectName, tag),
    queryFn: async () => {
      const { data, error } = await client.GET(
        "/projects/{projectName}/tasks",
        {
          params: {
            path: { projectName },
            query: { state: "all", ...(tag && { tag }) },
          },
        },
      );
      if (error || data === undefined) {
        throw new Error(apiErrorMessage(error, "Failed to load tasks"));
      }
      return data ?? [];
    },
    refetchInterval: (query) => {
      const tasks = query.state.data;
      if (!tasks) return TASKS_POLL_MS; // no data yet (or errored) — keep trying
      return tasks.some((t) => isActiveStatus(t.derivedStatus))
        ? TASKS_POLL_MS
        : false;
    },
  });
}

// One task with its execution history — the task page's initial state; the
// log stream (useTaskLog) takes over from there.
export function useTask(projectName: string, issueNumber: number) {
  return useQuery({
    queryKey: taskKeys.detail(projectName, issueNumber),
    queryFn: async () => {
      const { data, error } = await client.GET(
        "/projects/{projectName}/tasks/{issueNumber}",
        { params: { path: { projectName, issueNumber } } },
      );
      if (error || data === undefined) {
        throw new Error(apiErrorMessage(error, "Failed to load the task"));
      }
      return data;
    },
    staleTime: 30_000,
  });
}
