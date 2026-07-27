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

// Same-package tests for the ONE rule the component tier cannot show, because
// the component tier only ever drives a finite stream: a LIVE run must not
// settle, and it must settle the moment — and only the moment — its row goes
// terminal. Positive assertions get a 1s deadline, negative ones a 50ms window,
// matching the task-stream hub tests.
package runread

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// flipRuns answers with a run whose state the test flips under a lock — the
// supervisor settling its row, seen through the reader.
type flipRuns struct {
	mu  sync.Mutex
	row delivery.MilestoneRun
}

func (f *flipRuns) set(state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row.State = state
}

func (f *flipRuns) GetByIDScoped(context.Context, string, string) (*delivery.MilestoneRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.row
	return &row, nil
}

func (f *flipRuns) MilestoneNumberForTag(context.Context, string, string, string) (int, bool, error) {
	return 0, false, nil
}

func (f *flipRuns) ListByMilestone(context.Context, string, string, int) ([]delivery.MilestoneRun, error) {
	return nil, nil
}

type noCycles struct{}

func (noCycles) ListByRun(context.Context, string, string) ([]delivery.RunCycle, error) {
	return nil, nil
}

// syncBuffer is the connection's writer, readable from the test goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// startStream drives the loop in a goroutine against a fast tick and returns
// the buffer plus a stop func.
func startStream(t *testing.T, runs *flipRuns) (*syncBuffer, context.CancelFunc) {
	t.Helper()
	svc := NewProgressService(runs, noCycles{}, nil)
	svc.tick = 5 * time.Millisecond
	svc.keepAlive = time.Hour // out of the way

	ctx, cancel := context.WithCancel(context.Background())
	run, err := svc.OpenRunProgressStream(ctx, "acme", "widgets", "r1")
	if err != nil {
		cancel()
		t.Fatalf("open: %v", err)
	}
	buf := &syncBuffer{}
	go run(buf, func() {})
	return buf, cancel
}

// waitFor polls for a substring, failing at the deadline.
func waitFor(t *testing.T, buf *syncBuffer, want string, deadline time.Duration) {
	t.Helper()
	until := time.After(deadline)
	for {
		if strings.Contains(buf.String(), want) {
			return
		}
		select {
		case <-until:
			t.Fatalf("timed out waiting for %q\n---\n%s", want, buf.String())
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// TestRunProgress_LiveRun_DoesNotSettle: a running run holds the stream open —
// only a TERMINAL run settles it. Negative assertion, 50ms window.
func TestRunProgress_LiveRun_DoesNotSettle(t *testing.T) {
	runs := &flipRuns{row: delivery.MilestoneRun{
		ID: "r1", OrgID: "acme", ProjectID: "widgets", State: delivery.RunStateRunning,
	}}
	buf, cancel := startStream(t, runs)
	defer cancel()

	time.Sleep(50 * time.Millisecond)
	if got := buf.String(); strings.Contains(got, "[DONE]") {
		t.Fatalf("a live run must not settle the stream\n---\n%s", got)
	}
}

// TestRunProgress_SettlesWhenTheRunGoesTerminal: the flip is what closes it, and
// the `done` frame carries the terminal state the console renders.
func TestRunProgress_SettlesWhenTheRunGoesTerminal(t *testing.T) {
	runs := &flipRuns{row: delivery.MilestoneRun{
		ID: "r1", OrgID: "acme", ProjectID: "widgets", State: delivery.RunStateWaiting,
	}}
	buf, cancel := startStream(t, runs)
	defer cancel()

	time.Sleep(20 * time.Millisecond)
	if strings.Contains(buf.String(), "[DONE]") {
		t.Fatal("a waiting run must not settle the stream")
	}
	runs.set(delivery.RunStateCancelled)

	waitFor(t, buf, `"type":"done"`, time.Second)
	waitFor(t, buf, "data: [DONE]", time.Second)
	if got := buf.String(); !strings.Contains(got, `"state":"cancelled"`) {
		t.Errorf("the done frame must carry the terminal state\n---\n%s", got)
	}
}

// TestEmitterOf_UnstampedIsMain pins the default: the runner marks only what it
// forwards from inside a Task tool call, so an unstamped line is a positive fact
// about the main agent, not an unknown.
func TestEmitterOf_UnstampedIsMain(t *testing.T) {
	if got := emitterOf(""); got != emitterMain {
		t.Errorf("emitterOf(\"\") = %q, want %q", got, emitterMain)
	}
	if got := emitterOf(emitterSubagent); got != emitterSubagent {
		t.Errorf("emitterOf(subagent) = %q", got)
	}
	// An unrecognised value is not invented into a new chip.
	if got := emitterOf("gremlin"); got != emitterMain {
		t.Errorf("emitterOf(unknown) = %q, want %q", got, emitterMain)
	}
}
