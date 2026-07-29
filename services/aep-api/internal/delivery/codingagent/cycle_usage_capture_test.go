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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wso2/aep/aep-api/internal/clients/clustergatewayproxy"
	"github.com/wso2/aep/aep-api/internal/contracts"
	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/organization"
)

// ---- fakes ----------------------------------------------------------------

type fakeOrgsForCycle struct {
	organization.OrganizationRepository // unused methods
	uid                                 uuid.UUID
}

func (f fakeOrgsForCycle) GetByName(context.Context, string) (*organization.Organization, error) {
	return &organization.Organization{UUID: f.uid}, nil
}

// fakeCycleRepo records what the capture pass wrote. Only the two methods the
// pass touches are real; the rest of the interface is embedded (nil), so a pass
// that reached for anything else would panic rather than pass quietly.
type fakeCycleRepo struct {
	delivery.RunCycleRepository
	dispatched  []delivery.RunCycle
	recorded    map[string]contracts.TokenUsage
	recordCalls int
}

func (f *fakeCycleRepo) ListRecentDispatched(context.Context, time.Time) ([]delivery.RunCycle, error) {
	return f.dispatched, nil
}

func (f *fakeCycleRepo) RecordUsage(_ context.Context, id string, u contracts.TokenUsage) error {
	if f.recorded == nil {
		f.recorded = map[string]contracts.TokenUsage{}
	}
	f.recorded[id] = u
	f.recordCalls++
	return nil
}

type fakeCycleLogRepo struct {
	existing map[string]*delivery.RunCycleLog
	created  []delivery.RunCycleLog
}

func (f *fakeCycleLogRepo) GetByRun(_ context.Context, cycleID uuid.UUID, runName string) (*delivery.RunCycleLog, error) {
	return f.existing[cycleID.String()+"/"+runName], nil
}

func (f *fakeCycleLogRepo) Create(_ context.Context, row *delivery.RunCycleLog) error {
	f.created = append(f.created, *row)
	return nil
}

// ---- harness --------------------------------------------------------------

