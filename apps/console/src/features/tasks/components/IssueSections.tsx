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

import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  IconButton,
  ListingTable,
  Stack,
  Tooltip,
  Typography,
} from "@wso2/oxygen-ui";
import { GitHub } from "@wso2/oxygen-ui-icons-react";
import { SectionTitle } from "../../../components/SectionTitle";
import { StatusChip } from "../../../components/StatusChip";
import type { components } from "../../../generated/aep-api";
import { useAllTasks } from "../api/queries";
import { issueKindChip, issueStateChip } from "../api/status";
import { partitionIssues } from "../lib/issueRows";

type TaskView = components["schemas"]["TaskView"];

// A version's issues, as the two things they actually are (§10):
//
//   - the ISSUE LIST — everything the version took to build, carrying durable
//     facts only: the agent's work, and the connections the platform
//     provisioned for it, each tagged with which it is;
//   - a LEDGER of bare human issues, which are never worked and never stall
//     the run, and so must not be mistaken for tasks.
//
// A gate is in the list because it is part of the record: a version's story is
// incomplete if the database it needed simply never appears. What is NOT here
// is the gate WARNING — "why is nothing moving" is a question about the run, so
// the run card answers it, and answering it here as well put two warnings on
// one page competing to explain one fact.
//
// Rows sort by issue number, which is the order the version actually happened
// in: gates are minted before the milestone is planned, so the connections read
// first and the work that consumed them follows.
//
// The validation issue appears in none of them: the list read hides it, because
// the deployment is what is being validated and the verdict renders there.

function IssueRows({ issues }: { issues: TaskView[] }) {
  return (
    <ListingTable.Container sx={{ width: "100%" }} disablePaper>
      <ListingTable variant="card" density="standard">
        <ListingTable.Head>
          <ListingTable.Row>
            <ListingTable.Cell>Issue</ListingTable.Cell>
            <ListingTable.Cell sx={{ maxWidth: 120 }}>Status</ListingTable.Cell>
            <ListingTable.Cell sx={{ maxWidth: 64 }} aria-label="Links" />
          </ListingTable.Row>
        </ListingTable.Head>
        <ListingTable.Body>
          {issues.map((issue) => {
            const chip = issueStateChip(issue.derivedStatus);
            const kind = issueKindChip(issue.executorClass);
            return (
              // Deliberately not clickable. A row shows what GitHub holds; the
              // run's own story is the timeline and the feed above, and the
              // issue itself lives on GitHub — so the only affordance is the
              // link to it. (Agent replies on expand come later.)
              <ListingTable.Row key={issue.issueNumber} variant="card">
                <ListingTable.Cell>
                  <ListingTable.CellIcon
                    icon={
                      <Typography
                        variant="caption"
                        color="text.secondary"
                        sx={{ fontVariantNumeric: "tabular-nums" }}
                      >
                        #{issue.issueNumber}
                      </Typography>
                    }
                    primary={
                      kind ? (
                        <Stack
                          direction="row"
                          spacing={1}
                          sx={{ alignItems: "center", flexWrap: "wrap" }}
                        >
                          <span>{issue.title}</span>
                          <StatusChip
                            label={kind.label}
                            tone={kind.tone}
                            appearance="soft"
                          />
                        </Stack>
                      ) : (
                        issue.title
                      )
                    }
                  />
                </ListingTable.Cell>
                <ListingTable.Cell sx={{ maxWidth: 120 }}>
                  <StatusChip
                    label={chip.label}
                    tone={chip.tone}
                    appearance="soft"
                  />
                </ListingTable.Cell>
                <ListingTable.Cell sx={{ maxWidth: 64 }}>
                  <Tooltip title="Open the GitHub issue">
                    <IconButton
                      component="a"
                      href={issue.issueUrl}
                      target="_blank"
                      rel="noreferrer"
                      aria-label={`GitHub issue #${issue.issueNumber}`}
                    >
                      <GitHub size={16} />
                    </IconButton>
                  </Tooltip>
                </ListingTable.Cell>
              </ListingTable.Row>
            );
          })}
        </ListingTable.Body>
      </ListingTable>
    </ListingTable.Container>
  );
}

export function IssueSections({
  projectName,
  tag,
  live,
}: {
  projectName: string;
  /** The version whose issues to show — milestone membership, not a label. */
  tag: string;
  /** Is a run on this version live? Drives the GitHub-backed poll. */
  live: boolean;
}) {
  const issues = useAllTasks(projectName, tag, { live });

  if (issues.isPending) {
    return (
      <Box sx={{ display: "flex", justifyContent: "center", p: 6 }}>
        <CircularProgress aria-label="Loading issues" />
      </Box>
    );
  }

  if (issues.isError) {
    return (
      <Alert
        severity="error"
        action={<Button onClick={() => void issues.refetch()}>Retry</Button>}
      >
        Failed to load issues
        {issues.error instanceof Error && issues.error.message
          ? `: ${issues.error.message}`
          : ""}
      </Alert>
    );
  }

  // Gates and agent work are one list — see the note above. Only the run card's
  // hold narrows to the OPEN gates.
  const { work, gates, ledger } = partitionIssues(issues.data);
  const rows = [...gates, ...work].sort(
    (a, b) => a.issueNumber - b.issueNumber,
  );

  return (
    <>
      <SectionTitle
        trailing={<Chip label={rows.length} size="small" variant="outlined" />}
      >
        Issues
      </SectionTitle>
      {rows.length === 0 ? (
        <Typography variant="body2" color="text.secondary" sx={{ py: 3 }}>
          No issues for {tag} yet — the build plans them into the version's
          milestone right after it starts.
        </Typography>
      ) : (
        <IssueRows issues={rows} />
      )}

      {ledger.length > 0 && (
        <Box sx={{ mt: 4 }}>
          <SectionTitle
            trailing={
              <Chip label={ledger.length} size="small" variant="outlined" />
            }
          >
            Ledger
          </SectionTitle>
          <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
            Filed against this version by a human. Never worked and never
            holding the run — label one <code>aep:codingagent</code> on GitHub
            to adopt it into the next cycle.
          </Typography>
          <IssueRows issues={ledger} />
        </Box>
      )}
    </>
  );
}
