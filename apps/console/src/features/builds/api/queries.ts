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

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { client } from "../../../api/client";
import { apiErrorMessage } from "../../../api/errors";
import { buildKeys } from "./keys";
import { versionIsLive } from "../lib/runView";

// Both reads below are DB-only on the server (run rows and cycle records fed by
// webhooks — no GitHub, no cluster), which is what makes a 5s poll affordable.
// The GitHub-backed issue read is priced separately and polls only while a run
// is live; see useAllTasks.
const RUNS_POLL_MS = 5_000;

/**
 * The version ledger, newest first — one row per built spec version tag. It
 * backs the Builds page's version picker, and polls while a version is moving.
 */
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
      if (!builds) return RUNS_POLL_MS; // no data yet (or errored) — keep trying
      return builds.some(
        (b) => b.status === "in_progress" || b.status === "started",
      )
        ? RUNS_POLL_MS
        : false;
    },
  });
}

/**
 * One version's whole run story: every milestone run that has worked it,
 * newest first, each with its cycle records in dispatch order.
 *
 * This query is the Builds page's liveness driver. Polling stops the moment the
 * newest run is terminal — the old poll-stop read task derivedStatus, which
 * after the flip only says whether a GitHub issue is open.
 */
export function useBuildRuns(projectName: string, tag: string | undefined) {
  return useQuery({
    queryKey: buildKeys.runs(projectName, tag ?? ""),
    enabled: Boolean(tag),
    queryFn: async () => {
      const { data, error } = await client.GET(
        "/projects/{projectName}/builds/{tag}/runs",
        { params: { path: { projectName, tag: tag ?? "" } } },
      );
      if (error || data === undefined) {
        throw new Error(apiErrorMessage(error, "Failed to load the version's runs"));
      }
      return data;
    },
    refetchInterval: (query) => {
      const list = query.state.data;
      if (!list) return RUNS_POLL_MS; // no data yet (or errored) — keep trying
      return versionIsLive(list.runs) ? RUNS_POLL_MS : false;
    },
  });
}

/**
 * Cancel a milestone run — abandoning the increment. Cancel is the only expiry
 * the run's unbounded wait state has.
 *
 * 202 only means the signal was sent; the run row flips to `cancelled` when the
 * supervisor acts on it, so success invalidates rather than writes optimistically.
 * No retry (a failed write is surfaced, never silently repeated): a 503 here
 * means the workflow engine is unreachable and NOTHING was cancelled.
 */
export function useCancelRun(projectName: string, tag: string | undefined) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (runId: string) => {
      const { error } = await client.POST(
        "/projects/{projectName}/runs/{runId}/cancel",
        { params: { path: { projectName, runId } } },
      );
      if (error) {
        throw new Error(apiErrorMessage(error, "Failed to cancel the run"));
      }
    },
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: buildKeys.runs(projectName, tag ?? ""),
      });
    },
  });
}

// A cycle's builds are DERIVED from the cluster on read, so this one is NOT
// free the way the run read is. It polls slower, and only while the cycle's
// builds are still moving.
const CYCLE_BUILDS_POLL_MS = 10_000;

/**
 * The builds one cycle's merge fanned out to.
 *
 * Enabled only for a cycle that HAS a merge SHA: before the merge there is
 * nothing to have built, and asking would spend a cluster read to be told so.
 * Polling stops once every build is completed — a settled fan-out cannot change.
 */
export function useCycleBuilds(
  projectName: string,
  tag: string | undefined,
  cycleId: string,
  hasMerge: boolean,
) {
  return useQuery({
    queryKey: buildKeys.cycleBuilds(projectName, tag ?? "", cycleId),
    enabled: Boolean(tag) && hasMerge,
    queryFn: async () => {
      const { data, error } = await client.GET(
        "/projects/{projectName}/builds/{tag}/cycles/{cycleId}/builds",
        { params: { path: { projectName, tag: tag ?? "", cycleId } } },
      );
      if (error || data === undefined) {
        throw new Error(apiErrorMessage(error, "Failed to load the cycle's builds"));
      }
      return data.items ?? [];
    },
    refetchInterval: (query) => {
      const builds = query.state.data;
      if (!builds) return CYCLE_BUILDS_POLL_MS; // no data yet — keep trying
      // An empty list on a merged cycle means the fan-out has not appeared in
      // the cluster yet, so keep asking.
      if (builds.length === 0) return CYCLE_BUILDS_POLL_MS;
      return builds.every((b) => b.completed) ? false : CYCLE_BUILDS_POLL_MS;
    },
  });
}
