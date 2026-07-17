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

package ops

import (
	"context"
	"time"
)

// The ports ops needs FROM other domains, declared in ops' OWN vocabulary
// (§4: "all edges are consumer-side ports wired at the root").
//
// Stating them consumer-side is what lets this domain land in P1, years before
// its provider: delivery does not exist until P6, so the composition root wires
// a labelled bridge over the legacy execution repository. When delivery lands it
// implements this port directly and the bridge is deleted — and because the port
// is written in ops' terms, that is a change to the bridge alone, not to ops.

// ExecutionFact is one execution of a Task, reduced to the three things ops
// needs to correlate a report. It is deliberately NOT the Execution entity:
// borrowing the provider's model is how a "port" quietly becomes a shared table.
type ExecutionFact struct {
	// Kind is the platform/taskmeta execution kind (coding, build, …).
	Kind string
	// Status is the platform/taskmeta execution status.
	Status string
	// EndedAt is when the execution finished, nil while it is still running.
	EndedAt *time.Time
}

// ExecutionReader returns the latest execution per kind for one Task, fenced to
// an org. It lets GetReport reconcile a report's Dispatched/Deployed snapshot
// against live executions (the same source the Build/Task view reads): a report
// is written once at RCA time, so its snapshot goes stale as a coding agent is
// later dispatched and the fix is built and deployed.
//
// Optional — a nil reader disables correlation and the stored snapshot is served
// as-is, which is the honest degradation when the provider is absent.
type ExecutionReader interface {
	LatestExecutionPerKind(ctx context.Context, orgID, repo string, issueNumber int) (map[string]ExecutionFact, error)
}
