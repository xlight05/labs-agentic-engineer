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
  AlertTitle,
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
import { createLink } from "@tanstack/react-router";
import { SectionTitle } from "../../../components/SectionTitle";
import { StatusChip } from "../../../components/StatusChip";
import type { components } from "../../../generated/aep-api";
import { useAllTasks } from "../api/queries";
import { issueStateChip } from "../api/status";
import { gateSubject, partitionIssues } from "../lib/issueRows";

type TaskView = components["schemas"]["TaskView"];

// Router-typed Oxygen Button (the console's createLink pattern, cf.
// DeploymentsPage) so the banner's deep link is a real navigation.
const LinkButton = createLink(Button);

// A version's issues, as the three things they actually are (§10):
//
//   - a HOLD BANNER for open dispatch gates — a gate is not a row, it is the
//     reason nothing else is moving;
//   - the ISSUE LIST of agent work, carrying durable facts only;
//   - a LEDGER of bare human issues, which are never worked and never stall
//     the run, and so must not be mistaken for tasks.
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
                    primary={issue.title}
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

/**
 * Open dispatch gates, as the hold they are. A gate is the one deliberate
 * human brake on the loop: while any gate in the milestone is open, the run
 * dispatches nothing, so the remaining issues are held rather than idle.
 *
 * Gates are resolved in the architecture view's connection drawer, which is
 * where a dependency's configuration is supplied — hence the deep link.
 */
function GateHoldBanner({
  projectName,
  gates,
}: {
  projectName: string;
  gates: TaskView[];
}) {
  return (
    <Alert
      severity="warning"
      sx={{ mb: 3 }}
      action={
        <LinkButton
          size="small"
          color="inherit"
          to="/projects/$projectName/spec"
          params={{ projectName }}
          search={{ connections: "open" }}
        >
          Resolve connections
        </LinkButton>
      }
    >
      <AlertTitle>
        {gates.length === 1
          ? "A connection is unresolved"
          : `${gates.length} connections are unresolved`}
      </AlertTitle>
      Remaining tasks are held until it is resolved — the run dispatches nothing
      while a gate is open.
      <Stack direction="row" spacing={1} sx={{ mt: 1, flexWrap: "wrap", rowGap: 1 }}>
        {gates.map((gate) => (
          <Chip
            key={gate.issueNumber}
            component="a"
            href={gate.issueUrl}
            target="_blank"
            rel="noreferrer"
            clickable
            size="small"
            variant="outlined"
            color="warning"
            label={gateSubject(gate.title)}
          />
        ))}
      </Stack>
    </Alert>
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

  const { work, gates, ledger } = partitionIssues(issues.data);

  return (
    <>
      {gates.length > 0 && (
        <GateHoldBanner projectName={projectName} gates={gates} />
      )}

      <SectionTitle
        trailing={<Chip label={work.length} size="small" variant="outlined" />}
      >
        Issues
      </SectionTitle>
      {work.length === 0 ? (
        <Typography variant="body2" color="text.secondary" sx={{ py: 3 }}>
          No issues for {tag} yet — the build plans them into the version's
          milestone right after it starts.
        </Typography>
      ) : (
        <IssueRows issues={work} />
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
