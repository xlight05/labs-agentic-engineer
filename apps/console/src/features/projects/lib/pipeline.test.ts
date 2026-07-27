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

import { describe, expect, it } from "vitest";
import type { components } from "../../../generated/aep-api";
import {
  buildStageView,
  deployStageView,
  specStageView,
  validationView,
} from "./pipeline";

type ProjectStatus = components["schemas"]["ProjectStatus"];

function status(over: {
  spec?: Partial<ProjectStatus["spec"]>;
  build?: Partial<ProjectStatus["build"]>;
  deploy?: Partial<ProjectStatus["deploy"]>;
}): ProjectStatus {
  return {
    phase: "spec",
    repoStatus: "ready",
    repoUrl: "",
    hasSpec: true,
    hasDesign: false,
    hasTasks: false,
    specStatus: "",
    designStatus: "",
    spec: { exists: true, version: "", dirty: false, design: false, ...over.spec },
    build: { version: "", status: "idle", ...over.build },
    deploy: {
      version: "",
      status: "none",
      components: { total: 0, ready: 0 },
      validation: "none",
      ...over.deploy,
    },
  };
}

describe("specStageView — derived, no stored status", () => {
  it("no spec at all → the Generate CTA", () => {
    expect(specStageView(status({ spec: { exists: false } })).cta).toBe(true);
  });
  it("exists but never published → draft", () => {
    const v = specStageView(status({}));
    expect(v.line).toContain("draft");
    expect(v.version).toBe("");
  });
  it("published and clean → vN approved", () => {
    const v = specStageView(status({ spec: { version: "v2" } }));
    expect(v.version).toBe("v2");
    expect(v.tone).toBe("success");
  });
  it("published and dirty → vN+", () => {
    const v = specStageView(status({ spec: { version: "v2", dirty: true } }));
    expect(v.version).toBe("v2+");
    expect(v.tone).toBe("warning");
  });
});

// The build stage is deliberately count-free: a per-version task tally can only
// come from GitHub and this aggregate is polled at 5s, so it carries the run's
// state and its version and nothing else.
describe("buildStageView", () => {
  it("idle → ghosted waiting, no version claimed", () => {
    const v = buildStageView(status({}));
    expect(v.tone).toBe("ghost");
    expect(v.version).toBe("");
  });
  it("running → the version being built", () => {
    const v = buildStageView(
      status({ build: { version: "v1", status: "running" } }),
    );
    expect(v.line).toBe("building");
    expect(v.tone).toBe("info");
    expect(v.version).toBe("v1");
  });
  it("failed → error tone", () => {
    const v = buildStageView(
      status({ build: { version: "v1", status: "failed" } }),
    );
    expect(v.tone).toBe("error");
    expect(v.line).toBe("build failed");
  });
  it("succeeded → success", () => {
    const v = buildStageView(
      status({ build: { version: "v1", status: "succeeded" } }),
    );
    expect(v.tone).toBe("success");
    expect(v.line).toBe("built");
  });
});

describe("deployStageView", () => {
  it("none → ghosted", () => {
    expect(deployStageView(status({})).tone).toBe("ghost");
  });
  it("deploying → component rollout progress", () => {
    const v = deployStageView(
      status({
        deploy: {
          version: "v1",
          status: "deploying",
          components: { total: 5, ready: 3 },
        },
      }),
    );
    expect(v.line).toBe("deploying · 3/5 components");
  });
  it("deployed with no validation → plain live in dev", () => {
    const v = deployStageView(
      status({
        deploy: {
          version: "v1",
          status: "deployed",
          components: { total: 5, ready: 5 },
        },
      }),
    );
    expect(v.tone).toBe("success");
    expect(v.version).toBe("v1");
    expect(v.line).toBe("live in dev");
  });
  it("deployed while validating → appends the validation state", () => {
    const v = deployStageView(
      status({
        deploy: {
          version: "v1",
          status: "deployed",
          components: { total: 5, ready: 5 },
          validation: "running",
        },
      }),
    );
    expect(v.line).toBe("live in dev · validating");
  });
  it("deployed and validation ran → appends validation complete", () => {
    const v = deployStageView(
      status({
        deploy: {
          version: "v1",
          status: "deployed",
          components: { total: 5, ready: 5 },
          validation: "completed",
        },
      }),
    );
    expect(v.line).toBe("live in dev · validation passed");
  });
  it("failed → error", () => {
    const v = deployStageView(
      status({
        deploy: {
          version: "v1",
          status: "failed",
          components: { total: 5, ready: 1 },
        },
      }),
    );
    expect(v.tone).toBe("error");
  });
  it("status none but validation ran → still surfaces live-in-dev + validation", () => {
    // Validation runs only post-deploy, so a non-none validation means the app
    // is live even if the deploy-status read lagged to "none".
    const v = deployStageView(
      status({
        deploy: {
          version: "v1",
          status: "none",
          components: { total: 1, ready: 0 },
          validation: "completed",
        },
      }),
    );
    expect(v.line).toBe("live in dev · validation passed");
    expect(v.tone).toBe("success");
  });
  it("status none and no validation → nothing deployed", () => {
    const v = deployStageView(status({}));
    expect(v.line).toBe("nothing deployed");
    expect(v.tone).toBe("ghost");
  });
});

describe("validationView", () => {
  it("none / empty / unknown → null (nothing to show)", () => {
    expect(validationView("none")).toBeNull();
    expect(validationView("")).toBeNull();
    expect(validationView("bogus")).toBeNull();
  });
  it("running → validating (info)", () => {
    expect(validationView("running")).toEqual({
      label: "validating",
      tone: "info",
    });
  });
  // The state is folded from the RUN's verdict, so the label can name the
  // outcome rather than name the artifact the user would have to open.
  it("completed → validation passed (success)", () => {
    expect(validationView("completed")).toEqual({
      label: "validation passed",
      tone: "success",
    });
  });
  it("failed → validation failed (error)", () => {
    expect(validationView("failed")).toEqual({
      label: "validation failed",
      tone: "error",
    });
  });
});
