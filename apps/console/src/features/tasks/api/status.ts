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

// An issue row shows DURABLE FACTS ONLY: what GitHub holds about the issue.
// Mid-run liveness lives on the run's cycle timeline and its feed, never on a
// row — a row that animates while the platform has learned nothing new is a
// lie, and the platform learns issue facts only when GitHub tells it.
//
// So `derivedStatus` is a two-value vocabulary now: the issue is open, or it is
// closed. (It keeps two members of the retired ten-value set rather than
// inventing new strings, because it crosses an untyped contract field.) Closed
// reads "Done" because closing is what a merged PR does — close-at-merge is
// landed-on-main.
export interface TaskChip {
  label: string;
  tone: StatusTone;
}

const CHIP_BY_STATUS: Record<string, TaskChip> = {
  pending: { label: "Open", tone: "neutral" },
  merged: { label: "Done", tone: "success" },
};

/** An unknown status renders red with its raw value so nothing hides. */
export function issueStateChip(derivedStatus: string): TaskChip {
  return (
    CHIP_BY_STATUS[derivedStatus] ?? { label: derivedStatus, tone: "error" }
  );
}

// The row's KIND, shown only where it changes how the row should be read.
// A version's issue list is mostly agent work, so tagging every coding row
// "Coding" would be noise; a provisioning gate in the same list is not, because
// it is worked by the PLATFORM rather than by the agent and closes without a
// pull request. Anything else — including a kind this console has not learned —
// renders untagged rather than guessing a label for it.
const CHIP_BY_KIND: Record<string, TaskChip> = {
  provision: { label: "Provisioning", tone: "info" },
};

export function issueKindChip(executorClass: string): TaskChip | null {
  return CHIP_BY_KIND[executorClass] ?? null;
}
