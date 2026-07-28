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

import { useState } from "react";
import { Box, Button, Stack, Typography } from "@wso2/oxygen-ui";
import { ScrollText } from "@wso2/oxygen-ui-icons-react";
import { StatusChip } from "../../../components/StatusChip";
import type { components } from "../../../generated/aep-api";
import { useCycleBuilds } from "../api/queries";
import { useBuildLog } from "../hooks/useBuildLog";
import { buildStatusChip } from "../lib/runView";
import { LogNote, LogSurface } from "./AgentLogLines";

type CycleBuild = components["schemas"]["CycleBuild"];

// The builds one cycle's merge produced — the second half of a cycle's story,
// rendered inside the cycle that caused them.
//
// This is where the agent's work stops being the interesting thing: the cycle
// is waiting on exactly these builds to decide whether it landed green, so
// putting them anywhere else would separate the wait from the thing waited on.
//
// Status rides the (polled) list read so a red build is visible WITHOUT opening
// anything; the log is fetched only when a row is expanded.

function BuildLog({
  projectName,
  build,
}: {
  projectName: string;
  build: CycleBuild;
}) {
  const log = useBuildLog(projectName, build.component, build.buildName, true);

  if (log.error) {
    return (
      <LogSurface maxHeight={300}>
        <LogNote>{log.error}</LogNote>
      </LogSurface>
    );
  }
  if (log.loading) {
    return (
      <LogSurface maxHeight={300}>
        <LogNote>Loading the build log…</LogNote>
      </LogSurface>
    );
  }
  if (log.entries.length === 0) {
    // A completed build with no entries is not an error — the cluster's log
    // retention window has passed. The outcome is still on the row above.
    return (
      <LogSurface maxHeight={300}>
        <LogNote>
          {log.complete
            ? "No log retained for this build. Its outcome is still recorded above."
            : "This build has not written anything yet."}
        </LogNote>
      </LogSurface>
    );
  }
  return (
    <LogSurface maxHeight={300}>
      {log.entries.map((entry, i) => (
        <Typography
          key={`${entry.timestamp ?? ""}:${i}`}
          component="div"
          sx={{
            font: "inherit",
            color: "grey.300",
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
          }}
        >
          {entry.log}
        </Typography>
      ))}
      {!log.complete && <LogNote>…tailing</LogNote>}
    </LogSurface>
  );
}

function BuildRow({
  projectName,
  build,
}: {
  projectName: string;
  build: CycleBuild;
}) {
  const [open, setOpen] = useState(false);
  const chip = buildStatusChip(build);
  return (
    <Box sx={{ py: 1, borderTop: 1, borderColor: "divider" }}>
      <Stack
        direction="row"
        spacing={1.5}
        sx={{ alignItems: "center", flexWrap: "wrap", rowGap: 0.5 }}
      >
        <StatusChip label={chip.label} tone={chip.tone} appearance="soft" dot />
        <Typography variant="body2" sx={{ fontWeight: 500 }}>
          {build.component}
        </Typography>
        {build.attempt > 1 && (
          // The one automatic re-trigger a red build gets, per component per
          // SHA. A second attempt means the first build failed.
          <Typography variant="caption" color="warning.main">
            attempt {build.attempt}
          </Typography>
        )}
        <Box sx={{ flexGrow: 1 }} />
        <Button
          size="small"
          variant="outlined"
          startIcon={<ScrollText size={14} />}
          onClick={() => setOpen((v) => !v)}
        >
          {open ? "Hide log" : "Show log"}
        </Button>
      </Stack>
      {open && (
        <Box sx={{ mt: 1 }}>
          <BuildLog projectName={projectName} build={build} />
        </Box>
      )}
    </Box>
  );
}

/**
 * A cycle's build fan-out. Renders nothing at all until the cycle has a merge
 * SHA — before that there is nothing to have built, and an empty box would read
 * as "the builds failed to appear".
 */
export function CycleBuilds({
  projectName,
  tag,
  cycleId,
  mergeSha,
}: {
  projectName: string;
  tag: string;
  cycleId: string;
  mergeSha: string;
}) {
  const hasMerge = Boolean(mergeSha);
  const { data: builds, isPending } = useCycleBuilds(
    projectName,
    tag,
    cycleId,
    hasMerge,
  );

  if (!hasMerge) return null;

  return (
    <Box sx={{ mt: 2 }}>
      <Typography variant="overline" color="text.secondary">
        Builds
      </Typography>
      {isPending || builds === undefined ? (
        <Typography variant="body2" color="text.secondary" sx={{ py: 1 }}>
          Reading this merge's builds…
        </Typography>
      ) : builds.length === 0 ? (
        <Typography variant="body2" color="text.secondary" sx={{ py: 1 }}>
          The merge has not produced a build yet.
        </Typography>
      ) : (
        builds.map((build) => (
          <BuildRow key={build.buildName} projectName={projectName} build={build} />
        ))
      )}
    </Box>
  );
}
