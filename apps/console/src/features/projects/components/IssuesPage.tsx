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

import { Link } from "@tanstack/react-router";
import { CircleAlert } from "@wso2/oxygen-ui-icons-react";
import { EmptyState } from "../../../components/EmptyState";
import { PageHeader } from "../../../components/PageHeader";
import { useProject, useProjectStatus } from "../api/queries";
import { projectChip } from "../lib/projectChip";

// Placeholder by decision (#173): Issues is the future surface for issues
// the SRE agent raises against the running project; its own feature will
// land the content. It gets the same PageHeader every other project
// sub-page does (Task 5) — the EmptyState body keeps just the icon, title,
// and description; the one-off orange "Back to overview" action it used to
// carry is now the shared back link in the header.
export function IssuesPage({ projectName }: { projectName: string }) {
  const project = useProject(projectName);
  const status = useProjectStatus(projectName);

  return (
    <>
      <PageHeader
        title="Issues"
        {...(project.data && {
          subtitle: project.data.displayName ?? project.data.name,
        })}
        {...(status.data && { status: projectChip(status.data) })}
        backTo={{
          link: <Link to="/projects/$projectName" params={{ projectName }} />,
          label: "Back to Overview",
        }}
      />
      <EmptyState
        icon={<CircleAlert size={48} />}
        title="Issues is on its way"
        description="Issues the SRE agent raises against the running project will land here — triage them and follow their fixes."
      />
    </>
  );
}
