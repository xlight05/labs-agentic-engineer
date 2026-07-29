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
import { gateDrive } from "./runView";
import { loudestState, type SpineStage, type StageState } from "./stage";

type TaskView = components["schemas"]["TaskView"];

// PROVISIONING — the run's connections, as the first stage on its rail.
//
// It belongs to the RUN and not to a build session, because that is what the
// platform's own dispatch predicate says: no session dispatches while any
// provision gate is open, so an open gate holds every session, not the first
// one. A gate minted mid-run (a dependency discovered while work proceeds) has
// nowhere to live on a session's spine, and a fix session would have to either
// repeat the stage or pretend the gate was not there.
//
// This section is also where the run's gate hold went. It used to be a notice at
// the top of the card, several sections away from the sequence it was blocking;
// the answer to "why is nothing moving" belongs at the point where movement
// stopped.

/** One connection, and who has to act on it. */
export interface GateRow {
  gate: TaskView;
  state: StageState;
  /** Two or three words for the chip: WHO is acting, not how it is going. */
  label: string;
}

/** Is this gate resolved? A closed gate holds nothing and is pure record. */
function isOpen(gate: TaskView): boolean {
  return gate.derivedStatus === "pending";
}

/**
 * Each gate's own state.
 *
 * The three open cases are genuinely different and conflating them is what made
 * a healthy build announce itself as parked: the platform standing a database up
 * needs nothing from anyone, a failed provisioning run needs a correction, and a
 * gate NOTHING is driving is the only one that is actually waiting on a person.
 * `gateDrive` is where that distinction is read from — the gate's provisioning
 * Execution, which every path that resolves a gate admits before it acts.
 */
export function gateRows(gates: TaskView[]): GateRow[] {
  return gates.map((gate) => {
    if (!isOpen(gate)) {
      return { gate, state: "done" as StageState, label: "provisioned" };
    }
    switch (gateDrive(gate)) {
      case "provisioning":
        return { gate, state: "active" as StageState, label: "provisioning" };
      case "failed":
        return { gate, state: "failed" as StageState, label: "failed" };
      default:
        // The one case a person has to act on: no provisioning run has ever been
        // admitted against this gate, so nothing is driving it.
        return { gate, state: "attention" as StageState, label: "needs you" };
    }
  });
}

const NOTE_BY_STATE: Record<StageState, string> = {
  failed: "A connection failed to provision. Correct it and build again — no build session dispatches while a gate is open.",
  attention: "No build session dispatches while a connection is unresolved. Supply the configuration and the rest of the milestone is released.",
  active: "The platform is standing these up and closes each gate itself — nothing is held on you. A database or an identity app takes a few minutes.",
  done: "Every connection this version needs is provisioned.",
  waiting: "",
};

/**
 * The provisioning stage, or null when this version needs no connections at all
 * — in which case the section is absent rather than reassuring, because there is
 * nothing for it to reassure about.
 */
export function provisioningStage(gates: TaskView[]): SpineStage | null {
  if (gates.length === 0) return null;
  const rows = gateRows(gates);
  const state = loudestState(rows.map((row) => row.state));
  const open = rows.filter((row) => row.state !== "done").length;
  return {
    id: "provision",
    name: "Provisioning",
    actor: "platform",
    state,
    note: NOTE_BY_STATE[state],
    fact: open === 0 ? `${gates.length} resolved` : `${open} of ${gates.length} open`,
  };
}

/** Do any of these gates need a person? That is the only case with a way out. */
export function gatesNeedAction(gates: TaskView[]): boolean {
  return gateRows(gates).some((row) => row.state === "attention" || row.state === "failed");
}
