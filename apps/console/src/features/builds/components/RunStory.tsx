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
  Card,
  CardContent,
  Divider,
  Stack,
  Typography,
} from "@wso2/oxygen-ui";
import { X } from "@wso2/oxygen-ui-icons-react";
import { StatusChip } from "../../../components/StatusChip";
import type { components } from "../../../generated/aep-api";
import { useCancelRun } from "../api/queries";
import {
  isTerminalRun,
  runHold,
  runOriginLabel,
  runStateChip,
  spentBudgets,
  terminalReasonText,
} from "../lib/runView";
import { RunHoldNotice } from "./RunHoldNotice";
import { RunSpine } from "./RunSpine";

type MilestoneRunView = components["schemas"]["MilestoneRunView"];
type TaskView = components["schemas"]["TaskView"];

function when(value: string | null | undefined): string {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? ""
    : date.toLocaleString(undefined, {
        day: "numeric",
        month: "short",
        hour: "2-digit",
        minute: "2-digit",
      });
}

/**
 * One run of the version's milestone loop, as ONE NUMBERED FLOW: its connections
 * first (only when it needs any), then every build session's stages — agent,
 * pull request, merge, builds, deployment — counting straight through. See
 * RunSpine.
 *
 * Budgets are deliberately NOT a standing readout — see `spentBudgets`; they
 * surface only once one is spent, next to the reason it explains.
 *
 * The gate hold is NOT a notice here. It is the provisioning stage on the rail,
 * because "why is nothing moving" is best answered at the point where movement
 * stopped, naming each connection and who is acting on it. What remains a notice
 * is the handful of holds that name no connection at all: the plan window, an
 * empty milestone, and the unbounded park between sessions.
 *
 * Cancel is PROMINENT on a waiting run, quiet on a running one, and ABSENT
 * while planning: cancel is a signal to the supervisor, and during the plan
 * window there is no supervisor yet to receive it — the button would return
 * 202 and do nothing. A plan that fails settles its own run.
 */
export function RunStory({
  projectName,
  tag,
  run,
  milestone,
}: {
  projectName: string;
  tag: string;
  run: MilestoneRunView;
  /** The milestone's issue plane, or undefined while it is still loading. It
   *  carries the gates the provisioning stage is built from (resolved ones
   *  included — they are the version's record), and the agent work each build
   *  session claims. */
  milestone?: { gates: TaskView[]; work: TaskView[] };
}) {
  const cancel = useCancelRun(projectName, tag);
  const chip = runStateChip(run);
  const terminal = isTerminalRun(run.state);
  const waiting = run.state === "waiting";
  const planning = run.state === "planning";
  const work = milestone?.work ?? [];
  const hold = runHold(
    run,
    milestone && {
      gates: milestone.gates,
      openWork: milestone.work.filter((task) => task.derivedStatus === "pending").length,
    },
  );
  const reason = terminalReasonText(run.terminalReason ?? "");
  const spent = spentBudgets(run.budgets);
  const started = when(run.startedAt ?? run.createdAt);
  const ended = when(run.endedAt);
  // A spent budget on a succeeded run is a footnote, not an alarm — the run
  // simply used its whole allowance. On any other state it is the bad news.
  const tone = run.state === "succeeded" ? "text.secondary" : "error.main";

  return (
    <Card variant="outlined" sx={{ bgcolor: "action.hover" }}>
      <CardContent sx={{ "&:last-child": { pb: 2.5 } }}>
        <Stack
          direction="row"
          spacing={1.5}
          sx={{ alignItems: "center", flexWrap: "wrap", rowGap: 1 }}
        >
          <Typography variant="h6">{run.milestoneTitle}</Typography>
          <StatusChip label={chip.label} tone={chip.tone} appearance="soft" dot />
          <StatusChip
            label={runOriginLabel(run.origin)}
            tone="neutral"
            appearance="soft"
          />
          <Typography variant="body2" color="text.secondary">
            {started ? `Started ${started}` : ""}
            {ended ? ` · ended ${ended}` : ""}
          </Typography>
          <Box sx={{ flexGrow: 1 }} />
          {!terminal && !planning && (
            // Prominent on a parked run — that is the state cancel exists for.
            <Button
              size="small"
              color={waiting ? "warning" : "inherit"}
              variant={waiting ? "contained" : "outlined"}
              startIcon={<X size={16} />}
              disabled={cancel.isPending}
              onClick={() => cancel.mutate(run.id)}
            >
              {cancel.isPending ? "Cancelling…" : "Cancel run"}
            </Button>
          )}
        </Stack>

        {hold && (
          <RunHoldNotice
            tone={hold.tone}
            title={hold.title}
            body={hold.body}
            busy={hold.kind === "planning"}
          />
        )}

        {cancel.isError && (
          <Alert severity="error" sx={{ mt: 2 }}>
            {cancel.error instanceof Error
              ? cancel.error.message
              : "Failed to cancel the run"}
            . Nothing was cancelled — you can retry.
          </Alert>
        )}

        {(reason || spent.length > 0) && (
          <Stack spacing={0.5} sx={{ mt: 2 }}>
            {reason && (
              <Typography variant="body2" color={tone}>
                {reason}
              </Typography>
            )}
            {spent.length > 0 && (
              <Typography
                variant="caption"
                color={tone}
                sx={{ fontVariantNumeric: "tabular-nums" }}
              >
                {`Budget spent: ${spent
                  .map((budget) => `${budget.label} ${budget.text}`)
                  .join(" · ")}`}
              </Typography>
            )}
          </Stack>
        )}

        {/* A planning run has provably no build sessions — the supervisor that
            dispatches them has not been started yet — so the rail would say
            only that none exist, under a notice that already said why. */}
        {!planning && (
          <>
            <Divider sx={{ my: 2 }} />
            <RunSpine
              projectName={projectName}
              tag={tag}
              run={run}
              gates={milestone?.gates ?? []}
              work={work}
            />
          </>
        )}
      </CardContent>
    </Card>
  );
}
