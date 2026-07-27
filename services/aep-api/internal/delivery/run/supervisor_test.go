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
	"testing"

	"github.com/wso2/aep/aep-api/internal/config"
	"github.com/wso2/aep/aep-api/internal/delivery"
)

// configWithNoTemporal is a runtime that will never connect — the degraded boot
// every nil-safety assertion below is about.
func configWithNoTemporal() config.TemporalConfig { return config.TemporalConfig{} }

// fakeDispatcher stands in for the coding agent. Its only job in these tests is
// to be non-nil.
type fakeDispatcher struct{}

func (fakeDispatcher) Dispatch(context.Context, delivery.MilestoneDispatch) (string, error) {
	return "job-1", nil
}

// countingRuns records whether the start path reached the run store at all.
type countingRuns struct {
	RunStore
	admits int
	lives  int
}

func (r *countingRuns) TryAdmit(context.Context, *delivery.MilestoneRun) (bool, *delivery.MilestoneRun, error) {
	r.admits++
	return false, nil, errors.New("countingRuns: unexpected admit")
}

func (r *countingRuns) LiveRunForMilestone(context.Context, string, string, int) (*delivery.MilestoneRun, error) {
	r.lives++
	return nil, nil
}

// TestSupervisorIsNilSafe pins the property the event plane and the build click
// both depend on: they hold the supervisor unconditionally, so every verb on a
// nil one is a no-op rather than a panic. It is the same guarantee the
// task-keyed Signaler makes, and it is what keeps a degraded boot from needing
// a nil check at each call site.
func TestSupervisorIsNilSafe(t *testing.T) {
	var s *Supervisor
	if err := s.StartRun(context.Background(), delivery.StartRunRequest{}); err != nil {
		t.Fatalf("StartRun on a nil supervisor: %v", err)
	}
	if err := s.SignalRun(context.Background(), &delivery.MilestoneRun{}, delivery.SigRunWorkable, delivery.RunSignal{}); err != nil {
		t.Fatalf("SignalRun on a nil supervisor: %v", err)
	}
	if err := s.CancelRun(context.Background(), &delivery.MilestoneRun{}); err != nil {
		t.Fatalf("CancelRun on a nil supervisor: %v", err)
	}
}

// TestStartRunRefusesWithoutADispatcher: a run that could dispatch nothing must
// not be started, because starting it would burn the version's run row on a
// loop with no way forward. The row stays waiting and the reconcile sweep
// re-offers the milestone once the coding agent can be handed one.
func TestStartRunRefusesWithoutADispatcher(t *testing.T) {
	runs := &countingRuns{}
	s := NewSupervisor(delivery.NewRuntime(configWithNoTemporal()), runs, nil)
	if err := s.StartRun(context.Background(), delivery.StartRunRequest{
		OrgID: testOrg, ProjectID: testProject, MilestoneNumber: testMilepost,
		Origin: delivery.RunOriginIncidentAdoption,
	}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if runs.admits != 0 || runs.lives != 0 {
		t.Fatalf("an unwired dispatcher must not touch the run store (admits=%d lives=%d)", runs.admits, runs.lives)
	}
}

// TestStartRunWaitsForTemporal: with a dispatcher but no connected Temporal
// client, the start is a logged no-op — aep-api boots and serves everything
// else while the workflow engine is down, and the sweep re-offers.
func TestStartRunWaitsForTemporal(t *testing.T) {
	runs := &countingRuns{}
	s := NewSupervisor(delivery.NewRuntime(configWithNoTemporal()), runs, fakeDispatcher{})
	if err := s.StartRun(context.Background(), delivery.StartRunRequest{
		OrgID: testOrg, ProjectID: testProject, MilestoneNumber: testMilepost,
		Origin: delivery.RunOriginIncidentAdoption,
	}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if runs.admits != 0 {
		t.Fatalf("no run row may be admitted while the engine that would drive it is down")
	}
}

// TestSignalAndCancelAreInertWhileTemporalIsDown pins the best-effort contract:
// a webhook handler must never fail because the workflow engine is unreachable.
// Cancel is the exception — it is a human's instruction, so it reports honestly
// that it could not be delivered.
func TestSignalAndCancelAreInertWhileTemporalIsDown(t *testing.T) {
	s := NewSupervisor(delivery.NewRuntime(configWithNoTemporal()), &countingRuns{}, fakeDispatcher{})
	row := &delivery.MilestoneRun{ID: testRunID, OrgID: testOrg, ProjectID: testProject, MilestoneNumber: testMilepost}
	if err := s.SignalRun(context.Background(), row, delivery.SigRunWorkable, delivery.RunSignal{}); err != nil {
		t.Fatalf("SignalRun must never fail a webhook: %v", err)
	}
	if err := s.CancelRun(context.Background(), row); !errors.Is(err, delivery.ErrTemporalUnavailable) {
		t.Fatalf("CancelRun error = %v, want ErrTemporalUnavailable", err)
	}
}

// TestMilestoneRunWorkflowID pins the identity §7 names, which is also the id
// the event plane's signals and the console's cancel both address.
func TestMilestoneRunWorkflowID(t *testing.T) {
	if got := delivery.MilestoneRunWorkflowID("acme", "shop", 7); got != "run-acme-shop-7" {
		t.Fatalf("MilestoneRunWorkflowID = %q, want run-acme-shop-7", got)
	}
}
