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
	"log/slog"
	"time"

	"github.com/wso2/aep/aep-api/models"
)

// signalTimeout bounds a single SignalWorkflow call so a slow Temporal server
// cannot tie up the calling webhook handler / watcher goroutine. Signaling is
// best-effort — a timeout is logged, not propagated.
const signalTimeout = 5 * time.Second

// SignalLookup resolves which running workflow (if any) wants an event.
// Satisfied by repositories.WorkflowRunRepository.
type SignalLookup interface {
	RunningTaskByIssue(ctx context.Context, repo string, issueNumber int) (*models.DevflowRun, error)
}

// Signaler is the bridge existing webhook handlers and watchers use to feed
// events into the devflow task workflows. Every method is BEST-EFFORT: no
// matching workflow (the old-console flow drives a Task with no workflow_runs
// row) is a debug-log no-op, and a signal-send failure is logged, never
// propagated — a webhook handler must not fail because a workflow could not
// be signaled. A nil *Signaler is safe to call (feature disabled), so callers
// hold an optional dependency without nil checks.
type Signaler struct {
	rt     *Runtime
	lookup SignalLookup
}

// NewSignaler wires the signaler. Returns nil-safe methods.
func NewSignaler(rt *Runtime, lookup SignalLookup) *Signaler {
	return &Signaler{rt: rt, lookup: lookup}
}

// SignalTask resolves the running task workflow for (repo, issue) and sends
// it a signal. No running workflow ⇒ no-op.
func (s *Signaler) SignalTask(ctx context.Context, repo string, issue int, name string, payload any) {
	if s == nil || s.rt == nil || !s.rt.Available() {
		return
	}
	row, err := s.lookup.RunningTaskByIssue(ctx, repo, issue)
	if err != nil {
		slog.WarnContext(ctx, "devflow signaler: task lookup failed", "repo", repo, "issue", issue, "error", err)
		return
	}
	if row == nil {
		slog.DebugContext(ctx, "devflow signaler: no running task workflow", "repo", repo, "issue", issue, "signal", name)
		return
	}
	s.send(ctx, row.WorkflowID, name, payload)
}

func (s *Signaler) send(ctx context.Context, workflowID, name string, payload any) {
	c, err := s.rt.Client()
	if err != nil {
		return // not connected — best-effort
	}
	ctx, cancel := context.WithTimeout(ctx, signalTimeout)
	defer cancel()
	if err := c.SignalWorkflow(ctx, workflowID, "", name, payload); err != nil {
		slog.WarnContext(ctx, "devflow signaler: SignalWorkflow failed",
			"workflowId", workflowID, "signal", name, "error", err)
	}
}
