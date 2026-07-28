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

import type { StatusTone } from "../../../components/StatusChip";
import type { components } from "../../../generated/aep-api";
import { validationView } from "./pipeline";

type ProjectStatus = components["schemas"]["ProjectStatus"];

export interface ProjectChip {
  label: string;
  tone: StatusTone;
}

// The header chip beside a project's name: one line for "where is this project
// right now", folded from the same aggregates the overview pipeline renders
// (`pipeline.ts`).
//
// It is deliberately NOT keyed on `status.phase`. That ladder only tells the
// truth up to the repo and spec rungs: its terminal rung is `tasks`, and the
// server has emitted nothing past it since tasks became GitHub issues (aep-api
// `status_stages.go`, applyFlatArtifactFields). A chip switching on it read
// every project that had a design as "Building" — for good, however long ago
// the build finished and the components went live. Delivery state has real
// sources now, the build and deploy aggregates, so read those.
export function projectChip(status: ProjectStatus): ProjectChip {
  // Repo lifecycle is the phase field's remaining honest job: until the repo is
  // ready the stage aggregates are zero-valued, because the status read returns
  // before it ever looks at them.
  switch (status.phase) {
    case "no-repo":
      return { label: "No repository", tone: "warning" };
    case "repo-cloning":
      return { label: "Preparing repository", tone: "info" };
    case "repo-error":
      return { label: "Repository error", tone: "error" };
  }
  return deliveryChip(status) ?? specChip(status);
}

// Delivery state, loudest first: a failure outranks progress, and progress
// outranks whatever settled behind it (a v2 building over a live v1 reads
// "Building", matching build.version — the newest run — not the live one).
// null when nothing has been delivered yet, which hands the chip to specChip.
function deliveryChip(status: ProjectStatus): ProjectChip | null {
  const { build, deploy } = status;
  if (build.status === "failed") return { label: "Build failed", tone: "error" };
  if (build.status === "running") return { label: "Building", tone: "info" };
  if (deploy.status === "failed") return { label: "Deploy failed", tone: "error" };
  if (deploy.status === "deploying") return { label: "Deploying", tone: "info" };
  // Validation only runs once the components are live, so a validation state
  // means the project IS live even when the binding read lags or comes back
  // empty — the same allowance deployStageView makes for the deploy line.
  if (deploy.status === "deployed" || validationView(deploy.validation)) {
    return { label: "Active", tone: "success" };
  }
  // Built but nothing live: the build settled and the deploy has not started.
  if (build.status === "succeeded") return { label: "Built", tone: "success" };
  return null;
}

// Before the first build, the spec aggregate is the whole story — the same
// three states the spec stage card renders, minus its version chip. A dirty
// spec reads as in-progress: the published version has been edited since.
function specChip(status: ProjectStatus): ProjectChip {
  const { exists, version, dirty } = status.spec;
  if (!exists) return { label: "Starting", tone: "info" };
  if (!version || dirty) return { label: "Spec in progress", tone: "info" };
  return { label: "Spec published", tone: "success" };
}
