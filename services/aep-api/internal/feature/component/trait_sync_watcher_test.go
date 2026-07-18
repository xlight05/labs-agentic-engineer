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

package component

// UNIT tier: the TraitSyncWatcher's per-tuple retry budget is pure in-memory
// state (a map + mutex — no DB, no clock injection), so it is proven here by
// exercising the state machine directly and by driving reconcileOne against a
// deliberately-failing sync. The DB-shaped half — the DISTINCT tuple query the
// sweep runs over component_tasks — is proven against real Postgres in
// component_dbtest_test.go.

import (
	"context"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo/mocks"
	"github.com/wso2/aep/aep-api/internal/spec"
	"github.com/wso2/aep/aep-api/internal/spec/artifactstest"
	"github.com/wso2/aep/aep-api/models"
)

// newBudgetWatcher builds a watcher with a small budget + no DB/traitSync, for
// exercising the failure state machine in isolation.
func newBudgetWatcher(budget int, pause time.Duration) *TraitSyncWatcher {
	return &TraitSyncWatcher{
		failureBudget: budget,
		pauseFor:      pause,
		failures:      make(map[string]*tupleFailure),
	}
}

func TestTraitSyncWatcher_FailureBudget_PausesAfterConsecutive(t *testing.T) {
	t.Parallel()
	w := newBudgetWatcher(3, time.Minute)
	ctx := context.Background()
	key := "acme/web/svc"
	boom := context.DeadlineExceeded // any error

	// Below budget: the tuple keeps retrying (never paused).
	w.recordFailure(ctx, key, boom)
	w.recordFailure(ctx, key, boom)
	if w.isPaused(key) {
		t.Fatal("tuple paused before exhausting the budget")
	}

	// Hitting the budget flips it to paused.
	w.recordFailure(ctx, key, boom)
	if !w.isPaused(key) {
		t.Fatal("tuple must pause once the consecutive-failure budget is exhausted")
	}

	// A success mid-pause clears the counter (recovery).
	w.clearFailure(key)
	if w.isPaused(key) {
		t.Fatal("clearFailure must lift the pause")
	}
}

func TestTraitSyncWatcher_FailureBudget_PauseExpires(t *testing.T) {
	t.Parallel()
	w := newBudgetWatcher(1, time.Hour)
	ctx := context.Background()
	key := "acme/web/svc"

	w.recordFailure(ctx, key, context.Canceled)
	if !w.isPaused(key) {
		t.Fatal("budget of 1 must pause on the first failure")
	}

	// Rewind the pause deadline into the past: the next isPaused check clears the
	// entry so the following sweep retries the tuple.
	w.muFailures.Lock()
	w.failures[key].pausedUntil = time.Now().Add(-time.Second)
	w.muFailures.Unlock()
	if w.isPaused(key) {
		t.Fatal("an expired pause must report not-paused")
	}
	w.muFailures.Lock()
	_, stillTracked := w.failures[key]
	w.muFailures.Unlock()
	if stillTracked {
		t.Fatal("an expired pause must be evicted so the counter starts over")
	}
}

// TestTraitSyncWatcher_ReconcileOne_SkipsPausedTuple wires reconcileOne to a
// real (but always-failing) TraitSyncService: after the budget is spent the
// paused tuple must be skipped — the sync (and its OC calls) stop firing.
func TestTraitSyncWatcher_ReconcileOne_SkipsPausedTuple(t *testing.T) {
	t.Parallel()
	oc := &mocks.ComponentClientMock{
		UpdateComponentTraitsFunc: func(context.Context, string, string, string, []models.ComponentTrait) error {
			return context.DeadlineExceeded // force SyncComponentTraits to fail
		},
	}
	fakeArtifacts := &artifactstest.FakeArtifactService{
		ListDesignFilesFunc: func(context.Context, string, string) (map[string]string, error) {
			return designFiles("svc", "service", ""), nil
		},
	}
	ts := NewTraitSyncService(oc, spec.NewArtifactStore(fakeArtifacts))
	w := newBudgetWatcher(3, time.Hour)
	w.traitSync = ts
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		w.reconcileOne(ctx, "acme", "web", "svc")
	}
	if !w.isPaused("acme/web/svc") {
		t.Fatal("tuple must be paused after 3 consecutive sync failures")
	}
	if n := len(oc.UpdateComponentTraitsCalls()); n != 3 {
		t.Fatalf("sync must have fired exactly budget=3 times, got %d", n)
	}

	// Now paused: further sweeps skip the tuple, so the OC call count is frozen.
	w.reconcileOne(ctx, "acme", "web", "svc")
	if n := len(oc.UpdateComponentTraitsCalls()); n != 3 {
		t.Fatalf("paused tuple must be skipped; OC calls jumped to %d", n)
	}
}
