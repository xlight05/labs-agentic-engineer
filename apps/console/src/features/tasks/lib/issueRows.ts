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

type TaskView = components["schemas"]["TaskView"];

/**
 * A version's issues, split the three ways the Builds page renders them.
 *
 * The split is entirely label-derived (`executorClass`) — the platform makes no
 * other classification of an issue, and nothing here parses a body.
 */
export interface IssuePartition {
  /** Agent work: the run's working set, and the run's history once closed. */
  work: TaskView[];
  /** OPEN dispatch gates. These hold the loop, so they are a banner, not rows. */
  gates: TaskView[];
  /** Bare human issues that joined the milestone — never worked, never
   *  stalling settle. Their own section, so they are not read as tasks. */
  ledger: TaskView[];
}

/**
 * Partition one version's issues.
 *
 * A CLOSED gate is dropped entirely: a resolved gate holds nothing, and it was
 * never work, so it belongs neither to the banner nor to the history the work
 * list tells. The two other populations keep their closed members — that is
 * the version's record of what got done.
 */
export function partitionIssues(tasks: TaskView[]): IssuePartition {
  const partition: IssuePartition = { work: [], gates: [], ledger: [] };
  for (const task of tasks) {
    switch (task.executorClass) {
      case "provision":
        if (task.derivedStatus === "pending") partition.gates.push(task);
        break;
      case "ledger":
        partition.ledger.push(task);
        break;
      default:
        // `coding`, and anything a future label kind adds: agent work until
        // the platform says otherwise. The validation issue never reaches the
        // console here — the list read hides it.
        partition.work.push(task);
    }
  }
  return partition;
}

/**
 * The dependency a gate is holding on, taken from its title. Gate titles are
 * platform-authored prose ("Provide configuration: <dep>"), so the tail after
 * the colon is the dependency's name; a title without one falls back whole.
 * This is presentation, not parsing for meaning — the platform never reads a
 * gate back to decide anything.
 */
export function gateSubject(title: string): string {
  const colon = title.indexOf(":");
  return colon === -1 ? title : title.slice(colon + 1).trim();
}
