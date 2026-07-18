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

package provisioning

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/delivery"
)

// resourceWatchStaleAfter bounds a stuck platform-resource provision run: past
// it the run is failed terminally (a real DB stands up in minutes; 30m stale
// means it will not come up). Mirrors the upstream resource watcher.
const resourceWatchStaleAfter = 30 * time.Minute

// ResourceWatcher reconciles running provision Executions against their OC
// ResourceReleaseBinding's native Ready condition — the async completion path
// for platform-resource provisioning (the external path completes synchronously
// in SaveValues). On Ready it Finishes the run (→ deployed), closes the gate
// issue with the binding reference, and re-evaluates the funnel so consumer
// tasks dispatch. Cloned from codingagent.ExecWatcher, keyed on KindProvision +
// the binding's Ready condition instead of a WorkflowRun's completion.
type ResourceWatcher struct {
	svc       *Service
	asService func(ctx context.Context) context.Context
	tick      time.Duration
	stale     time.Duration
	now       func() time.Time
}

// NewResourceWatcher wires the watcher. asService may be nil (tests); tick
// defaults to 10s.
func NewResourceWatcher(svc *Service, asService func(ctx context.Context) context.Context, tick time.Duration) *ResourceWatcher {
	if tick <= 0 {
		tick = 10 * time.Second
	}
	return &ResourceWatcher{svc: svc, asService: asService, tick: tick, stale: resourceWatchStaleAfter, now: time.Now}
}

// Run sweeps on its interval until ctx is canceled.
func (w *ResourceWatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.Sweep(ctx); err != nil {
				slog.WarnContext(ctx, "resource watcher sweep failed", "error", err)
			}
		}
	}
}

// Sweep runs one reconciliation pass over running provision Executions. Exported
// for tests.
func (w *ResourceWatcher) Sweep(ctx context.Context) error {
	if w.asService != nil {
		ctx = w.asService(ctx)
	}
	active, err := w.svc.execs.ListActive(ctx)
	if err != nil {
		return err
	}
	for i := range active {
		row := &active[i]
		if row.Kind != string(taskmeta.KindProvision) || row.Status != string(taskmeta.ExecRunning) || row.RunName == "" {
			continue
		}
		w.reconcile(ctx, row)
	}
	return nil
}

func (w *ResourceWatcher) reconcile(ctx context.Context, row *delivery.Execution) {
	// Stale bound: a run that has been running past the stale window is failed
	// terminally so the gate does not hang forever on a resource that will not
	// come up.
	if row.StartedAt != nil && w.now().Sub(*row.StartedAt) > w.stale {
		reason := fmt.Sprintf("provisioning timed out after %s (stale)", w.stale)
		w.svc.failProvisionRow(ctx, row.OrgID, row.ProjectID, row.IssueNumber, row.ID, reason)
		return
	}
	// row.RunName is the development binding name; row.OrgID is the OC namespace.
	b, err := w.svc.bindings.GetBinding(ctx, row.OrgID, row.RunName)
	if err != nil {
		slog.WarnContext(ctx, "resource watcher: get binding failed", "binding", row.RunName, "error", err)
		return // transient — retry next tick
	}
	if b == nil {
		return // binding not visible yet — wait
	}
	if !b.IsReady() {
		return // still provisioning — wait (the stale bound catches genuinely stuck ones)
	}
	w.svc.completeProvisionRow(ctx, row.OrgID, row.ProjectID, row.IssueNumber, row.ID,
		fmt.Sprintf("Platform resource ready (OC binding `%s`).", row.RunName))
}
