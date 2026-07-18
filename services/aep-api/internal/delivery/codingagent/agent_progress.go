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

// Coding-agent activity feed for the unified progress endpoint. The runner
// emits typed NDJSON progress events (kind:phase/tool_use/git_commit/…; schema
// at remote-worker/src/lib/progress/schema.ts) to its stdout; the JobWatcher
// captures the final tail into coding_agent_logs on terminal Job state
// (captureFinalLog). This reader surfaces that activity to the console:
//
//   - while the ca-… Job runs, it live-tails the pod's stdout via the
//     cluster-gateway-proxy (`pods/log?timestamps=true&limitBytes=64KiB`);
//   - once terminal, it serves the captured coding_agent_logs snapshot.
//
// Recovered from the pre-cutover codingagent progress_service (retired in the
// tasks-github-native migration, which ported only the build-progress path) and
// re-keyed from ComponentTask to Execution. feature/execution's ProgressService
// consumes it through the CodingProgress port; this package holds the proxy + DB
// edge so execution grows none.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/organization"

	"github.com/google/uuid"

	"github.com/wso2/aep/aep-api/internal/clients/clustergatewayproxy"
	"github.com/wso2/aep/aep-api/internal/contracts"
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

const (
	// progressSchemaVersion mirrors the runner NDJSON schema (schemaVersion=1).
	progressSchemaVersion = 1
	// defaultProgressLimit caps events surfaced per poll (matches the retired
	// pre-cutover reader).
	defaultProgressLimit = 200
	// logPageBytes bounds the live-tail window read per poll — the LAST 64KiB of
	// pod stdout, so old lines scroll off and the console gets fresh content.
	// Paired with the watcher's 256KiB final-capture (finalLogTailBytes).
	logPageBytes = 64 * 1024
)

// Bootstrap seqs identify the synthetic "dark zone" progress lines the reader
// emits BEFORE the runner writes its first NDJSON line — the stretch (pod
// scheduling, image pull, container boot) the live-tail is otherwise blind to,
// so the console showed a dead "waiting…" for the slowest part of the flow.
//
// Each pre-stdout state carries a STABLE, NEGATIVE seq. The console dedups the
// unified timeline by (executionId, seq) and the runner's real lines use
// positive seqs, so: negatives never collide with real output; the same state
// re-derived every ~2s collapses to one row; and a state TRANSITION (a new
// negative seq) shows exactly one new row. Ts is left empty on purpose — these
// are transient markers, not wall-clock log lines (filterEventsAfter keeps
// untimestamped events, and lastEventMillis ignores them, so the cursor never
// advances past a synthetic line).
const (
	seqBootScheduling = -10 // Job applied, pod not scheduled / created yet
	seqBootPulling    = -11 // ContainerCreating / PodInitializing (image pull + setup)
	seqBootBackoff    = -12 // ImagePullBackOff / ErrImagePull (pull retrying)
	seqBootConfig     = -13 // CreateContainerConfigError / secrets not yet materialised
	seqBootStarting   = -14 // container Running, agent has not emitted its first line
)

