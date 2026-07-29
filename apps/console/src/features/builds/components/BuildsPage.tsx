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

import { useEffect, useRef } from "react";
import {
  Alert,
  Autocomplete,
  Box,
  Button,
  CircularProgress,
  Stack,
  TextField,
  Typography,
  type TextFieldProps,
} from "@wso2/oxygen-ui";
import { Link } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { PageHeader } from "../../../components/PageHeader";
import { IssueSections } from "../../tasks/components/IssueSections";
import { useAllTasks } from "../../tasks/api/queries";
import { taskKeys } from "../../tasks/api/keys";
import { partitionIssues } from "../../tasks/lib/issueRows";
import { useBuildRuns, useBuilds } from "../api/queries";
import { versionIsLive } from "../lib/runView";
import { RunStory } from "./RunStory";

/**
 * The Builds page is ONE VERSION'S STORY, latest by default.
 *
 * There is no ledger list in between: navigating here while a run is live lands
 * straight on that run, with its feed already open. Old versions are reached
 * through this page's own version picker, which writes `?tag=v<N>`.
 *
 * Two data planes, priced apart. The run rows and cycle records are DB-only, so
 * they poll at 5s while the version is moving. The issue list is GitHub-backed,
 * so it polls only while a run is live — plus exactly one fetch at settle, when
 * the run's last writes (issues closed by merge) have landed.
 */
export function BuildsPage({
  projectName,
  tag,
  onTagChange,
}: {
  projectName: string;
  tag: string | undefined;
  onTagChange: (tag: string | undefined) => void;
}) {
  const builds = useBuilds(projectName);

  // An unknown/absent ?tag falls back to the newest version (the list is
  // newest-first), so a stale shared link degrades to "latest", not a 404.
  const newest = builds.data?.[0];
  const selected = builds.data?.find((b) => b.tag === tag) ?? newest;
  const selectedTag = selected?.tag;

  const runs = useBuildRuns(projectName, selectedTag);
  const runList = runs.data?.runs ?? [];
  const live = versionIsLive(runList);

  // The same query IssueSections reads, on the same key — react-query serves
  // both from one request. The run card needs it because only the issue plane
  // can tell a gate hold apart from an empty working set, and undefined until
  // it lands is what stops a card accusing a run of having no work on the
  // strength of a list that has not arrived.
  const issues = useAllTasks(projectName, selectedTag, { live });
  const partition = issues.data ? partitionIssues(issues.data) : undefined;
  // Both populations WHOLE, closed members included. The run's rail is the
  // version's story, and a provisioned connection is as much a part of how a
  // version came to exist as a merged pull request — while a build session's own
  // issues are closed by the very merge that completed it, so narrowing to the
  // open ones would empty every finished session.
  const milestone = partition && {
    gates: partition.gates,
    work: partition.work,
  };

  // One final issue fetch at settle. The GitHub-backed list stops polling the
  // moment the run turns terminal, but the writes that settle a version (the
  // merge that closes the last issue) can land in the same instant — so the
  // live→settled edge triggers exactly one more read.
  const queryClient = useQueryClient();
  const wasLive = useRef(false);
  useEffect(() => {
    if (wasLive.current && !live && selectedTag) {
      void queryClient.invalidateQueries({
        queryKey: taskKeys.list(projectName, selectedTag),
      });
    }
    wasLive.current = live;
  }, [live, projectName, selectedTag, queryClient]);

  // The header renders through every state below so the back link stays
  // reachable while builds load or fail — the pattern every adopted page uses.
  const backTo = {
    link: <Link to="/projects/$projectName" params={{ projectName }} />,
    label: "Back to Overview",
  };

  if (builds.isPending) {
    return (
      <>
        <PageHeader title="Builds" backTo={backTo} />
        <Box sx={{ display: "flex", justifyContent: "center", p: 6 }}>
          <CircularProgress aria-label="Loading builds" />
        </Box>
      </>
    );
  }

  if (builds.isError) {
    return (
      <>
        <PageHeader title="Builds" backTo={backTo} />
        <Alert
          severity="error"
          action={<Button onClick={() => void builds.refetch()}>Retry</Button>}
        >
          Failed to load builds
          {builds.error instanceof Error && builds.error.message
            ? `: ${builds.error.message}`
            : ""}
        </Alert>
      </>
    );
  }

  if (!newest || !selected || !selectedTag) {
    return (
      <>
        <PageHeader title="Builds" backTo={backTo} />
        <Typography variant="body2" color="text.secondary" sx={{ py: 3 }}>
          No builds yet — publish your spec and click Build in the spec view to
          start the first one.
        </Typography>
      </>
    );
  }

  // The version picker sits at the page-header level so it reads as a
  // page-level control, and the version's story spans full width beneath it.
  const versionSelector = (
    <Autocomplete
      options={builds.data.map((b) => b.tag)}
      value={selected.tag}
      onChange={(_, value) =>
        // Selecting the newest version clears ?tag — the default view.
        onTagChange(value && value !== newest.tag ? value : undefined)
      }
      disableClearable
      size="small"
      sx={{ width: 180, flexShrink: 0 }}
      renderInput={(params) => (
        // MUI's render params don't declare `| undefined` on their optional
        // props, which exactOptionalPropertyTypes rejects — the cast is the
        // documented escape hatch for this spread.
        <TextField {...(params as TextFieldProps)} label="Version" />
      )}
    />
  );

  return (
    <>
      <PageHeader title="Builds" backTo={backTo} actions={versionSelector} />

      {runs.isError ? (
        <Alert
          severity="error"
          sx={{ mb: 3 }}
          action={<Button onClick={() => void runs.refetch()}>Retry</Button>}
        >
          Failed to load {selected.tag}'s runs
          {runs.error instanceof Error && runs.error.message
            ? `: ${runs.error.message}`
            : ""}
        </Alert>
      ) : runs.isPending ? (
        <Box sx={{ display: "flex", justifyContent: "center", p: 4 }}>
          <CircularProgress aria-label="Loading the version's runs" />
        </Box>
      ) : runList.length === 0 ? (
        <Alert severity="info" sx={{ mb: 3 }}>
          {selected.tag} has no run rows — the version was tagged before this
          platform started keeping them.
        </Alert>
      ) : (
        // A milestone sees SEQUENTIAL runs across its life: the spec build that
        // created the version, then any incident adopted into it. Newest first,
        // and only the newest can be live.
        <Stack spacing={2} sx={{ mb: 4 }}>
          {runList.map((run) => (
            <RunStory
              key={run.id}
              projectName={projectName}
              tag={selected.tag}
              run={run}
              {...(milestone ? { milestone } : {})}
            />
          ))}
        </Stack>
      )}

      <IssueSections projectName={projectName} tag={selected.tag} live={live} />
    </>
  );
}
