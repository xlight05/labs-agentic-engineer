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

package codingagent

import (
	"context"
	"log/slog"
	"time"

	"github.com/wso2/aep/aep-api/internal/gen"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/feature/devflow"
	"github.com/wso2/aep/aep-api/internal/feature/execution"
	"github.com/wso2/aep/aep-api/models"
	"github.com/wso2/aep/aep-api/repositories"
)

// BuildRetrier re-mints the build clone credential and re-triggers a build at
// the row's pinned commit under a fresh run name, returning that name. The
// coding executor satisfies it — wired in only when build-secret staging is
// configured, so the retry path is inert on public-repo-only deployments.
type BuildRetrier interface {
	RetryAuthFailedBuild(ctx context.Context, row *models.Execution) (newRunName string, err error)
}

// ExecWatcher reconciles running execution rows against their OpenChoreo
// WorkflowRun status — the complement to the GitHub webhook path. It writes
// terminal outcomes through the execution repository (one discipline):
//
//   - a coding run's SUCCESS is not acted on here — the coding Execution ends
//     via the pull_request-opened webhook (the PR is the real completion); only
//     a terminal-FAILED coding run is Finished failed;
//   - a build run's success/failure IS terminal here; on success it also
//     re-evaluates the funnel so Tasks whose dependency just deployed dispatch.
//     A build that FAILED at git-clone-auth within budget is re-minted and
//     re-triggered (the retrier) rather than Finished — the legacy build
//     watcher's auth-retry loop, re-keyed to the execution row's reason (§7).
type ExecWatcher struct {
	oc        openchoreo.ComponentClient
	execRows  repositories.ExecutionRepository
	reeval    Reevaluator
	asService func(ctx context.Context) context.Context
	tick      time.Duration

	// buildRetrier + authBudget bound the git-clone-auth build retry. nil retrier
	// → a git-auth build failure is Finished failed like any other failure.
	buildRetrier BuildRetrier
	authBudget   int

	// deployObserver is notified when a component deploys (build success), so the
	// provisioning feature can grant pending cross-project access (nil → skipped).
	deployObserver DeployObserver

	// signaler feeds build + deploy terminals to a waiting devflow TaskFlow
	// workflow. Nil-safe: a nil signaler is a no-op, so the watcher behaves
	// exactly as before when no workflow is driving.
	signaler *devflow.Signaler
	// notifier wakes any attached task-log stream on a build/deploy terminal.
	// Nil-safe.
	notifier *execution.TaskStreamHub
}

// WithWorkflowSignaler wires the devflow signaler so build/deploy terminals
// reach a waiting TaskFlow workflow. Optional. Returns the receiver.
func (w *ExecWatcher) WithWorkflowSignaler(s *devflow.Signaler) *ExecWatcher {
	w.signaler = s
	return w
}

// WithTaskNotifier wires the task-log stream hub so build/deploy terminals wake
// attached console streams instantly. Optional — nil-safe.
func (w *ExecWatcher) WithTaskNotifier(h *execution.TaskStreamHub) *ExecWatcher {
	w.notifier = h
	return w
}

// NewExecWatcher wires the watcher. asService may be nil (tests); tick defaults
// to 10s.
func NewExecWatcher(oc openchoreo.ComponentClient, execRows repositories.ExecutionRepository, reeval Reevaluator, asService func(ctx context.Context) context.Context, tick time.Duration) *ExecWatcher {
	if tick <= 0 {
		tick = 10 * time.Second
	}
	return &ExecWatcher{oc: oc, execRows: execRows, reeval: reeval, asService: asService, tick: tick}
}

// WithBuildRetrier enables the git-clone-auth build retry loop. budget ≤0 uses
// the default. Returns the receiver for chained construction.
func (w *ExecWatcher) WithBuildRetrier(retrier BuildRetrier, budget int) *ExecWatcher {
	w.buildRetrier = retrier
	if budget <= 0 {
		budget = defaultBuildAuthRetryBudget
	}
	w.authBudget = budget
	return w
}

