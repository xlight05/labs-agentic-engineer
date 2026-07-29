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

import { Box, Button } from "@wso2/oxygen-ui";
import { createLink } from "@tanstack/react-router";
import { ArrowRight } from "@wso2/oxygen-ui-icons-react";
import type { StageState } from "../lib/stage";

const LinkButton = createLink(Button);

/**
 * DEPLOYMENT — the honest end of a build session.
 *
 * It reads NOTHING from the cluster, on purpose. A Deployment carries no commit,
 * image or revision, so no rollout can be attributed to the merge that caused
 * it: per-component chips here would show a later session's rollout underneath
 * an earlier session, and after two sessions on one run every session would
 * claim the same one. What this stage owns is the CONSEQUENCE — components carry
 * auto-deploy, so a green build deploys itself — and the way to the board that
 * does know what is running.
 *
 * The sentence itself lives on the stage (see lib/sessionSpine.ts); this is only
 * the way out, and only once the session has caused something worth looking at.
 */
export function DeploymentStage({
  projectName,
  state,
}: {
  projectName: string;
  state: StageState;
}) {
  // Before a merge there is nothing deployed to go and look at, so the stage
  // says what it waits for and offers nothing to click.
  if (state === "waiting") return null;
  return (
    <Box>
      <LinkButton
        size="small"
        variant="outlined"
        endIcon={<ArrowRight size={14} />}
        to="/projects/$projectName/deployments"
        params={{ projectName }}
      >
        View deployments
      </LinkButton>
    </Box>
  );
}
