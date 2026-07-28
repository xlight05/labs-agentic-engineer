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
  Avatar,
  Box,
  Button,
  Card,
  CardContent,
  Chip,
  CircularProgress,
  Link as MuiLink,
  Stack,
  Typography,
} from "@wso2/oxygen-ui";
import { ExternalLink } from "@wso2/oxygen-ui-icons-react";
import { createLink, Link } from "@tanstack/react-router";
import { EmptyState } from "../../../components/EmptyState";
import { PageHeader } from "../../../components/PageHeader";
import { SectionTitle } from "../../../components/SectionTitle";
import { StatusChip, type StatusTone } from "../../../components/StatusChip";
import {
  useComponentsDeployments,
  useProjectComponents,
  useProjectStatus,
} from "../api/queries";
import {
  groupDeploymentCards,
  type DeploymentCard,
} from "../lib/deploymentRows";
import { projectChip } from "../lib/projectChip";
import { validationView } from "../lib/pipeline";

const LinkChip = createLink(Chip);

// Chip vocabulary for a card's state (#216): the label keeps the backend's
// raw condition reason (it's the vocabulary operators see in OpenChoreo),
// only the two join-derived states get console-authored labels. Tones feed
// the shared StatusChip (Task 4) so this card's chip shares one palette
// with every other status pill in the app.
function cardChip(card: DeploymentCard): {
  label: string;
  tone: StatusTone;
  outlined?: boolean;
} {
  switch (card.kind) {
    case "notDeployed":
      return { label: "Not deployed", tone: "neutral", outlined: true };
    case "undeployed":
      return { label: "Undeployed", tone: "neutral" };
    case "success":
      return { label: card.deployment?.status ?? "Ready", tone: "success" };
    case "error":
      return { label: card.deployment?.status ?? "Failed", tone: "error" };
    case "transitional":
      return { label: card.deployment?.status ?? "In progress", tone: "info" };
    default:
      return { label: "Pending", tone: "neutral", outlined: true };
  }
}

function deployedAt(createdAt: string | undefined): string | null {
  if (!createdAt) return null;
  const date = new Date(createdAt);
  return Number.isNaN(date.getTime()) ? null : date.toLocaleString();
}

