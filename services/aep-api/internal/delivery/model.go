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
	"context"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
)

// TaskFacts is a Task's structured truth resolved live from its GitHub issue at
// the moment of dispatch (§2, §5 "re-verified against the design at HEAD at the
// moment of use"). The funnel reads the issue, parses its machine block, and
// hands these to an executor — the executor never re-reads GitHub.
type TaskFacts struct {
	OrgID       string
	ProjectID   string
	Repo        string // "owner/name"
	IssueNumber int
	IssueURL    string
	Title       string

	Class      taskmeta.ExecutorClass
	Component  string // coding tasks; "" for ops
	Operation  string // ops tasks; "" for coding
	DependsOn  []string
	Origin     taskmeta.Origin
	SpecTag    string
	DesignTag  string
	IssueOpen  bool
	HoldActive bool
}

// DispatchRequest is what the funnel hands an Executor: the admitted execution
// row (the executor Starts it and the watchers Finish it — §7) plus the Task
// facts snapshotted at dispatch. An Execution snapshots its Task at dispatch;
// later edits to the issue affect only future Executions (§4).
//
// MergeSHA is set only for a build Execution (Execution.Kind == build), spawned
// when a linked PR merges: the commit the build is pinned to (§7).
type DispatchRequest struct {
	Execution *Execution
	Task      TaskFacts
	MergeSHA  string
}

// Executor performs one dispatch attempt for a class of Task (§3, §11). The
// coding executor (feature/codingagent) is the one wired executor in v1; the
// ops executor is a later registry entry. Run transitions the execution row to
// running and launches the outbound work; the watchers/webhooks Finish the row
// on completion. A launch failure returns an error (the funnel Finishes the row
// failed and flags attention).
type Executor interface {
	Run(ctx context.Context, req DispatchRequest) error
}
