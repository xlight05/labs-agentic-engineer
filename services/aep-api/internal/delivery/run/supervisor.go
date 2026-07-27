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

package run

import (
	"context"
	"errors"
	"log/slog"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// signalTimeout bounds one SignalWorkflow call so a slow Temporal server cannot
// tie up the webhook handler that is telling the run something. Signalling is
// best-effort by contract — the supervisor re-derives from ground truth at
// every boundary — so a timeout is logged, never propagated.
const signalTimeout = 5 * time.Second

// Supervisor is the outside world's handle on milestone runs: start one, tell
// one something, cancel one.
//
// It is a nil-safe CONCRETE TYPE rather than an interface, exactly like the
// task-keyed Signaler it sits beside. That nil-safety is load-bearing: the
// event plane and the build click both hold it unconditionally, and a degraded
// boot (Temporal down, no agent dispatcher) must make every call a logged no-op
// rather than something callers have to nil-check.
type Supervisor struct {
	rt   *delivery.Runtime
	runs RunStore
	// dispatcher is held only to answer "can a run do anything if I start one?".
	// The workflow reaches it through its own activity; this reference is the
	// admission check.
	dispatcher delivery.MilestoneDispatcher
}

// NewSupervisor wires the supervisor. A nil dispatcher is a documented state,
// not a bug: it means the coding agent cannot yet be handed a milestone, and
// starting a run that could never dispatch would burn the version's run row on
// a loop with no way forward. StartRun refuses instead, leaving the row waiting
// for the reconcile sweep to re-offer once the dispatcher is wired.
func NewSupervisor(rt *delivery.Runtime, runs RunStore, dispatcher delivery.MilestoneDispatcher) *Supervisor {
	return &Supervisor{rt: rt, runs: runs, dispatcher: dispatcher}
}

// StartRun admits the run row when the caller has not already, then starts the
// supervisor over its milestone.
//
// Idempotent by construction, because both of its callers re-offer the same
// milestone: adoption fires on every `aep:codingagent` label, and the reconcile
// sweep re-offers every pass until a run is live. A milestone that already has
// a live run reuses its row, and a workflow that is already running answers
// AlreadyStarted, which is success.
func (s *Supervisor) StartRun(ctx context.Context, req delivery.StartRunRequest) error {
	if s == nil {
		return nil
	}
	if s.dispatcher == nil {
		slog.WarnContext(ctx, "run: no agent dispatcher wired — the run row waits",
			"project", req.ProjectID, "milestone", req.MilestoneNumber, "origin", req.Origin)
		return nil
	}
	if s.rt == nil || !s.rt.Available() {
		slog.WarnContext(ctx, "run: temporal unavailable — the run row waits for the reconcile sweep",
			"project", req.ProjectID, "milestone", req.MilestoneNumber)
		return nil
	}

	row, err := s.admit(ctx, req)
	if err != nil {
		return err
	}
	if row == nil {
		return nil // the mutex refused it; the winner is already supervised
	}

	c, err := s.rt.Client()
	if err != nil {
		return nil // raced with a client teardown — best-effort, the sweep re-offers
	}
	_, err = c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        delivery.MilestoneRunWorkflowID(row.OrgID, row.ProjectID, row.MilestoneNumber),
		TaskQueue: s.rt.TaskQueue(),
		// A milestone sees SEQUENTIAL runs across its life, so the id is reused
		// once the previous run is terminal. Concurrency is prevented by the run
		// row (the spec mutex) and by AlreadyStarted below, not by the id.
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	}, delivery.MilestoneRunWorkflowName, RunInput{
		RunID:           row.ID,
		OrgID:           row.OrgID,
		ProjectID:       row.ProjectID,
		MilestoneNumber: row.MilestoneNumber,
		MilestoneTitle:  row.MilestoneTitle,
		Origin:          row.Origin,
		CycleCeiling:    row.CycleCeiling,
	})
	var already *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &already) {
		slog.DebugContext(ctx, "run: already supervised", "run", row.ID, "milestone", row.MilestoneNumber)
		return nil
	}
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "run: supervising a milestone",
		"run", row.ID, "project", row.ProjectID, "milestone", row.MilestoneNumber, "origin", row.Origin)
	return nil
}

