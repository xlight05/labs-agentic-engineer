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

import { Box, Stack, Typography } from "@wso2/oxygen-ui";
import { StatusChip } from "../../../components/StatusChip";
import type { components } from "../../../generated/aep-api";
import { issueStateChip } from "../../tasks/api/status";

type TaskView = components["schemas"]["TaskView"];

// The issues a build session worked, as links — the CODING AGENT stage's own
// content.
//
// Issues appear on that stage and on PROVISIONING (which renders its gates
// itself, labelled with who is acting on each rather than with a GitHub state).
// They are deliberately NOT repeated on the pull request, the merge, the builds
// or the deployment: past the point where the set stops changing, the same chips
// again read as duplication rather than as progress — and the merge's matched set
// IS the coding stage's set.
//
// Each row carries the issue's own DURABLE state (open, or done) and nothing
// else — mid-run liveness on an issue row would be a lie, because the platform
// learns issue facts only when GitHub tells it. The progression is expressed by
// which stage the rows sit under, and the caption says how the console knows
// they are this session's.

export function IssueChips({
  issues,
  caption,
  /** Trim long platform-authored titles (gate titles are prose). */
  label,
}: {
  issues: TaskView[];
  /** How the console knows these are this stage's issues. */
  caption?: string;
  label?: (issue: TaskView) => string;
}) {
  if (issues.length === 0) return null;
  return (
    <Box>
      {caption && (
        <Typography variant="caption" color="text.disabled" sx={{ display: "block", mb: 0.75 }}>
          {caption}
        </Typography>
      )}
      <Stack spacing={0.75}>
        {issues.map((issue) => {
          const chip = issueStateChip(issue.derivedStatus);
          return (
            <Stack
              key={issue.issueNumber}
              direction="row"
              spacing={1}
              sx={{ alignItems: "center", flexWrap: "wrap", rowGap: 0.5 }}
            >
              <Typography
                variant="caption"
                color="text.secondary"
                sx={{ fontVariantNumeric: "tabular-nums", flexShrink: 0 }}
              >
                #{issue.issueNumber}
              </Typography>
              <Typography
                component="a"
                href={issue.issueUrl}
                target="_blank"
                rel="noreferrer"
                variant="body2"
                sx={{
                  color: "text.primary",
                  textDecoration: "none",
                  minWidth: 0,
                  "&:hover": { textDecoration: "underline" },
                }}
              >
                {label ? label(issue) : issue.title}
              </Typography>
              <StatusChip label={chip.label} tone={chip.tone} appearance="soft" />
            </Stack>
          );
        })}
      </Stack>
    </Box>
  );
}
