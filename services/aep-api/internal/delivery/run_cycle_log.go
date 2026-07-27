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

package delivery

import (
	"time"

	"github.com/google/uuid"
)

// RunCycleLog is the captured-on-terminal snapshot of one CYCLE's agent pod
// stdout — the milestone loop's twin of CodingAgentLog.
//
// It is a separate table rather than a re-key of coding_agent_logs because that
// table is FK'd to `executions(id)` and a milestone run mints no execution row:
// a cycle's log has no execution to hang off. Keying it to the cycle is what
// makes the run's progress stream serveable after the pod is gone — without it a
// finished run would have no history at all, since the Job's 24h TTL reaps the
// pod long before anyone opens an old version's page.
//
// One row per `(cycle_id, run_name)`: a re-dispatched cycle gets a new Job (and
// therefore a new run name) while keeping the same cycle id, so each attempt
// keeps its own log.
//
// Captured by `internal/delivery/codingagent.JobWatcher` when the cycle's Job
// reaches a terminal phase; read by the run progress stream.
type RunCycleLog struct {
	CycleID    uuid.UUID `gorm:"type:uuid;primaryKey;column:cycle_id" json:"cycleId"`
	RunName    string    `gorm:"type:text;primaryKey;column:run_name" json:"runName"`
	FinalPhase string    `gorm:"type:text;not null;column:final_phase" json:"finalPhase"`
	CapturedAt time.Time `gorm:"type:timestamptz;not null;default:now();column:captured_at" json:"capturedAt"`
	LogText    string    `gorm:"type:text;not null;column:log_text" json:"-"`
	SizeBytes  int64     `gorm:"type:bigint;not null;column:size_bytes" json:"sizeBytes"`
}

// TableName pins the table name so a struct rename cannot silently move the
// table.
func (RunCycleLog) TableName() string { return "run_cycle_logs" }