// WithDeployObserver enables the deploy-cascade notification on build success
// (nil → skipped). Returns the receiver for chained construction.
func (w *ExecWatcher) WithDeployObserver(o DeployObserver) *ExecWatcher {
	w.deployObserver = o
	return w
}

// Run sweeps on its interval until ctx is canceled.
func (w *ExecWatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.Sweep(ctx); err != nil {
				slog.WarnContext(ctx, "exec watcher sweep failed", "error", err)
			}
		}
	}
}

// Sweep runs one reconciliation pass over running executions. Exported for tests.
func (w *ExecWatcher) Sweep(ctx context.Context) error {
	if w.asService != nil {
		ctx = w.asService(ctx)
	}
	active, err := w.execRows.ListActive(ctx)
	if err != nil {
		return err
	}
	for i := range active {
		row := &active[i]
		if row.Status != string(taskmeta.ExecRunning) || row.RunName == "" {
			continue
		}
		// Proxy-dispatched coding-agent Jobs (`ca-…`) are K8s Jobs owned by the
		// JobWatcher, NOT OpenChoreo WorkflowRuns. Skip them or GetWorkflowRun
		// spams "WorkflowRun not found" every tick until the row terminates.
		if isProxyJobRun(row.RunName) {
			continue
		}
		run, gerr := w.oc.GetWorkflowRun(ctx, row.OrgID, row.RunName)
		if gerr != nil {
			slog.WarnContext(ctx, "exec watcher: get workflow run failed", "run", row.RunName, "error", gerr)
			continue
		}
		if run == nil || !run.Completed {
			continue // still running
		}
		w.reconcile(ctx, row, run)
	}
	return nil
}

func (w *ExecWatcher) reconcile(ctx context.Context, row *models.Execution, run *gen.WorkflowRun) {
	succeeded := run.Status == openchoreo.ReasonWorkflowSucceeded
	switch row.Kind {
	case string(taskmeta.KindCoding):
		if !succeeded {
			// A failed coding run — the PR will never open; Finish failed.
			if _, err := w.execRows.Finish(ctx, row.ID, string(taskmeta.ExecFailed), workflowReason(run)); err != nil {
				slog.WarnContext(ctx, "exec watcher: finish coding failed", "execution", row.ID, "error", err)
			}
			// Tell any waiting TaskFlow workflow the coding attempt failed (success
			// rides the PR-opened webhook, not this watcher).
			w.signaler.SignalTask(ctx, row.Repo, row.IssueNumber, devflow.SigJobStatus, devflow.RunStatusSignal{
				ExecutionID: row.ID,
				Phase:       devflow.PhaseFailed,
				Message:     workflowReason(run),
			})
			w.notifier.Notify(row.Repo, row.IssueNumber)
		}
		// A succeeded coding run rides the pull_request-opened webhook — no action.
	case string(taskmeta.KindBuild):
		if succeeded {
			if _, err := w.execRows.Finish(ctx, row.ID, string(taskmeta.ExecSucceeded), ""); err != nil {
				slog.WarnContext(ctx, "exec watcher: finish build succeeded", "execution", row.ID, "error", err)
				return
			}
			if w.reeval != nil {
				// A dependency just deployed — release any Task queued on it (§5).
				if rerr := w.reeval.Reevaluate(ctx); rerr != nil {
					slog.WarnContext(ctx, "exec watcher: reevaluate after build success failed", "error", rerr)
				}
			}
			// The build succeeded — tell any waiting TaskFlow workflow.
			w.signaler.SignalTask(ctx, row.Repo, row.IssueNumber, devflow.SigBuildStatus, devflow.RunStatusSignal{
				ExecutionID: row.ID,
				Phase:       devflow.PhaseSucceeded,
			})
			if w.deployObserver != nil {
				// The component deployed — grant any pending cross-project access
				// request targeting it (best-effort; the grant read no-ops otherwise).
				if derr := w.deployObserver.OnComponentDeployed(ctx, row.OrgID, row.ProjectID, row.Component); derr != nil {
					slog.WarnContext(ctx, "exec watcher: deploy observer failed", "component", row.Component, "error", derr)
				}
			}
			// Deploy is observed as part of build success in this topology (the
			// build produces the ReleaseBinding that deploys). Signal deploy-ready
			// keyed by the build row's issue — the one place the issue is known,
			// so the TaskFlow workflow needs no component→issue resolution.
			w.signaler.SignalTask(ctx, row.Repo, row.IssueNumber, devflow.SigDeployStatus, devflow.RunStatusSignal{
				ExecutionID: row.ID,
				Phase:       devflow.PhaseSucceeded,
			})
			w.notifier.Notify(row.Repo, row.IssueNumber)
			return
		}
		w.reconcileBuildFailure(ctx, row, run)
	}
}

