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

import { createFileRoute } from "@tanstack/react-router";
import { SpecView } from "../features/spec/components/SpecView";

// `$projectName_` (trailing underscore) un-nests this route from the
// /projects/$projectName layout: the spec view is a full-screen workspace
// without the shared project header (#80).
//
// `?generate=requirements|design` (#150/#159): arriving from a "Generate spec"
// or "Generate/Re-generate design" CTA — AppLayout opens the agent panel and
// auto-sends the matching generation turn.
//
// `?connections=open`: arriving from the Builds page's gate hold banner — a
// dispatch gate is holding the run and the connection drawer is where its
// dependency is supplied, so the drawer opens on arrival.
export const Route = createFileRoute("/projects/$projectName_/spec")({
  validateSearch: (
    search: Record<string, unknown>,
  ): { generate?: "requirements" | "design"; connections?: "open" } => ({
    ...(search.generate === "requirements" || search.generate === "design"
      ? { generate: search.generate }
      : {}),
    ...(search.connections === "open" ? { connections: "open" as const } : {}),
  }),
  component: SpecRoute,
});

function SpecRoute() {
  const { projectName } = Route.useParams();
  return <SpecView projectName={projectName} />;
}
