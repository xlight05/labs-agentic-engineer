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
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/wso2/aep/aep-api/internal/organization"

	"github.com/google/uuid"

	"github.com/wso2/aep/aep-api/internal/clients/clustergatewayproxy"
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/delivery"
)

// finalLogTailBytes caps the captured snapshot size (~3000 lines).
const finalLogTailBytes = 256 * 1024

// JobWatcher polls per-Execution coding-agent Jobs via cluster-gateway-proxy
// (the `ca-…` proxy dispatch path) and projects terminal phases onto the
// executions rows. It does NOT drive success transitions — a succeeded agent
// Job only means the pod exited zero; the PR (via the pull_request webhook) is
// the durable completion. It Finishes a coding Execution FAILED when the Job
// fails/vanishes, and captures the pod's final log into coding_agent_logs. State
// is written through the execution repository (one discipline).
type JobWatcher struct {
	logs     delivery.CodingAgentLogRepository
	orgs     organization.OrganizationRepository
	proxy    *clustergatewayproxy.Client
	execRows delivery.ExecutionRepository

	pollInterval time.Duration
	once         sync.Once

	// cleanupExternalSecrets gates the per-run ExternalSecret teardown. Only the
	// proxy DISPATCH path stages per-run ExternalSecrets (anthropic/github/
	// publisher); the direct K8sJobDispatcher writes a plain Secret and creates
	// none, so with it the cleanup would only 403/404 on resources that never
	// existed. Enabled from the composition root when proxy dispatch is active.
	cleanupExternalSecrets bool

	// notifier wakes any attached task-log stream on the failure. Nil-safe.
	notifier *delivery.TaskStreamHub

	// cycles + cycleLogs are the milestone-run half: the same `ca-…` Jobs, but
	// dispatched by a run CYCLE instead of an execution row. Both nil → the pass
	// is skipped entirely. The pass only ever captures a log; a cycle's outcome
	// is the supervisor's, learned from webhooks, never from a Job phase.
	cycles    delivery.RunCycleRepository
	cycleLogs delivery.RunCycleLogRepository
}

// cycleCaptureWindow bounds how far back a CLOSED cycle is still worth polling
// for its log. A cycle closes on the merge webhook, seconds after the agent Job
// exits — long before the next 30s tick — so restricting the pass to open cycles
// would miss nearly every capture. The Job's own ttlSecondsAfterFinished (24h)
// is what actually bounds availability; this window sits well inside it.
const cycleCaptureWindow = 6 * time.Hour

// NewJobWatcher constructs a watcher. logs + orgs + proxy + execRows required.
func NewJobWatcher(logs delivery.CodingAgentLogRepository, orgs organization.OrganizationRepository, proxy *clustergatewayproxy.Client, execRows delivery.ExecutionRepository) *JobWatcher {
	if logs == nil || orgs == nil || proxy == nil || execRows == nil {
		panic("codingagent.JobWatcher: logs + orgs + proxy + execRows are required")
	}
	return &JobWatcher{logs: logs, orgs: orgs, proxy: proxy, execRows: execRows, pollInterval: 30 * time.Second}
}

// WithExternalSecretCleanup enables per-run ExternalSecret teardown on terminal
// Job state. Wire only when the proxy dispatch path is active (it is what
// stages them); leave off for direct K8s-Job dispatch. Returns the receiver.
func (w *JobWatcher) WithExternalSecretCleanup() *JobWatcher {
	w.cleanupExternalSecrets = true
	return w
}

// WithTaskNotifier wires the task-log stream hub so a coding-job failure wakes
// attached console streams instantly. Optional — nil-safe.
func (w *JobWatcher) WithTaskNotifier(h *delivery.TaskStreamHub) *JobWatcher {
	w.notifier = h
	return w
}

// WithCycleLogCapture enables the milestone-run pass: capture a run cycle's
// agent-pod log once its Job is terminal, so the run progress stream can serve
// history after the pod is reaped. Both arguments are required to enable it.
// Returns the receiver.
func (w *JobWatcher) WithCycleLogCapture(cycles delivery.RunCycleRepository, logs delivery.RunCycleLogRepository) *JobWatcher {
	if cycles == nil || logs == nil {
		return w
	}
	w.cycles = cycles
	w.cycleLogs = logs
	return w
}

// Run blocks until ctx is canceled, ticking immediately then on pollInterval.
func (w *JobWatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	w.once.Do(func() { slog.Info("codingagent.JobWatcher: started", "interval", w.pollInterval) })
	w.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("codingagent.JobWatcher: stopping")
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *JobWatcher) tick(ctx context.Context) {
	// Claim only running, proxy-dispatched coding Executions (`ca-…` run names).
	active, err := w.execRows.ListActive(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "codingagent.JobWatcher: list active executions failed", "error", err)
		return
	}
	for i := range active {
		row := &active[i]
		if row.Kind != string(taskmeta.KindCoding) || row.Status != string(taskmeta.ExecRunning) {
			continue
		}
		if !isProxyJobRun(row.RunName) {
			continue // ClusterWorkflow runs (`wf-…`) are the ExecWatcher's job
		}
		w.checkOne(ctx, row)
	}
	w.captureCycleLogs(ctx)
}

