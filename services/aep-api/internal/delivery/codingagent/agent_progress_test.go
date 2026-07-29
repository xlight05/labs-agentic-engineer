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
	"strings"
	"testing"
	"time"
)

// TestParseProgressLine covers the runner NDJSON → event decode and the
// non-envelope fallback to a `log` event.
func TestParseProgressLine(t *testing.T) {
	t.Parallel()

	phase := parseProgressLine(`{"schemaVersion":1,"ts":"2026-07-08T10:14:17.210Z","seq":2,"kind":"phase","phase":"workspace_ready"}`)
	if phase.Kind != "phase" || phase.Phase != "workspace_ready" || phase.Seq != 2 {
		t.Errorf("phase envelope = %+v", phase)
	}

	tool := parseProgressLine(`{"schemaVersion":1,"seq":15,"kind":"tool_use","tool":"Read","summary":"reading design"}`)
	if tool.Kind != "tool_use" || tool.Tool != "Read" || tool.Summary != "reading design" {
		t.Errorf("tool_use envelope = %+v", tool)
	}

	// Bootstrap / library stdout is not JSON → wrapped as a log event verbatim.
	boot := parseProgressLine("[oneshot] materialised 3 skill(s); preload=1 org skill(s)")
	if boot.Kind != "log" || boot.Summary != "[oneshot] materialised 3 skill(s); preload=1 org skill(s)" {
		t.Errorf("bootstrap line = %+v, want kind=log with raw summary", boot)
	}

	// JSON that isn't a recognised envelope (no kind) → log fallback.
	weird := parseProgressLine(`{"hello":"world"}`)
	if weird.Kind != "log" {
		t.Errorf("unrecognised json = %+v, want kind=log", weird)
	}

	// Wrong schema version → log fallback (forward-compat guard).
	future := parseProgressLine(`{"schemaVersion":2,"kind":"phase","phase":"x"}`)
	if future.Kind != "log" {
		t.Errorf("future schema = %+v, want kind=log", future)
	}
}

// TestTextToProgressEvents pins the K8s timestamp-prefix split, envelope-ts
// preference, and the newest-window cap.
func TestTextToProgressEvents(t *testing.T) {
	t.Parallel()

	// K8s `timestamps=true` prefix + envelope carrying its own ts → envelope ts wins.
	withEnvTs := "2026-07-08T10:14:17.300000000Z " +
		`{"schemaVersion":1,"ts":"2026-07-08T10:14:17.210Z","seq":2,"kind":"phase","phase":"workspace_ready"}`
	// K8s prefix + a bootstrap (non-JSON) line → prefix used as the event ts.
	bootWithPrefix := "2026-07-08T10:14:18.000000000Z [oneshot] materialised 3 skill(s)"

	events, _ := textToProgressEvents(withEnvTs + "\n" + bootWithPrefix + "\n")
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(events), events)
	}
	if events[0].Kind != "phase" || events[0].Ts != "2026-07-08T10:14:17.210Z" {
		t.Errorf("event0 = %+v, want phase with envelope ts", events[0])
	}
	if events[1].Kind != "log" || events[1].Ts != "2026-07-08T10:14:18.000000000Z" {
		t.Errorf("event1 = %+v, want log with k8s-prefix ts", events[1])
	}
	for _, e := range events {
		if e.SchemaVersion != progressSchemaVersion {
			t.Errorf("schemaVersion not stamped: %+v", e)
		}
	}

	if got, _ := textToProgressEvents(""); len(got) != 0 {
		t.Errorf("empty text → %d events, want 0", len(got))
	}

	// Over-cap input keeps the NEWEST window (live-tail freshness).
	var b strings.Builder
	for i := 0; i < defaultProgressLimit+50; i++ {
		b.WriteString(`{"schemaVersion":1,"kind":"log","summary":"line"}` + "\n")
	}
	capped, _ := textToProgressEvents(b.String())
	if len(capped) != defaultProgressLimit {
		t.Errorf("capped len = %d, want %d", len(capped), defaultProgressLimit)
	}
}