// bootstrapEvent maps a pre-stdout runner state to the synthetic progress line
// the console renders during the dark zone. podFound=false means the Job exists
// but no pod object does yet; otherwise phase/waitingReason come from the pod's
// status (waitingReason is the first waiting container's reason, "" once the
// container is running). Phase names are stable ids the console maps to friendly
// labels; Summary is the human fallback when it doesn't.
func bootstrapEvent(podFound bool, phase, waitingReason string) contracts.ProgressEvent {
	mk := func(seq int64, name, summary string) contracts.ProgressEvent {
		return contracts.ProgressEvent{
			SchemaVersion: progressSchemaVersion,
			Seq:           seq,
			Kind:          "phase",
			Phase:         name,
			Summary:       summary,
			// Ts intentionally empty — synthetic, wall-clock-less marker.
		}
	}
	if !podFound {
		return mk(seqBootScheduling, "runner_scheduling", "Waiting for a runner to be scheduled…")
	}
	switch waitingReason {
	case "ImagePullBackOff", "ErrImagePull", "ImageInspectError", "RegistryUnavailable":
		return mk(seqBootBackoff, "runner_image_pull_backoff", "Pulling the agent image is taking longer than usual (retrying)…")
	case "CreateContainerConfigError", "CreateContainerError", "InvalidImageName":
		return mk(seqBootConfig, "runner_config_error", "Runner is waiting on its configuration and secrets…")
	case "ContainerCreating", "PodInitializing":
		return mk(seqBootPulling, "runner_pulling_image", "Pulling the agent image and preparing the container…")
	case "":
		// No waiting reason: a Running pod is booting the agent; anything else
		// (Pending with no container status yet) is still being scheduled.
		if strings.EqualFold(phase, "Running") {
			return mk(seqBootStarting, "runner_starting", "Runner container started — booting the agent…")
		}
		return mk(seqBootScheduling, "runner_scheduling", "Waiting for a runner to be scheduled…")
	default:
		// An unrecognised waiting reason — surface it verbatim so nothing hides,
		// bucketed under the pulling seq (its most common cause).
		return mk(seqBootPulling, "runner_pulling_image", "Preparing the runner container ("+waitingReason+")…")
	}
}

// AgentProgressReader serves coding-execution activity from the ca-… pod log:
// the live tail while the Job runs, the captured coding_agent_logs snapshot once
// terminal. Wired from the cluster-gateway-proxy client + DB at the composition
// root; nil-safe consumers skip it (the endpoint then reports terminal-ness
// only, the pre-reconnect behaviour).
type AgentProgressReader struct {
	proxy *clustergatewayproxy.Client
	logs  delivery.CodingAgentLogRepository
	orgs  organization.OrganizationRepository
}

// NewAgentProgressReader wires the reader. proxy/logs may be nil in degraded
// boot (AgentProgress then returns an empty, non-final response).
func NewAgentProgressReader(proxy *clustergatewayproxy.Client, logs delivery.CodingAgentLogRepository, orgs organization.OrganizationRepository) *AgentProgressReader {
	return &AgentProgressReader{proxy: proxy, logs: logs, orgs: orgs}
}

