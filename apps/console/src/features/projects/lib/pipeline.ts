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

import type { components } from "../../../generated/aep-api";

type ProjectStatus = components["schemas"]["ProjectStatus"];

// One rendered stage of the overview pipeline (#183). Views are pure
// derivations of the ProjectStatus stage aggregates — the spec stage in
// particular has no stored status: exists/version/dirty decide everything.
export type StageTone = "ghost" | "neutral" | "info" | "warning" | "success" | "error";

export interface StageView {
  /** Version chip text ("v1", "v1+"); "" renders an em-dash. */
  version: string;
  /** One-line state under the chip. */
  line: string;
  tone: StageTone;
  /** Spec stage only: render the Generate-spec CTA instead of a state. */
  cta?: boolean;
}

export function specStageView(status: ProjectStatus): StageView {
  const { exists, version, dirty } = status.spec;
  if (!exists) return { version: "", line: "", tone: "neutral", cta: true };
  if (!version) return { version: "", line: "draft · not published", tone: "info" };
  if (dirty) return { version: `${version}+`, line: "draft changes", tone: "warning" };
  return { version, line: "published", tone: "success" };
}

// The build stage is COUNT-FREE. A per-version task tally can only come from
// the version's milestone on GitHub, and this aggregate is polled at 5s, so the
// status read does not carry one — the Builds page renders counts from the
// issue list it already pays for. What the overview says instead is what the
// version's run is doing, which is a run row and costs nothing.
export function buildStageView(status: ProjectStatus): StageView {
  const { version, status: state } = status.build;
  switch (state) {
    case "running":
      return { version, line: "building", tone: "info" };
    case "failed":
      return { version, line: "build failed", tone: "error" };
    case "succeeded":
      return { version, line: "built", tone: "success" };
    default:
      return { version: "", line: "waiting on spec", tone: "ghost" };
  }
}

export function deployStageView(status: ProjectStatus): StageView {
  const { version, status: state, components: comps } = status.deploy;
  switch (state) {
    case "deploying":
      return {
        version,
        line: `deploying · ${comps.ready}/${comps.total} components`,
        tone: "info",
      };
    case "deployed": {
      // Validation runs after the components deploy, so its state is appended to
      // the live-in-dev line (deployed stays the deploy tone; validation is
      // informational text here — the deployments board carries the loud chip).
      const v = validationView(status.deploy.validation);
      return {
        version,
        line: v ? `live in dev · ${v.label}` : "live in dev",
        tone: "success",
      };
    }
    case "failed":
      return { version, line: "deploy failed", tone: "error" };
    default: {
      // No live deploy status — but validation only runs after the app
      // deploys, so a non-none validation state means it IS live in dev.
      // Surface it even when the binding read lags or returns nothing (a
      // transient/degraded deploy-status read must not hide validation).
      const v = validationView(status.deploy.validation);
      if (v) return { version, line: `live in dev · ${v.label}`, tone: v.tone };
      return { version: "", line: "nothing deployed", tone: "ghost" };
    }
  }
}

// validationView maps the coarse deploy.validation state to a label + tone,
// shared by the overview deploy line (suffix) and the deployments board chip.
// null = nothing to show (validation never reached, skipped, or no criteria).
//
// The state is folded from the RUN's verdict — the verdict is a run property,
// and the run rows are where the platform keeps it — so unlike the old
// task-derived state these labels can name the outcome instead of naming the
// artifact the user would have to open to find it out.
export function validationView(
  validation: string,
): { label: string; tone: StageTone } | null {
  switch (validation) {
    case "running":
      return { label: "validating", tone: "info" };
    case "completed":
      return { label: "validation passed", tone: "success" };
    case "failed":
      return { label: "validation failed", tone: "error" };
    default: // "none" | "" | unknown
      return null;
  }
}
