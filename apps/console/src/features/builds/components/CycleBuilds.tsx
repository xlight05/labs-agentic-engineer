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
import { useBuildLog } from "../hooks/useBuildLog";
import { buildStatusChip } from "../lib/runView";
import { LogNote, LogSurface } from "./AgentLogLines";

type CycleBuild = components["schemas"]["CycleBuild"];

// The builds one build session's merge produced, rendered inside the session
// that caused them.
//
// This is where the agent's work stops being the interesting thing: the session
// is waiting on exactly these builds to decide whether it landed green, so
// putting them anywhere else would separate the wait from the thing waited on.
//
// Status rides the (polled) list read, so a red build reaches the session's
// collapsed strip without anything being opened; a build's LOG is fetched only
// when its row is expanded.

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
 * The build rows of one build session's fan-out — the per-component detail
 * behind the Builds stage.
 *
 * The builds are HANDED IN rather than fetched here: the stage's own state and
 * the deployment stage that follows it are both derived from the same read, and
 * a second query behind this component would let the rows and the stage above
 * them disagree. Whether the read happens at all is the session's decision (see
 * RunSpine) — every merged session, since every Builds stage is on the rail.
 *
 * Renders nothing when there is nothing yet: the stage above already says
 * whether the merge has landed and whether the fan-out has appeared, so an empty
 * box here would only repeat it.
 */
export function CycleBuilds({
  projectName,
  builds,
}: {
  projectName: string;
  builds: CycleBuild[] | undefined;
}) {
  if (builds === undefined || builds.length === 0) return null;
  return (
    <Box>
      {builds.map((build) => (
        <BuildRow key={build.buildName} projectName={projectName} build={build} />
      ))}
    </Box>
  );
}