// reconcileBuildFailure handles a completed-failed build run. A git-clone-auth
// failure within budget is re-minted + re-triggered (the row stays running, its
// attempt count threaded through the reason); budget exhaustion or a non-auth
// failure Finishes the row failed. Mirrors the legacy build watcher's §9.3 loop,
// re-keyed to the execution row's reason instead of a BuildAuthRetryCount column.
func (w *ExecWatcher) reconcileBuildFailure(ctx context.Context, row *models.Execution, run *gen.WorkflowRun) {
	_, authFailure := classifyBuildRun(run)
	if !authFailure || w.buildRetrier == nil {
		if _, err := w.execRows.Finish(ctx, row.ID, string(taskmeta.ExecFailed), workflowReason(run)); err != nil {
			slog.WarnContext(ctx, "exec watcher: finish build failed", "execution", row.ID, "error", err)
		}
		w.signaler.SignalTask(ctx, row.Repo, row.IssueNumber, devflow.SigBuildStatus, devflow.RunStatusSignal{
			ExecutionID: row.ID, Phase: devflow.PhaseFailed, Message: workflowReason(run),
		})
		w.notifier.Notify(row.Repo, row.IssueNumber)
		return
	}
	attempt := parseBuildAuthRetryAttempt(row.Reason)
	if attempt >= w.authBudget {
		if _, err := w.execRows.Finish(ctx, row.ID, string(taskmeta.ExecFailed), buildAuthRetryExceededReason); err != nil {
			slog.WarnContext(ctx, "exec watcher: finish build auth-exhausted", "execution", row.ID, "error", err)
		} else {
			slog.WarnContext(ctx, "exec watcher: build git-auth retry budget exhausted", "execution", row.ID, "attempts", attempt, "budget", w.authBudget)
		}
		w.signaler.SignalTask(ctx, row.Repo, row.IssueNumber, devflow.SigBuildStatus, devflow.RunStatusSignal{
			ExecutionID: row.ID, Phase: devflow.PhaseFailed, Message: buildAuthRetryExceededReason,
		})
		w.notifier.Notify(row.Repo, row.IssueNumber)
		return
	}
	newRun, err := w.buildRetrier.RetryAuthFailedBuild(ctx, row)
	if err != nil {
		// A stuck re-mint (org disconnected / repo not in org) must still march
		// toward budget exhaustion, so record the attempt against the SAME run.
		slog.WarnContext(ctx, "exec watcher: build auth re-mint failed", "execution", row.ID, "attempt", attempt+1, "error", err)
		if _, nerr := w.execRows.NoteBuildRetry(ctx, row.ID, row.RunName, buildAuthRetryReason(attempt+1)); nerr != nil {
			slog.WarnContext(ctx, "exec watcher: note build retry (failed re-mint) failed", "execution", row.ID, "error", nerr)
		}
		return
	}
	if _, err := w.execRows.NoteBuildRetry(ctx, row.ID, newRun, buildAuthRetryReason(attempt+1)); err != nil {
		slog.WarnContext(ctx, "exec watcher: note build retry failed", "execution", row.ID, "newRun", newRun, "error", err)
		return
	}
	slog.InfoContext(ctx, "exec watcher: re-minted + re-triggered build after git-auth failure", "execution", row.ID, "newRun", newRun, "attempt", attempt+1)
}

// workflowReason returns a short reason string for a terminal WorkflowRun.
func workflowReason(run *gen.WorkflowRun) string {
	if run.Status == openchoreo.ReasonWorkflowSucceeded {
		return ""
	}
	return run.Status
}