// TestFilterEventsAfter pins the half-open (sinceMillis, +∞) cursor filter and
// the keep-untimestamped rule.
func TestFilterEventsAfter(t *testing.T) {
	t.Parallel()

	// ts = 2026-07-08T10:14:17.210Z → 1783505657210 ms.
	const tsA = "2026-07-08T10:14:17.210Z" // older
	const tsB = "2026-07-08T10:14:18.500Z" // newer
	msA := int64(1783505657210)

	events, _ := textToProgressEvents(
		`{"schemaVersion":1,"ts":"` + tsA + `","kind":"phase","phase":"a"}` + "\n" +
			`{"schemaVersion":1,"ts":"` + tsB + `","kind":"phase","phase":"b"}` + "\n" +
			`{"schemaVersion":1,"kind":"log","summary":"no-ts"}` + "\n",
	)

	// sinceMillis at tsA drops A (== boundary), keeps B (newer) and the no-ts line.
	got := filterEventsAfter(events, msA)
	var phases []string
	for _, e := range got {
		if e.Kind == "phase" {
			phases = append(phases, e.Phase)
		}
	}
	if len(phases) != 1 || phases[0] != "b" {
		t.Errorf("phases after filter = %v, want [b] (a dropped at boundary)", phases)
	}
	// The untimestamped line is always kept.
	kept := false
	for _, e := range got {
		if e.Summary == "no-ts" {
			kept = true
		}
	}
	if !kept {
		t.Error("untimestamped event must be kept")
	}

	// sinceMillis<=0 → no filtering.
	if got := filterEventsAfter(events, 0); len(got) != len(events) {
		t.Errorf("sinceMillis=0 filtered %d→%d, want no-op", len(events), len(got))
	}
}

// TestLastEventMillis returns the max parseable ts, ignoring untimestamped lines.
func TestLastEventMillis(t *testing.T) {
	t.Parallel()
	events, _ := textToProgressEvents(
		`{"schemaVersion":1,"ts":"2026-07-08T10:14:17.210Z","kind":"log","summary":"a"}` + "\n" +
			`{"schemaVersion":1,"ts":"2026-07-08T10:14:18.500Z","kind":"log","summary":"b"}` + "\n" +
			`{"schemaVersion":1,"kind":"log","summary":"no-ts"}` + "\n",
	)
	if got := lastEventMillis(events); got != 1783505658500 {
		t.Errorf("lastEventMillis = %d, want 1783505658500 (tsB)", got)
	}
	if got := lastEventMillis(nil); got != 0 {
		t.Errorf("lastEventMillis(nil) = %d, want 0", got)
	}
}

