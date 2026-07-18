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

package runtimeconfig

import (
	"context"
	"log/slog"
	"time"

	"github.com/wso2/aep/aep-api/repositories"
)

// DeployedProjectLister enumerates every project with something dispatched —
// the sweep's input. *repositories.ExecutionRepository satisfies it via
// DistinctDeployedProjects; the watcher needs only that one method, so it takes
// this narrow port rather than the ORM.
type DeployedProjectLister interface {
	DistinctDeployedProjects(ctx context.Context) ([]repositories.DeployedProjectRef, error)
}

// Watcher is the convergence backstop for SPA env-config.js emission — the
// mirror of TraitSyncWatcher for runtime config.
//
// Why this exists: env-config.js is emitted eagerly on the coding-dispatch
// pre-flight and again on each component's build-success (spaDeployObserver).
// But a web-app's OWN public URL — required for the consumer-url-env-config
// callback patch of a thunder-app dependency — only becomes available after
// its ReleaseBinding endpoint resolves, which is LATER than the build-success
// event that would emit it. When that URL is still empty the emit gate returns
// ready=false and skips the whole write (all-or-nothing). Because a web-app is
// dispatched last (it depends on its sibling service), no further build-success
// fires to re-trigger the emit, so the deferral is permanent and window._env_
// is never written — the SPA renders blank.
//
// This watcher runs on a ticker and re-invokes EmitForProjectSPAs for every
// project with dispatched components. EmitForProjectSPAs is idempotent and
// convergent (it only writes when the assembled config actually changed), so
// re-running it every tick is safe; it simply lands the env-config once the
// SPA's URL finally resolves. It replaces the periodic reconcile backstop that
// was dropped when the trait_sync drift watcher moved to the ExecWatcher deploy
// path (see the note in internal/app/app.go).
type Watcher struct {
	projects          DeployedProjectLister
	svc               *RuntimeConfigService
	asServiceIdentity func(ctx context.Context) context.Context
	tick              time.Duration
}

// NewWatcher builds the runtime-config convergence watcher. asServiceIdentity
// is optional — when non-nil it marks outbound OC calls as service-identity
// (M2M + per-org impersonation), matching the other deploy-path watchers. A
// non-positive tick defaults to 30s (env-config convergence is not latency
// critical — the SPA URL resolves within seconds of the pod going Ready).
func NewWatcher(
	projects DeployedProjectLister,
	svc *RuntimeConfigService,
	asServiceIdentity func(ctx context.Context) context.Context,
	tick time.Duration,
) *Watcher {
	if tick <= 0 {
		tick = 30 * time.Second
	}
	return &Watcher{projects: projects, svc: svc, asServiceIdentity: asServiceIdentity, tick: tick}
}

// Run blocks until ctx is cancelled. Spawned as a goroutine from main.
func (w *Watcher) Run(ctx context.Context) {
	if w.svc == nil || w.projects == nil {
		slog.InfoContext(ctx, "runtimeconfig watcher: svc/projects nil; not starting")
		return
	}
	ticker := time.NewTicker(w.tick)
	defer ticker.Stop()
	slog.InfoContext(ctx, "runtimeconfig watcher started", "tick", w.tick)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

func (w *Watcher) sweep(ctx context.Context) {
	if w.asServiceIdentity != nil {
		ctx = w.asServiceIdentity(ctx)
	}
	// Every dispatched component leaves execution rows carrying its org+project,
	// so this catches every project with something deployed to configure. A
	// project with a design but no dispatch yet has no SPA to emit for.
	tuples, err := w.projects.DistinctDeployedProjects(ctx)
	if err != nil {
		slog.WarnContext(ctx, "runtimeconfig watcher: enumerate projects failed", "error", err)
		return
	}
	for _, t := range tuples {
		// EmitForProjectSPAs internally no-ops for projects with no SPA, and is
		// idempotent for those already converged; a per-project failure is
		// transient (the SPA URL not yet resolved) and retried next tick.
		if err := w.svc.EmitForProjectSPAs(ctx, t.OrgID, t.ProjectID); err != nil {
			slog.DebugContext(ctx, "runtimeconfig watcher: emit failed; will retry next tick",
				"org", t.OrgID, "project", t.ProjectID, "error", err)
		}
	}
}