// AgentProgress returns the coding execution's activity, filtered to events
// strictly newer than sinceMillis (the console's cursor). It prefers the
// persisted coding_agent_logs snapshot (Final=true — the complete captured log);
// absent that, it live-tails the pod (Final=false). "Not ready yet" states (no
// run name, pod not scheduled, container starting, NS unresolved) return an
// empty, non-final response so the console keeps polling rather than flashing an
// error. Genuine read/tail failures return an error for the caller to degrade.
func (r *AgentProgressReader) AgentProgress(ctx context.Context, row *delivery.Execution, sinceMillis int64) (*contracts.ProgressResponse, error) {
	resp := &contracts.ProgressResponse{
		SchemaVersion: progressSchemaVersion,
		Lines:         []contracts.ProgressEvent{},
		CursorMillis:  sinceMillis,
		Final:         false,
	}
	if r == nil || r.proxy == nil || r.logs == nil || row == nil || row.RunName == "" {
		return resp, nil
	}
	execUUID, err := uuid.Parse(row.ID)
	if err != nil {
		return nil, fmt.Errorf("parse execution id: %w", err)
	}

	// Snapshot path: prefer the persisted sidecar row when present (the watcher
	// wrote it on terminal Job state — the complete final log).
	snap, err := r.logs.GetByRun(ctx, execUUID, row.RunName)
	switch {
	case err == nil && snap != nil:
		resp.Lines, resp.Truncated = pageEvents(snap.LogText, sinceMillis)
		if cur := lastEventMillis(resp.Lines); cur > resp.CursorMillis {
			resp.CursorMillis = cur
		}
		// Advance to captured_at when the snapshot is fully consumed, so a
		// second poll's sinceMillis is at/past captured_at and the cursor never
		// slides backwards.
		if capturedMs := snap.CapturedAt.UnixMilli(); capturedMs > resp.CursorMillis {
			resp.CursorMillis = capturedMs
		}
		resp.Final = true
		return resp, nil
	case err == nil && snap == nil:
		// fall through to the live tail
	default:
		return nil, fmt.Errorf("read coding_agent_logs: %w", err)
	}

	// Live tail path: the watcher hasn't captured yet (Job still active).
	ns, ok := resolveRemoteWorkerNS(ctx, r.orgs, row.OrgID)
	if !ok {
		// Org row missing / no Thunder UUID yet — keep polling non-final; the
		// watcher captures on terminal state regardless.
		return resp, nil
	}

	// Once the row is terminal but the snapshot hasn't landed (a brief
	// watcher-tick race), don't narrate the pod — the status settles the stream.
	// The synthetic bootstrap lines below are only meaningful while the attempt
	// is still queued/running.
	live := !taskmeta.ExecutionStatus(row.Status).IsTerminal()

	pod, err := r.proxy.GetJobPod(ctx, ns, row.RunName)
	if err != nil {
		if errors.Is(err, clustergatewayproxy.ErrNotFound) {
			// Job applied, pod not scheduled/created yet (or GC'd past TTL with no
			// snapshot). Narrate "scheduling" so the console shows motion instead
			// of a dead "waiting…" during the dark zone.
			if live {
				resp.Lines = []contracts.ProgressEvent{bootstrapEvent(false, "", "")}
			}
			return resp, nil
		}
		return nil, fmt.Errorf("get job pod: %w", err)
	}
	body, err := r.proxy.TailPodLog(ctx, ns, pod.Name, clustergatewayproxy.PodLogOptions{
		Timestamps: true,
		LimitBytes: logPageBytes,
	})
	if err != nil {
		// ErrNotFound: pod GC'd / not scheduled. ErrPodNotReady: container still
		// starting (image pull / ContainerCreating / config). Both mean "no logs
		// yet" — narrate the pod state from its status instead of a dead wait.
		if errors.Is(err, clustergatewayproxy.ErrNotFound) ||
			errors.Is(err, clustergatewayproxy.ErrPodNotReady) {
			if live {
				resp.Lines = []contracts.ProgressEvent{bootstrapEvent(true, pod.Phase, pod.WaitingReason)}
			}
			return resp, nil
		}
		return nil, fmt.Errorf("tail pod log: %w", err)
	}
	all, truncated := textToProgressEvents(string(body))
	if len(all) == 0 {
		// Container is up but the runner hasn't emitted its first line yet (token
		// mint / node boot). Name the wait rather than showing nothing.
		if live {
			resp.Lines = []contracts.ProgressEvent{bootstrapEvent(true, "Running", "")}
		}
		return resp, nil
	}
	newer := filterEventsAfter(all, sinceMillis)
	if len(newer) > defaultProgressLimit {
		newer = newer[:defaultProgressLimit]
		truncated = true
	}
	resp.Lines, resp.Truncated = newer, truncated
	// Advance the cursor only as far as actually-emitted content reaches (NOT
	// now()), so the next poll's window never skips late-arriving lines.
	if cur := lastEventMillis(resp.Lines); cur > resp.CursorMillis {
		resp.CursorMillis = cur
	}
	return resp, nil
}

// pageEvents parses a raw pod-log page into events newer than sinceMillis,
// capped at defaultProgressLimit. truncated is true when the raw page exceeded
// the cap (oldest lines dropped) or the post-filter set still exceeds it.
func pageEvents(text string, sinceMillis int64) ([]contracts.ProgressEvent, bool) {
	all, truncated := textToProgressEvents(text)
	newer := filterEventsAfter(all, sinceMillis)
	if len(newer) > defaultProgressLimit {
		newer = newer[:defaultProgressLimit]
		truncated = true
	}
	return newer, truncated
}

// resolveRemoteWorkerNS maps an OC org handle to its remote-worker namespace
// (the ca-… Job namespace). Shared by the JobWatcher (log capture) and the
// AgentProgressReader (live tail).
func resolveRemoteWorkerNS(ctx context.Context, orgs organization.OrganizationRepository, orgID string) (string, bool) {
	org, err := orgs.GetByName(ctx, orgID)
	if err != nil || org == nil {
		return "", false
	}
	var uid string
	if org.ThunderOrgUUID != nil && *org.ThunderOrgUUID != uuid.Nil {
		uid = org.ThunderOrgUUID.String()
	} else {
		uid = org.UUID.String()
	}
	if uid == "" || uid == "00000000-0000-0000-0000-000000000000" {
		return "", false
	}
	return tenant.RemoteWorkerNamespace(uid), true
}

