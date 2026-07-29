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
  CircularProgress,
  IconButton,
  Stack,
  Tooltip,
} from "@wso2/oxygen-ui";
import { FileText, GitPullRequest, ScrollText } from "@wso2/oxygen-ui-icons-react";
import { Link } from "@tanstack/react-router";
import { ValidationView } from "@aep/ui-validation-view";
import { PageHeader, type PageHeaderStatus } from "../../../components/PageHeader";
import { EmptyState } from "../../../components/EmptyState";
import { useProjectStatus } from "../../projects/api/queries";
import { useBuildRuns } from "../../builds/api/queries";
import { RunFeed } from "../../builds/components/RunFeed";
import { validationVerdictChip } from "../../builds/lib/runView";
import { useValidationCriteria, useValidationReport } from "../api/queries";

// Validation lives on the DEPLOYMENT surface because the deployment is what is
// being validated. Its verdict is a RUN property — read from the version's run
// story (list-build-runs), which is the only place the platform keeps it; there
// is no validation endpoint. The page joins that verdict with the authored
// oracle (specs/validation/validation-criteria.json) and the runner's committed
// report, both read at HEAD through the Files API.

// The validation cycle is the phase of the run this page owns; the rest of the
// loop is the Builds page's story.
const VALIDATION_CYCLE = ["validation"] as const;

// Header chip for the run's verdict, falling back to the coarse lifecycle state
// while no verdict exists yet.
function headerChip(
  verdict: ReturnType<typeof validationVerdictChip>,
  running: boolean,
): PageHeaderStatus | undefined {
  if (verdict) return { label: verdict.label, tone: verdict.tone };
  return running ? { label: "Validating", tone: "info" } : undefined;
}

/**
 * The Validation page: a read-only report of the deployed system against its
 * acceptance criteria, plus the validation cycle's live log. No writes.
 */
export function ValidationPage({
  projectName,
  view,
  onViewChange,
}: {
  projectName: string;
  view: "logs" | undefined;
  onViewChange: (view: "logs" | undefined) => void;
}) {
  const status = useProjectStatus(projectName);
  const deploy = status.data?.deploy;
  const version = deploy?.version ?? "";
  const running = deploy?.validation === "running";

  // The deployed version's runs: the newest is the one that landed it, and its
  // validation record is this page's subject.
  const runs = useBuildRuns(projectName, version || undefined);
  const run = runs.data?.runs?.[0];
  const verdict = validationVerdictChip(run?.validation);
  const reportPath = run?.validation?.reportPath ?? "";
  const validationCycle = run?.cycles?.find((c) => c.kind === "validation");
  // The cycle carries the pull request's page as the webhook reported it. This
  // page used to build one from the project's repoUrl and the number, which is a
  // CLONE url — a `.git` suffix produced a link that 404s.
  const prUrl = validationCycle?.prUrl;

  // A verdict means the run committed its report; before that there is nothing
  // at HEAD to read. Hooks stay unconditional; `enabled` gates them.
  const settled = verdict !== null && verdict.label !== "Validation skipped";
  const criteria = useValidationCriteria(projectName, version, settled);
  const report = useValidationReport(projectName, version, settled, reportPath);

  // Body rule: the log shows while there is no report to show (running, failed
  // mechanically, nothing settled yet) OR the user toggled ?view=logs.
  const showLogs = !settled || view === "logs";

  const chip = headerChip(verdict, running);
  const header = (
    <PageHeader
      title="Validation"
      backTo={{
        link: <Link to="/projects/$projectName" params={{ projectName }} />,
        label: "Back to Overview",
      }}
      {...(chip ? { status: chip } : {})}
      actions={
        <Stack direction="row" spacing={1} sx={{ alignItems: "center" }}>
          {prUrl && (
            <Tooltip title="Open the validation PR">
              <IconButton
                component="a"
                href={prUrl}
                target="_blank"
                rel="noreferrer"
                aria-label="Validation pull request"
              >
                <GitPullRequest size={18} />
              </IconButton>
            </Tooltip>
          )}
          {settled &&
            (showLogs ? (
              <Button
                size="small"
                variant="outlined"
                startIcon={<FileText size={16} />}
                onClick={() => onViewChange(undefined)}
              >
                View report
              </Button>
            ) : (
              <Button
                size="small"
                variant="outlined"
                startIcon={<ScrollText size={16} />}
                onClick={() => onViewChange("logs")}
              >
                View logs
              </Button>
            ))}
        </Stack>
      }
    />
  );

  if (status.isPending || (version !== "" && runs.isPending)) {
    return (
      <>
        {header}
        <Box sx={{ display: "flex", justifyContent: "center", p: 6 }}>
          <CircularProgress aria-label="Loading validation" />
        </Box>
      </>
    );
  }

  if (status.isError) {
    return (
      <>
        {header}
        <Alert
          severity="error"
          action={<Button onClick={() => void status.refetch()}>Retry</Button>}
        >
          Failed to load validation
          {status.error instanceof Error && status.error.message
            ? `: ${status.error.message}`
            : ""}
        </Alert>
      </>
    );
  }

  // Nothing to show: no deployed version, or its run never reached validation.
  if (!run || (!validationCycle && !verdict)) {
    return (
      <>
        {header}
        <EmptyState
          compact
          description="No validation has run yet — it runs automatically once the project's components are deployed to dev and the version's work is done."
        />
      </>
    );
  }

  if (verdict?.label === "Validation skipped") {
    return (
      <>
        {header}
        <EmptyState
          compact
          description="This version was not validated — it has no acceptance criteria, or it was an incident run, which gets no validation cycle."
        />
      </>
    );
  }

  if (showLogs) {
    return (
      <>
        {header}
        <RunFeed
          projectName={projectName}
          runId={run.id}
          cycleKinds={VALIDATION_CYCLE}
        />
      </>
    );
  }

  return (
    <>
      {header}
      {criteria.isPending || (!criteria.isError && !criteria.data) ? (
        <Box sx={{ display: "flex", justifyContent: "center", p: 6 }}>
          <CircularProgress aria-label="Loading validation report" />
        </Box>
      ) : criteria.isError ? (
        <Alert
          severity="error"
          action={<Button onClick={() => void criteria.refetch()}>Retry</Button>}
        >
          Failed to load the validation criteria
          {criteria.error instanceof Error && criteria.error.message
            ? `: ${criteria.error.message}`
            : ""}
        </Alert>
      ) : (
        <>
          {report.isError && (
            <Box sx={{ px: 3, pt: 2 }}>
              <Alert severity="info">
                The run reached a verdict but its report wasn't found — showing
                the criteria without per-criterion results.
              </Alert>
            </Box>
          )}
          <ValidationView
            criteria={criteria.data.content}
            {...(report.data ? { report: report.data.content } : {})}
          />
        </>
      )}
    </>
  );
}