// cycleCaptureEnv stands up a proxy pointed at an httptest server that answers
// the three calls the capture pass makes (job status, pod name, pod log) with a
// real terminal runner log. Using the REAL proxy client keeps the test honest
// about the wire shapes it decodes.
func cycleCaptureEnv(t *testing.T, jobStatus, podLog string) (*JobWatcher, *fakeCycleRepo, *fakeCycleLogRepo) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/jobs/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":` + jobStatus + `}`))
		case strings.Contains(r.URL.Path, "/pods") && !strings.Contains(r.URL.Path, "/log"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"pod-1"},"status":{"phase":"Succeeded"}}]}`))
		case strings.Contains(r.URL.Path, "/log"):
			_, _ = w.Write([]byte(podLog))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	proxy := clustergatewayproxy.New(clustergatewayproxy.Config{BaseURL: srv.URL})
	cycles := &fakeCycleRepo{}
	logs := &fakeCycleLogRepo{}
	w := NewJobWatcher(&fakeCodingLogs{}, fakeOrgsForCycle{uid: uuid.New()}, proxy, newFakeExecRepo()).
		WithCycleLogCapture(cycles, logs)
	return w, cycles, logs
}

// fakeCodingLogs satisfies the execution-side log repo the constructor requires;
// the cycle pass never touches it.
type fakeCodingLogs struct{ delivery.CodingAgentLogRepository }

const terminalRunnerLog = `2026-07-29T10:00:00.000000000Z {"schemaVersion":1,"ts":"t","seq":1,"kind":"phase","phase":"agent"}
2026-07-29T10:00:09.000000000Z {"schemaVersion":1,"ts":"t","seq":9,"kind":"result","status":"success","usage":{"inputTokens":1200,"outputTokens":340,"cacheReadTokens":50000,"cacheCreationTokens":700,"model":"claude-fable-5"}}
`

// ---- tests ----------------------------------------------------------------

// The regression this whole change exists for: a terminal cycle's log carries
// the run's token usage, and the capture pass must stamp it onto the CYCLE.
// Before this, capture only ever walked Execution rows — which no agent run
// produces after the issue-driven flip — so delivery's spend was never recorded.
func TestCaptureCycleLog_RecordsUsageOnTheCycle(t *testing.T) {
	w, cycles, logs := cycleCaptureEnv(t, `{"succeeded":1}`, terminalRunnerLog)
	cycleID := uuid.New().String()
	cycles.dispatched = []delivery.RunCycle{{
		ID: cycleID, OrgID: "acme", ProjectID: "shop", JobRef: "ca-abc",
		Kind: delivery.CycleKindCoding,
	}}

	w.captureCycleLogs(context.Background())

	if len(logs.created) != 1 {
		t.Fatalf("cycle logs created = %d, want 1", len(logs.created))
	}
	got, ok := cycles.recorded[cycleID]
	if !ok {
		t.Fatalf("no usage recorded for the cycle; recorded = %+v", cycles.recorded)
	}
	want := contracts.TokenUsage{
		InputTokens: 1200, OutputTokens: 340, CacheReadTokens: 50000,
		CacheCreationTokens: 700, Model: "claude-fable-5",
	}
	if got != want {
		t.Fatalf("recorded usage = %+v, want %+v", got, want)
	}
}

// A validation cycle is an agent run like any other — its spend is captured the
// same way, which is what puts the validation phase back on the Usage page.
func TestCaptureCycleLog_RecordsUsageForValidationCycles(t *testing.T) {
	w, cycles, _ := cycleCaptureEnv(t, `{"succeeded":1}`, terminalRunnerLog)
	cycleID := uuid.New().String()
	cycles.dispatched = []delivery.RunCycle{{
		ID: cycleID, OrgID: "acme", ProjectID: "shop", JobRef: "ca-val",
		Kind: delivery.CycleKindValidation,
	}}

	w.captureCycleLogs(context.Background())

	if _, ok := cycles.recorded[cycleID]; !ok {
		t.Fatalf("validation cycle usage not recorded; recorded = %+v", cycles.recorded)
	}
}

// A log with no terminal result (a pre-capture runner, or an agent killed before
// it reported) still captures the LOG — usage is best-effort and must never
// cost the run its history, nor write a bogus all-zero row.
func TestCaptureCycleLog_NoUsageInLogStillCapturesLog(t *testing.T) {
	w, cycles, logs := cycleCaptureEnv(t, `{"failed":1}`, "plain boot output, no NDJSON result\n")
	cycles.dispatched = []delivery.RunCycle{{
		ID: uuid.New().String(), OrgID: "acme", ProjectID: "shop", JobRef: "ca-dead",
		Kind: delivery.CycleKindCoding,
	}}

	w.captureCycleLogs(context.Background())

	if len(logs.created) != 1 {
		t.Fatalf("cycle logs created = %d, want 1 (the log is captured regardless)", len(logs.created))
	}
	if logs.created[0].FinalPhase != "Failed" {
		t.Fatalf("final phase = %q, want Failed", logs.created[0].FinalPhase)
	}
	if cycles.recordCalls != 0 {
		t.Fatalf("RecordUsage called %d times for a usage-less log, want 0", cycles.recordCalls)
	}
}

// A cycle whose Job is still running is left entirely alone: the live tail
// serves the stream, and stamping usage from a partial log would freeze a
// half-run's figures as the cycle's total.
func TestCaptureCycleLog_SkipsStillRunningJobs(t *testing.T) {
	w, cycles, logs := cycleCaptureEnv(t, `{"active":1}`, terminalRunnerLog)
	cycles.dispatched = []delivery.RunCycle{{
		ID: uuid.New().String(), OrgID: "acme", ProjectID: "shop", JobRef: "ca-live",
		Kind: delivery.CycleKindCoding,
	}}

	w.captureCycleLogs(context.Background())

	if len(logs.created) != 0 || cycles.recordCalls != 0 {
		t.Fatalf("a running job was captured: logs=%d recordUsage=%d", len(logs.created), cycles.recordCalls)
	}
}

// Capture is once-only: a cycle whose log is already stored is skipped whole, so
// a later tick cannot re-stamp usage (or duplicate the log row).
func TestCaptureCycleLog_SkipsAlreadyCapturedCycles(t *testing.T) {
	w, cycles, logs := cycleCaptureEnv(t, `{"succeeded":1}`, terminalRunnerLog)
	cycleID := uuid.New().String()
	cycles.dispatched = []delivery.RunCycle{{
		ID: cycleID, OrgID: "acme", ProjectID: "shop", JobRef: "ca-done",
		Kind: delivery.CycleKindCoding,
	}}
	logs.existing = map[string]*delivery.RunCycleLog{
		cycleID + "/ca-done": {RunName: "ca-done"},
	}

	w.captureCycleLogs(context.Background())

	if len(logs.created) != 0 || cycles.recordCalls != 0 {
		t.Fatalf("an already-captured cycle was re-captured: logs=%d recordUsage=%d",
			len(logs.created), cycles.recordCalls)
	}
}