// captureCycleLogs is the milestone-run half of the tick: snapshot a run
// cycle's agent pod once its Job is terminal.
//
// It captures and NOTHING else — no status projection, no signal, no cleanup.
// A cycle's outcome belongs to the supervisor and reaches it through webhooks;
// a Job that exited zero says nothing about whether the cycle landed. So this
// pass is purely about not losing the log when the pod is reaped.
func (w *JobWatcher) captureCycleLogs(ctx context.Context) {
	if w.cycles == nil || w.cycleLogs == nil {
		return
	}
	rows, err := w.cycles.ListRecentDispatched(ctx, time.Now().UTC().Add(-cycleCaptureWindow))
	if err != nil {
		slog.ErrorContext(ctx, "codingagent.JobWatcher: list recent run cycles failed", "error", err)
		return
	}
	for i := range rows {
		cycle := &rows[i]
		if !isProxyJobRun(cycle.JobRef) {
			continue
		}
		w.captureCycleLog(ctx, cycle)
	}
}

func (w *JobWatcher) captureCycleLog(ctx context.Context, cycle *delivery.RunCycle) {
	cycleUUID, err := uuid.Parse(cycle.ID)
	if err != nil {
		return
	}
	if existing, gerr := w.cycleLogs.GetByRun(ctx, cycleUUID, cycle.JobRef); gerr != nil || existing != nil {
		return
	}
	ns, ok := w.resolveNS(ctx, cycle.OrgID)
	if !ok {
		return
	}
	status, err := w.proxy.GetJob(ctx, ns, cycle.JobRef)
	if err != nil {
		if !errors.Is(err, clustergatewayproxy.ErrNotFound) {
			slog.WarnContext(ctx, "codingagent.JobWatcher: cycle GetJob failed", "cycle", cycle.ID, "ns", ns, "run", cycle.JobRef, "error", err)
		}
		return
	}
	phase := ""
	switch {
	case status.Succeeded > 0:
		phase = "Succeeded"
	case status.Failed > 0:
		phase = "Failed"
	default:
		return // still running — the live tail serves the stream meanwhile
	}
	podName, err := w.proxy.GetJobPodName(ctx, ns, cycle.JobRef)
	if err != nil {
		slog.WarnContext(ctx, "codingagent.JobWatcher: cycle pod lookup failed", "cycle", cycle.ID, "ns", ns, "run", cycle.JobRef, "error", err)
		return
	}
	body, err := w.proxy.TailPodLog(ctx, ns, podName, clustergatewayproxy.PodLogOptions{Timestamps: true, LimitBytes: finalLogTailBytes})
	if err != nil {
		slog.WarnContext(ctx, "codingagent.JobWatcher: cycle tail failed", "cycle", cycle.ID, "ns", ns, "pod", podName, "error", err)
		return
	}
	if err := w.cycleLogs.Create(ctx, &delivery.RunCycleLog{
		CycleID:    cycleUUID,
		RunName:    cycle.JobRef,
		FinalPhase: phase,
		LogText:    string(body),
		SizeBytes:  int64(len(body)),
	}); err != nil {
		slog.WarnContext(ctx, "codingagent.JobWatcher: cycle log persist failed", "cycle", cycle.ID, "run", cycle.JobRef, "error", err)
		return
	}
	// Token usage rides the runner's terminal NDJSON result (#249/#291) — stamp it
	// onto the cycle now that the log is in hand. This is the ONLY capture point
	// for delivery's agent spend: a cycle is one agent run, and the execution-row
	// twin above never sees one (KindProvision runs no model). Best-effort — a
	// pre-capture runner, or an agent that died before its terminal message,
	// simply carries none.
	if u := usageFromLog(string(body)); u != nil {
		if err := w.cycles.RecordUsage(ctx, cycle.ID, *u); err != nil {
			slog.WarnContext(ctx, "codingagent.JobWatcher: record cycle usage failed", "cycle", cycle.ID, "error", err)
		}
	}
	slog.InfoContext(ctx, "codingagent.JobWatcher: captured cycle log",
		"cycle", cycle.ID, "run", cycle.JobRef, "phase", phase, "bytes", len(body))
}