function BoardCard({ card, column }: { card: DeploymentCard; column: string }) {
  const chip = cardChip(card);
  const d = card.deployment;
  const when = deployedAt(d?.createdAt);
  const notDeployed = card.kind === "notDeployed";
  return (
    <Card
      variant="outlined"
      sx={{
        width: "100%",
        ...(notDeployed && { opacity: 0.6, borderStyle: "dashed" }),
      }}
    >
      <CardContent sx={{ "&:last-child": { pb: 2 } }}>
        <Stack direction="row" spacing={1.5} sx={{ alignItems: "center" }}>
          <Avatar
            sx={{
              width: 28,
              height: 28,
              bgcolor: "action.hover",
              color: "text.primary",
              fontSize: 14,
            }}
          >
            {(card.displayName.trim()[0] ?? "C").toUpperCase()}
          </Avatar>
          <Typography variant="subtitle2" sx={{ flexGrow: 1, minWidth: 0 }}>
            {card.displayName}
          </Typography>
          <StatusChip
            label={chip.label}
            tone={chip.tone}
            {...(chip.outlined && { variant: "outlined" as const })}
          />
        </Stack>
        {/* A binding from an environment that isn't the column's own (e.g.
            staging on the Development board) keeps its env visible. */}
        {d?.environment && d.environment !== column && (
          <Chip
            label={d.environment}
            size="small"
            variant="outlined"
            sx={{ mt: 1 }}
          />
        )}
        {(d?.releaseName || d?.endpointUrl || when) && (
          <Stack spacing={0.5} sx={{ mt: 1.5 }}>
            {d?.releaseName && (
              <Typography
                variant="caption"
                color="text.secondary"
                sx={{
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {d.releaseName}
              </Typography>
            )}
            <Stack
              direction="row"
              spacing={1}
              sx={{ alignItems: "center", justifyContent: "space-between" }}
            >
              {when ? (
                <Typography variant="caption" color="text.secondary">
                  {when}
                </Typography>
              ) : (
                <span />
              )}
              {d?.endpointUrl && (
                <MuiLink
                  href={d.endpointUrl}
                  target="_blank"
                  rel="noreferrer"
                  variant="body2"
                  sx={{
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 0.5,
                    flexShrink: 0,
                  }}
                >
                  Open <ExternalLink size={14} />
                </MuiLink>
              )}
            </Stack>
          </Stack>
        )}
      </CardContent>
    </Card>
  );
}

function BoardColumn({
  title,
  column,
  cards,
  emptyText,
  projectName,
  version,
  validation,
}: {
  title: string;
  column: string;
  cards: DeploymentCard[];
  emptyText: string;
  projectName: string;
  version?: string;
  // The whole-project validation run state for this environment (dev only).
  // null = nothing to show; otherwise the chip opens the Validation page.
  validation?: ReturnType<typeof validationView>;
}) {
  // Capitalize the shared lowercase label for the chip ("validating" → "Validating").
  const validationLabel = validation
    ? validation.label.charAt(0).toUpperCase() + validation.label.slice(1)
    : "";
  return (
    <Box
      sx={{
        flex: 1,
        minWidth: 0,
        bgcolor: "background.default",
        border: 1,
        borderColor: "divider",
        borderRadius: 2,
        p: 2,
      }}
    >
      <SectionTitle
        trailing={
          <>
            <Chip label={cards.length} size="small" variant="outlined" />
            {version && (
              <Chip
                label={version}
                size="small"
                color="primary"
                variant="outlined"
                title="Spec version live in this environment"
              />
            )}
            {validation && (
              // Opens the top-level Validation page, which owns the report, the
              // run log, and the issue/PR links across every lifecycle state.
              <LinkChip
                label={validationLabel}
                size="small"
                color={validation.tone as "info" | "success" | "error"}
                variant="outlined"
                clickable
                to="/projects/$projectName/validation"
                params={{ projectName }}
                title="Open validation"
              />
            )}
          </>
        }
      >
        {title}
      </SectionTitle>
      {cards.length === 0 ? (
        <EmptyState compact description={emptyText} />
      ) : (
        <Stack spacing={1.5}>
          {cards.map((card) => (
            <BoardCard
              key={`${card.componentName}/${card.deployment?.environment ?? ""}`}
              card={card}
              column={column}
            />
          ))}
        </Stack>
      )}
    </Box>
  );
}

// Deployments board (#216): what's running and what isn't, as two
// environment columns. Data is the components list plus one
// list-deployments read per component (the endpoints the overview already
// uses — no bespoke aggregate), so a single component's failed read
// degrades to a warning instead of blanking the board.
export function DeploymentsPage({ projectName }: { projectName: string }) {
  const components = useProjectComponents(projectName);
  const componentNames = (components.data?.items ?? []).map((c) => c.name);
  const deployments = useComponentsDeployments(projectName, componentNames);
  // The status poll's deploy aggregate (#184) carries the spec tag live in
  // dev ("v1"); the layout already runs this query, so the read is free.
  const status = useProjectStatus(projectName);
  const devVersion = status.data?.deploy.version || undefined;
  // Whole-project validation runs against dev after components deploy; surface
  // its coarse state on the Development column header. The chip opens the
  // top-level Validation page, which owns the report, the run log, and the
  // issue/PR links across every lifecycle state.
  const devValidation = validationView(status.data?.deploy.validation ?? "");

  // Unconditional, like Builds (Task 5): the back link and project status
  // stay reachable through every state below, not just the loaded board.
  const header = (
    <PageHeader
      title="Deployments"
      {...(status.data && { status: projectChip(status.data) })}
      backTo={{
        link: <Link to="/projects/$projectName" params={{ projectName }} />,
        label: "Back to Overview",
      }}
    />
  );

  if (components.isPending || (componentNames.length > 0 && deployments.isPending)) {
    return (
      <>
        {header}
        <Box sx={{ display: "flex", justifyContent: "center", p: 6 }}>
          <CircularProgress aria-label="Loading deployments" />
        </Box>
      </>
    );
  }

  if (components.isError) {
    return (
      <>
        {header}
        <Alert
          severity="error"
          action={
            <Button onClick={() => void components.refetch()}>Retry</Button>
          }
        >
          Failed to load deployments
          {components.error instanceof Error && components.error.message
            ? `: ${components.error.message}`
            : ""}
        </Alert>
      </>
    );
  }

  if (componentNames.length === 0) {
    return (
      <>
        {header}
        <EmptyState
          compact
          description="Nothing to deploy yet — components appear here once the published design produces them, and agents deploy to dev on merge."
        />
      </>
    );
  }

  const board = groupDeploymentCards(
    components.data?.items ?? [],
    deployments.deployments,
  );

  return (
    <>
      {header}
      {deployments.failedCount > 0 && (
        <Alert severity="warning" sx={{ mb: 2 }}>
          Deployments for {deployments.failedCount} component
          {deployments.failedCount === 1 ? "" : "s"} could not be loaded — the
          board shows what did.
        </Alert>
      )}
      <Stack
        direction={{ xs: "column", md: "row" }}
        spacing={2}
        sx={{ alignItems: "stretch" }}
      >
        <BoardColumn
          title="Development"
          column="development"
          cards={board.development}
          emptyText="Nothing in development yet."
          projectName={projectName}
          {...(devVersion && { version: devVersion })}
          validation={devValidation}
        />
        <BoardColumn
          title="Production"
          column="production"
          cards={board.production}
          emptyText="Nothing in production yet — dev is the only deploy target for now."
          projectName={projectName}
        />
      </Stack>
    </>
  );
}