// TestBootstrapEvent pins the pre-stdout "dark zone" state → synthetic line
// mapping: every state is a kind=phase event with a STABLE negative seq (so the
// console dedups re-polls yet shows each transition) and an empty ts (a
// transient marker, not a wall-clock log line).
func TestBootstrapEvent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		podFound      bool
		phase         string
		waitingReason string
		wantSeq       int64
		wantPhase     string
	}{
		{"no pod yet", false, "", "", seqBootScheduling, "runner_scheduling"},
		{"container creating", true, "Pending", "ContainerCreating", seqBootPulling, "runner_pulling_image"},
		{"pod initializing", true, "Pending", "PodInitializing", seqBootPulling, "runner_pulling_image"},
		{"image pull backoff", true, "Pending", "ImagePullBackOff", seqBootBackoff, "runner_image_pull_backoff"},
		{"err image pull", true, "Pending", "ErrImagePull", seqBootBackoff, "runner_image_pull_backoff"},
		{"config error", true, "Pending", "CreateContainerConfigError", seqBootConfig, "runner_config_error"},
		{"invalid image name", true, "Pending", "InvalidImageName", seqBootConfig, "runner_config_error"},
		{"running no output", true, "Running", "", seqBootStarting, "runner_starting"},
		{"pending no reason", true, "Pending", "", seqBootScheduling, "runner_scheduling"},
		{"unknown reason", true, "Pending", "SomethingWeird", seqBootPulling, "runner_pulling_image"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := bootstrapEvent(tc.podFound, tc.phase, tc.waitingReason)
			if ev.Kind != "phase" {
				t.Errorf("kind = %q, want phase", ev.Kind)
			}
			if ev.Seq != tc.wantSeq {
				t.Errorf("seq = %d, want %d", ev.Seq, tc.wantSeq)
			}
			if ev.Phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", ev.Phase, tc.wantPhase)
			}
			if ev.Ts != "" {
				t.Errorf("ts = %q, want empty (synthetic marker)", ev.Ts)
			}
			if ev.SchemaVersion != progressSchemaVersion {
				t.Errorf("schemaVersion = %d, want %d", ev.SchemaVersion, progressSchemaVersion)
			}
			if ev.Summary == "" {
				t.Error("summary must be a non-empty human fallback")
			}
		})
	}

	// An unrecognised reason is surfaced verbatim so nothing hides.
	if ev := bootstrapEvent(true, "Pending", "SomethingWeird"); !strings.Contains(ev.Summary, "SomethingWeird") {
		t.Errorf("unknown reason summary = %q, want it to contain the raw reason", ev.Summary)
	}

	// Distinct states must carry distinct seqs (else the console dedups two
	// different transitions into one row).
	seqs := map[int64]bool{
		seqBootScheduling: true, seqBootPulling: true, seqBootBackoff: true,
		seqBootConfig: true, seqBootStarting: true,
	}
	if len(seqs) != 5 {
		t.Errorf("bootstrap seqs collide: %v", seqs)
	}
}

// TestPageEvents caps and flags truncation.
func TestPageEvents(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 0; i < defaultProgressLimit+10; i++ {
		b.WriteString(`{"schemaVersion":1,"kind":"log","summary":"x"}` + "\n")
	}
	lines, truncated, _ := pageEvents(b.String(), 0)
	if len(lines) != defaultProgressLimit || !truncated {
		t.Errorf("pageEvents over-cap = (%d, %v), want (%d, true)", len(lines), truncated, defaultProgressLimit)
	}

	lines, truncated, _ = pageEvents(`{"schemaVersion":1,"kind":"phase","phase":"p"}`+"\n", 0)
	if len(lines) != 1 || truncated {
		t.Errorf("pageEvents under-cap = (%d, %v), want (1, false)", len(lines), truncated)
	}
}

// TestPageEventsHadOutput pins the distinction that keeps the dark-zone
// narration out of a live stream: hadOutput is a fact about the POD LOG, not
// about the cursor window. A tail carrying only already-seen lines yields no new
// lines yet still had output, so the caller must not re-narrate "Starting the
// agent…" — the console dedups bootstrap lines on a stable seq, so one such
// mid-stream emission is pinned in place for the rest of the run.
func TestPageEventsHadOutput(t *testing.T) {
	t.Parallel()

	const seen = `{"schemaVersion":1,"kind":"log","summary":"first","ts":"2026-07-28T10:00:00.000000000Z"}` + "\n"

	if _, _, hadOutput := pageEvents("", 0); hadOutput {
		t.Error("empty page: hadOutput = true, want false (the runner has not spoken)")
	}

	// The agent is mid-thought: the tail still holds the line we already emitted.
	cursor := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC).UnixMilli()
	lines, _, hadOutput := pageEvents(seen, cursor)
	if len(lines) != 0 {
		t.Errorf("already-seen page: lines = %d, want 0", len(lines))
	}
	if !hadOutput {
		t.Error("already-seen page: hadOutput = false, want true (the pod HAS produced output)")
	}
}
