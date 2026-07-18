// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package spec

import (
	"encoding/json"

	"github.com/wso2/aep/aep-api/repositories"
)

// Turn vocabulary. The agent_turns ORM lives in repositories/ (the gorm fence),
// but the turn engine — the runner, the sweeper, the service — speaks these
// terms, so they re-export here: the terminal shape and the one-active-guard
// sentinel are aliases (same type across the seam), and the status/reason
// strings are the domain's own names for the values the store persists.

// TurnRepository is the agent_turns row store (design D17/D18).
type TurnRepository = repositories.TurnRepository

// TurnTerminal is the terminal state Finish stamps onto a running row.
type TurnTerminal = repositories.TurnTerminal

// ErrTurnActive marks the D18 one-active-turn-per-project guard.
var ErrTurnActive = repositories.ErrTurnActive

// Turn statuses (models.AgentTurn.Status).
const (
	turnStatusRunning   = "running"
	turnStatusCompleted = "completed"
	turnStatusFailed    = "failed"
)

// Failure reasons (models.AgentTurn.Reason and the terminal event's `reason`).
const (
	turnReasonStreamDied     = "stream-died"
	turnReasonFoldParity     = "fold-parity"
	turnReasonBaseMoved      = "base-moved"
	turnReasonDispatchFailed = "dispatch-failed"
	turnReasonInternal       = "internal"
)

// decodePaths reads the stored conflicting-path JSON array back (nil for
// empty/invalid) for the status projection.
func decodePaths(raw string) []string {
	if raw == "" {
		return nil
	}
	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		return nil
	}
	return paths
}