// admit resolves the run row this request supervises: the caller's own (the
// plan path admits before planning, so the spec mutex is armed across the
// planning turn), the milestone's existing live run, or a fresh incident row.
//
// It returns (nil, nil) when the mutex refused a new row — somebody else won,
// and they are the one being supervised.
func (s *Supervisor) admit(ctx context.Context, req delivery.StartRunRequest) (*delivery.MilestoneRun, error) {
	if req.RunID != "" {
		return &delivery.MilestoneRun{
			ID:              req.RunID,
			OrgID:           req.OrgID,
			ProjectID:       req.ProjectID,
			MilestoneNumber: req.MilestoneNumber,
			MilestoneTitle:  req.MilestoneTitle,
			Origin:          req.Origin,
			CycleCeiling:    delivery.RunDefaultCycleCeiling,
		}, nil
	}
	if s.runs == nil {
		return nil, errNotConfigured
	}
	live, err := s.runs.LiveRunForMilestone(ctx, req.OrgID, req.ProjectID, req.MilestoneNumber)
	if err != nil {
		return nil, err
	}
	if live != nil {
		// A row exists but may have no workflow behind it (a crash between admit
		// and start). Returning it re-starts the supervisor over the same row,
		// which is precisely the reconcile sweep's job.
		return live, nil
	}
	admitted, row, err := s.runs.TryAdmit(ctx, &delivery.MilestoneRun{
		OrgID:           req.OrgID,
		ProjectID:       req.ProjectID,
		MilestoneNumber: req.MilestoneNumber,
		MilestoneTitle:  req.MilestoneTitle,
		Origin:          req.Origin,
		State:           delivery.RunStateWaiting,
	})
	if err != nil || !admitted {
		return nil, err
	}
	return row, nil
}

// SignalRun tells a live run a fact the event plane observed.
//
// This is the milestone-keyed twin of the task-keyed Signaler: it addresses the
// workflow by (org, project, milestone) directly, with no lookup table, because
// the run's identity IS its milestone. Best-effort — a run that is not there
// re-derives everything it missed at its next boundary.
func (s *Supervisor) SignalRun(ctx context.Context, row *delivery.MilestoneRun, name string, payload delivery.RunSignal) error {
	if s == nil || row == nil || s.rt == nil || !s.rt.Available() {
		return nil
	}
	return s.signal(ctx, row.OrgID, row.ProjectID, row.MilestoneNumber, name, payload)
}

// CancelRun abandons an increment. It is the write behind the console's cancel,
// and the only expiry the unbounded wait state has.
//
// Cancel is a SIGNAL, not a Temporal cancellation, so the run settles its own
// row and closes its own cycle on the ordinary code path — a cancelled context
// could not run the activities that record the outcome.
func (s *Supervisor) CancelRun(ctx context.Context, row *delivery.MilestoneRun) error {
	if s == nil || row == nil {
		return nil
	}
	if s.rt == nil || !s.rt.Available() {
		return delivery.ErrTemporalUnavailable
	}
	return s.signal(ctx, row.OrgID, row.ProjectID, row.MilestoneNumber, delivery.SigRunCancel, delivery.RunSignal{
		Signal:          delivery.SigRunCancel,
		MilestoneNumber: row.MilestoneNumber,
	})
}

func (s *Supervisor) signal(ctx context.Context, orgID, projectID string, milestoneNumber int, name string, payload delivery.RunSignal) error {
	c, err := s.rt.Client()
	if err != nil {
		return nil
	}
	workflowID := delivery.MilestoneRunWorkflowID(orgID, projectID, milestoneNumber)
	ctx, cancel := context.WithTimeout(ctx, signalTimeout)
	defer cancel()
	if serr := c.SignalWorkflow(ctx, workflowID, "", name, payload); serr != nil {
		var notFound *serviceerror.NotFound
		if errors.As(serr, &notFound) {
			slog.DebugContext(ctx, "run: no live supervisor for this milestone",
				"workflowId", workflowID, "signal", name)
			return nil
		}
		slog.WarnContext(ctx, "run: signal failed", "workflowId", workflowID, "signal", name, "error", serr)
	}
	return nil
}
