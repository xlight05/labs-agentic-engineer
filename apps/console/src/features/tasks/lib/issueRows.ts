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
  /** Dispatch gates — the connections a version's components need before they
   *  can deploy. Worked by the platform rather than by the agent, and closed
   *  without a pull request, but a real part of the version's record. */
  gates: TaskView[];
  /** Bare human issues that joined the milestone — never worked, never
   *  stalling settle. Their own section, so they are not read as tasks. */
  ledger: TaskView[];
}

/**
 * Partition one version's issues.
 *
 * All three populations keep their CLOSED members — that is the version's
 * record of what got done, and a provisioned connection is as much a part of
 * how a version came to exist as a merged pull request. Only the run card
 * narrows further, to the open gates, because only an open gate holds
 * anything; see `openGates`.
 */
export function partitionIssues(tasks: TaskView[]): IssuePartition {
  const partition: IssuePartition = { work: [], gates: [], ledger: [] };
  for (const task of tasks) {
    switch (task.executorClass) {
      case "provision":
        partition.gates.push(task);
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
 * The gates that are still OPEN — the only ones that hold dispatch, and so the
 * only ones the run card's hold notice may speak for. A resolved gate is
 * history: it belongs in the version's issue list, not in an explanation of why
 * nothing is moving.
 */
export function openGates(gates: TaskView[]): TaskView[] {
  return gates.filter((gate) => gate.derivedStatus === "pending");
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