func (w *JobWatcher) checkOne(ctx context.Context, row *delivery.Execution) {
	ns, ok := w.resolveNS(ctx, row.OrgID)
	if !ok {
		slog.DebugContext(ctx, "codingagent.JobWatcher: NS resolve failed; skip", "execution", row.ID, "org", row.OrgID)
		return
	}
	status, err := w.proxy.GetJob(ctx, ns, row.RunName)
	if err != nil {
		if errors.Is(err, clustergatewayproxy.ErrNotFound) {
			w.finishFailed(ctx, row, "job_not_found_in_namespace")
			return
		}
		slog.WarnContext(ctx, "codingagent.JobWatcher: GetJob failed", "execution", row.ID, "ns", ns, "run", row.RunName, "error", err)
		return
	}
	switch {
	case status.Succeeded > 0:
		// Agent succeeded — the coding Execution ends via the PR webhook, not
		// here. Capture the log + clean up the per-run ExternalSecrets.
		w.captureFinalLog(ctx, row, ns, "Succeeded")
		if w.cleanupExternalSecrets {
			w.cleanupPerRunExternalSecrets(ctx, row, ns)
		}
	case status.Failed > 0:
		reason := "job_failed"
		for _, c := range status.Conditions {
			if c.Type == "Failed" && c.Status == "True" {
				reason = "job_failed:" + c.Reason
				break
			}
		}
		w.captureFinalLog(ctx, row, ns, "Failed")
		if w.cleanupExternalSecrets {
			w.cleanupPerRunExternalSecrets(ctx, row, ns)
		}
		w.finishFailed(ctx, row, reason)
	}
}

func (w *JobWatcher) finishFailed(ctx context.Context, row *delivery.Execution, reason string) {
	if _, err := w.execRows.Finish(ctx, row.ID, string(taskmeta.ExecFailed), reason); err != nil {
		slog.ErrorContext(ctx, "codingagent.JobWatcher: finish failed", "execution", row.ID, "reason", reason, "error", err)
		return
	}
	slog.InfoContext(ctx, "codingagent.JobWatcher: coding execution failed", "execution", row.ID, "reason", reason)
	w.notifier.Notify(row.Repo, row.IssueNumber)
}

// cleanupPerRunExternalSecrets deletes the per-run ExternalSecrets the
// Dispatcher applied (anthropic + github + optional publisher). Best-effort.
func (w *JobWatcher) cleanupPerRunExternalSecrets(ctx context.Context, row *delivery.Execution, ns string) {
	if row.RunName == "" {
		return
	}
	for _, name := range []string{row.RunName + "-anthropic-es", row.RunName + "-github-es", row.RunName + "-publisher-es"} {
		if err := w.proxy.DeleteExternalSecret(ctx, ns, name); err != nil {
			slog.WarnContext(ctx, "codingagent.JobWatcher: cleanup ExternalSecret failed", "execution", row.ID, "ns", ns, "es", name, "error", err)
		}
	}
}

// captureFinalLog reads the agent pod's stdout/stderr once and persists it to
// coding_agent_logs, keyed by the execution id. Idempotent on (task_id,run_name).
func (w *JobWatcher) captureFinalLog(ctx context.Context, row *delivery.Execution, ns, phase string) {
	execUUID, err := uuid.Parse(row.ID)
	if err != nil {
		slog.WarnContext(ctx, "codingagent.JobWatcher: captureFinalLog: invalid execution id", "execution", row.ID, "error", err)
		return
	}
	if existing, err := w.logs.GetByRun(ctx, execUUID, row.RunName); err == nil && existing != nil {
		return
	}
	podName, err := w.proxy.GetJobPodName(ctx, ns, row.RunName)
	if err != nil {
		slog.WarnContext(ctx, "codingagent.JobWatcher: captureFinalLog: pod lookup failed", "execution", row.ID, "ns", ns, "run", row.RunName, "error", err)
		return
	}
	body, err := w.proxy.TailPodLog(ctx, ns, podName, clustergatewayproxy.PodLogOptions{Timestamps: true, LimitBytes: finalLogTailBytes})
	if err != nil {
		slog.WarnContext(ctx, "codingagent.JobWatcher: captureFinalLog: tail failed", "execution", row.ID, "ns", ns, "pod", podName, "error", err)
		return
	}
	// Redact before persisting: this row is the run's log at rest and is served
	// back to users, so a credential reaching it would outlive the pod.
	logText := redactSecrets(string(body))
	if err := w.logs.Create(ctx, &delivery.CodingAgentLog{
		TaskID:     execUUID,
		RunName:    row.RunName,
		FinalPhase: phase,
		LogText:    logText,
		SizeBytes:  int64(len(logText)),
	}); err != nil {
		slog.WarnContext(ctx, "codingagent.JobWatcher: captureFinalLog: persist failed", "execution", row.ID, "run", row.RunName, "error", err)
		return
	}
	// Token usage rides the runner's terminal NDJSON result (#249) — stamp it
	// onto the execution row now that the log is in hand. Best-effort: a
	// pre-capture runner simply carries none.
	if u := usageFromLog(string(body)); u != nil {
		if err := w.execRows.RecordUsage(ctx, row.ID, *u); err != nil {
			slog.WarnContext(ctx, "codingagent.JobWatcher: record usage failed", "execution", row.ID, "error", err)
		}
	}
	slog.InfoContext(ctx, "codingagent.JobWatcher: captured final log", "execution", row.ID, "run", row.RunName, "phase", phase, "bytes", len(body))
}

func (w *JobWatcher) resolveNS(ctx context.Context, orgID string) (string, bool) {
	return resolveRemoteWorkerNS(ctx, w.orgs, orgID)
}
