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
  Chip,
  Divider,
  Stack,
  Typography,
} from "@wso2/oxygen-ui";
import { X } from "@wso2/oxygen-ui-icons-react";
import { createLink } from "@tanstack/react-router";
import { StatusChip } from "../../../components/StatusChip";
import type { components } from "../../../generated/aep-api";
import { gateSubject } from "../../tasks/lib/issueRows";
import { useCancelRun } from "../api/queries";
import {
  isTerminalRun,
  runHold,
  runOriginLabel,
  runStateChip,
  spentBudgets,
  terminalReasonText,
} from "../lib/runView";
import { CycleSections } from "./CycleSections";
import { RunHoldNotice } from "./RunHoldNotice";

type MilestoneRunView = components["schemas"]["MilestoneRunView"];
type TaskView = components["schemas"]["TaskView"];

// Router-typed Oxygen Button (the console's createLink pattern) so the hold's
// way out is a real navigation.
const LinkButton = createLink(Button);

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
 * One run of the version's milestone loop: its state, why it is not moving if
 * it is not, then one section per cycle telling that cycle's whole story —
 * agent, pull request, merge, builds. Budgets are deliberately NOT a standing
 * readout — see `spentBudgets`; they surface only once one is spent, next to
 * the reason it explains.
 *
 * The hold notice lives HERE, and only here. "Why is nothing happening" is a
 * question about the run, so answering it beside the issue list as well left
 * two warnings competing to explain one fact.
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
   *  is what tells a gate hold apart from an empty working set — and, through
   *  each OPEN gate's provisioning run, a gate the platform is already working
   *  on apart from one stalled on a human. */
  milestone?: { gates: TaskView[]; openWork: number };
}) {
  const cancel = useCancelRun(projectName, tag);
  const chip = runStateChip(run);
  const terminal = isTerminalRun(run.state);
  const waiting = run.state === "waiting";
  const planning = run.state === "planning";
  const hold = runHold(run, milestone);
  // A gate the platform is provisioning is not held on anybody, so it earns no
  // way out: the only two holds with somewhere to go are the ones a human has
  // to act on.
  const gatesNeedAction =
    hold?.kind === "gates" || hold?.kind === "gate-failed";
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
            busy={hold.kind === "planning" || hold.kind === "provisioning"}
            action={
              gatesNeedAction ? (
                <LinkButton
                  size="small"
                  variant="outlined"
                  color={hold.tone === "error" ? "error" : "warning"}
                  to="/projects/$projectName/spec"
                  params={{ projectName }}
                  search={{ connections: "open" }}
                >
                  Resolve connections
                </LinkButton>
              ) : undefined
            }
          >
            {hold.gates.length > 0 && (
              <Stack
                direction="row"
                spacing={1}
                sx={{ mt: 1.25, flexWrap: "wrap", rowGap: 1 }}
              >
                {hold.gates.map((gate) => (
                  <Chip
                    key={gate.issueNumber}
                    component="a"
                    href={gate.issueUrl}
                    target="_blank"
                    rel="noreferrer"
                    clickable
                    size="small"
                    variant="outlined"
                    color={hold.tone}
                    label={gateSubject(gate.title)}
                  />
                ))}
              </Stack>
            )}
          </RunHoldNotice>
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

        {/* A planning run has provably no cycles — the supervisor that
            dispatches them has not been started yet — so the section would say
            only that none exist, under a notice that already said why. */}
        {!planning && (
          <>
            <Divider sx={{ my: 2 }} />
            <CycleSections projectName={projectName} tag={tag} run={run} />
          </>
        )}
      </CardContent>
    </Card>
  );
}
