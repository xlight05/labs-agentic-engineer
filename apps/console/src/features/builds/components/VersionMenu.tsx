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

import { useState, type MouseEvent } from "react";
import { Chip, ListItemText, Menu, MenuItem, Typography } from "@wso2/oxygen-ui";
import { ChevronDown } from "@wso2/oxygen-ui-icons-react";
import { useNavigate } from "@tanstack/react-router";
import { StatusChip, type StatusTone } from "../../../components/StatusChip";
import type { components } from "../../../generated/aep-api";
import { useBuilds } from "../api/queries";

type BuildSummary = components["schemas"]["BuildSummary"];

// The VERSION LEDGER, as a dropdown on the overview's build stage card. A build
// is a version is a tag is a milestone, so the ledger is just "which versions
// exist" — and it is how a user reaches an old version at all: selecting one
// deep-links the Builds page at ?tag=v<N>, which is that version's whole story.
//
// The list is fetched ON DEMAND — the query is disabled until the menu opens —
// because an idle overview must cost zero polling, and the overview's own 5s
// status poll already carries the current version on the chip.

function ledgerChip(status: BuildSummary["status"]): {
  label: string;
  tone: StatusTone;
} {
  switch (status) {
    case "completed":
      return { label: "Succeeded", tone: "success" };
    case "failed":
      return { label: "Failed", tone: "error" };
    default: // started / in_progress
      return { label: "Running", tone: "info" };
  }
}

export function VersionMenu({
  projectName,
  currentVersion,
  tone,
}: {
  projectName: string;
  /** The version the stage card is stamped with; "" renders an em-dash. */
  currentVersion: string;
  tone: "default" | "info" | "warning" | "success" | "error";
}) {
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const navigate = useNavigate();
  // Enabled only while the menu is open, and never polled: the ledger is a
  // list of past versions, and past versions do not change.
  const builds = useBuilds(projectName, { enabled: anchor !== null, poll: false });

  const open = (event: MouseEvent<HTMLElement>) => {
    // The card behind this chip is a CardActionArea that navigates; opening the
    // ledger must not also follow it.
    event.stopPropagation();
    setAnchor(event.currentTarget);
  };

  const pick = (tag: string) => {
    setAnchor(null);
    void navigate({
      to: "/projects/$projectName/builds",
      params: { projectName },
      search: { tag },
    });
  };

  return (
    <>
      <Chip
        size="small"
        label={currentVersion || "—"}
        color={tone}
        variant={currentVersion ? "filled" : "outlined"}
        clickable
        onClick={open}
        onDelete={open}
        deleteIcon={<ChevronDown size={14} />}
        aria-haspopup="menu"
        aria-label="Choose a version"
      />
      <Menu
        anchorEl={anchor}
        open={anchor !== null}
        onClose={() => setAnchor(null)}
        slotProps={{ list: { dense: true } }}
      >
        {builds.isPending && (
          <MenuItem disabled>
            <Typography variant="body2">Loading versions…</Typography>
          </MenuItem>
        )}
        {builds.isError && (
          <MenuItem disabled>
            <Typography variant="body2" color="error.main">
              Failed to load versions
            </Typography>
          </MenuItem>
        )}
        {builds.data?.length === 0 && (
          <MenuItem disabled>
            <Typography variant="body2">No versions built yet</Typography>
          </MenuItem>
        )}
        {builds.data?.map((build) => {
          const chip = ledgerChip(build.status);
          return (
            <MenuItem key={build.tag} onClick={() => pick(build.tag)}>
              <ListItemText
                primary={build.tag}
                secondary={new Date(build.startedAt).toLocaleDateString()}
                sx={{ mr: 2 }}
              />
              <StatusChip label={chip.label} tone={chip.tone} appearance="soft" />
            </MenuItem>
          );
        })}
      </Menu>
    </>
  );
}