// textToProgressEvents splits raw pod stdout/stderr into progress events. Each
// line becomes one event; the K8s `timestamps=true` prefix
// (`YYYY-MM-DDTHH:MM:SS.NNNNNNNNNZ <line>`) is split off the front and used as
// the event Ts when the envelope carries none. When the page holds more than
// defaultProgressLimit lines the newest window is kept (live-tail freshness)
// and truncated is true.
func textToProgressEvents(text string) ([]contracts.ProgressEvent, bool) {
	if text == "" {
		return []contracts.ProgressEvent{}, false
	}
	out := make([]contracts.ProgressEvent, 0, 256)
	scanner := bufio.NewScanner(strings.NewReader(text))
	// Allow long lines — agent output occasionally dumps long JSON blobs that
	// exceed the default 64K scanner buffer.
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	for scanner.Scan() {
		ts, msg := splitTimestampPrefix(scanner.Text())
		ev := parseProgressLine(msg)
		if ev.Ts == "" {
			ev.Ts = ts
		}
		if ev.SchemaVersion == 0 {
			ev.SchemaVersion = progressSchemaVersion
		}
		out = append(out, ev)
	}
	if len(out) > defaultProgressLimit {
		return out[len(out)-defaultProgressLimit:], true
	}
	return out, false
}

// parseProgressLine decodes one runner NDJSON envelope. Non-JSON lines, or lines
// without a recognised schema version / kind, are wrapped as a `log` event so
// the feed stays continuous (bootstrap output like "[oneshot] …" and stray
// library lines still render). Recovered from the retired observer.ParseProgressLine.
func parseProgressLine(raw string) contracts.ProgressEvent {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed[0] != '{' {
		return contracts.ProgressEvent{Kind: "log", Summary: raw}
	}
	var ev contracts.ProgressEvent
	if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
		return contracts.ProgressEvent{Kind: "log", Summary: raw}
	}
	if ev.SchemaVersion != progressSchemaVersion || ev.Kind == "" {
		return contracts.ProgressEvent{Kind: "log", Summary: raw}
	}
	return ev
}

// splitTimestampPrefix peels the K8s `?timestamps=true` prefix off a log line.
// Returns (ts, rest); ts="" when no RFC3339Nano prefix is present.
func splitTimestampPrefix(line string) (string, string) {
	i := strings.IndexByte(line, ' ')
	if i <= 0 {
		return "", line
	}
	candidate := line[:i]
	if _, err := time.Parse(time.RFC3339Nano, candidate); err != nil {
		return "", line
	}
	return candidate, line[i+1:]
}

// filterEventsAfter drops events whose ts is at or before sinceMillis (half-open
// (sinceMillis, +∞)). Events without a parseable ts are kept — the BFF never
// silently loses content it can't timestamp.
func filterEventsAfter(events []contracts.ProgressEvent, sinceMillis int64) []contracts.ProgressEvent {
	if sinceMillis <= 0 {
		return events
	}
	out := events[:0:len(events)]
	for _, e := range events {
		if e.Ts == "" {
			out = append(out, e)
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, e.Ts)
		if err != nil {
			out = append(out, e)
			continue
		}
		if t.UnixMilli() > sinceMillis {
			out = append(out, e)
		}
	}
	return out
}

// lastEventMillis returns the highest UnixMilli ts in events, or 0 when none
// carry a parseable ts. Advances the cursor only as far as emitted content
// reaches, so the next poll never skips late-arriving lines.
func lastEventMillis(events []contracts.ProgressEvent) int64 {
	var max int64
	for _, e := range events {
		if e.Ts == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, e.Ts)
		if err != nil {
			continue
		}
		if m := t.UnixMilli(); m > max {
			max = m
		}
	}
	return max
}
