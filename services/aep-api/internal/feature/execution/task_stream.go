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

package execution

// task_stream.go — the task-log SSE endpoint (contract: GET
// /projects/{p}/tasks/{issueNumber}/log). One connection carries a Task's
// ENTIRE live state: a `task` frame (the TaskView, upserted), one `execution`
// frame per attempt (upserted), and a `line` frame per unified-timeline entry
// (deduped), ending in `done` + `[DONE]` when the Task settles. Frame types are
// carried INSIDE the JSON payload (`type` field), never as an SSE `event:` name
// — the console's shared parser keeps only `data:` lines, so a self-describing
// payload (like the agents turn stream) is what it can read.
//
// Lines are derived from the SAME per-kind sources as the retired cursor-poll
// (reused verbatim via ProgressService.GetProgress): a running coding pod tail
// or its captured snapshot, a build's WorkflowRun steps. A lightweight in-proc
// hub (instant wake on lifecycle transitions) plus a slow re-derive tick (the
// safety net) drive live updates — no durable event table (§Backend).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/wso2/aep/aep-api/internal/contracts"
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/models"
)

const (
	// taskStreamLineTick re-derives the running execution's lines (DB + cluster
	// proxy / OC — never GitHub) so live logs stream without waiting on a hub
	// notify. Fast, because it hits no rate-limited API.
	taskStreamLineTick = 2 * time.Second
	// taskStreamSnapshotTick re-derives the Task snapshot (a GitHub read) as a
	// SAFETY net only — the hub notify already re-derives it on every lifecycle
	// transition, so this slow tick just covers a missed notify without hammering
	// GitHub every couple of seconds.
	taskStreamSnapshotTick = 8 * time.Second
	// taskStreamKeepAlive paces `: keep-alive` comments so proxies keep an idle
	// stream open and a dead client is noticed.
	taskStreamKeepAlive = 15 * time.Second
)

// TaskSnapshot is the `task` frame's payload (the full TaskView JSON, forwarded
// verbatim — the stream never unmarshals it) plus the derived status the stream
// uses to decide when the Task has settled.
type TaskSnapshot struct {
	JSON          json.RawMessage
	DerivedStatus string
}

// TaskSnapshotReader reads a Task's current snapshot. Wired at the composition
// root over feature/task — execution never imports it (§1 split). Returns nil
// when the issue is not a Task (the stream answers 404 before opening).
type TaskSnapshotReader interface {
	TaskSnapshot(ctx context.Context, orgID, projectID string, issueNumber int) (*TaskSnapshot, error)
}

// ExecutionHistory lists a Task's execution rows (every attempt, oldest first)
// so the stream can walk them into one chronological timeline. Wired at the
// composition root over the executions store + repo lookup.
type ExecutionHistory interface {
	ByIssue(ctx context.Context, orgID, projectID string, issueNumber int) ([]models.Execution, error)
}

// TaskStreamRepoLookup resolves a project's "owner/name" — the hub key half the
// notifier writers stamp. Satisfied by the composition-root repo adapter.
type TaskStreamRepoLookup interface {
	RepoFullName(ctx context.Context, orgID, projectID string) (string, error)
}

// TaskStreamService serves the task-log SSE stream. It composes the existing
// per-execution ProgressService (line derivation, unchanged) with the durable
// task/execution reads and the change hub.
type TaskStreamService struct {
	progress *ProgressService
	tasks    TaskSnapshotReader
	execs    ExecutionHistory
	repos    TaskStreamRepoLookup
	hub      *TaskStreamHub
}

// NewTaskStreamService wires the stream service.
func NewTaskStreamService(progress *ProgressService, tasks TaskSnapshotReader, execs ExecutionHistory, repos TaskStreamRepoLookup, hub *TaskStreamHub) *TaskStreamService {
	return &TaskStreamService{progress: progress, tasks: tasks, execs: execs, repos: repos, hub: hub}
}

// streamFrame is one SSE `data:` payload, discriminated by Type.
type streamFrame struct {
	Type          string                   `json:"type"` // task | execution | line | done
	Task          json.RawMessage          `json:"task,omitempty"`
	Execution     *execView                `json:"execution,omitempty"`
	Line          *contracts.TimelineEvent `json:"line,omitempty"`
	DerivedStatus string                   `json:"derivedStatus,omitempty"`
}

// execView projects a models.Execution to the ExecutionView JSON shape the
// console already holds (kept here so execution never imports feature/task).
type execView struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Status    string     `json:"status"`
	RunName   string     `json:"runName,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

// run is one connection's lifetime: an initial full derive (task → executions →
// lines), then a live loop that re-derives on a hub notify or a tick and closes
// with `done` when the Task settles. The GitHub-backed task snapshot re-derives
// only on notify + the slow safety tick; the fast line tick touches DB/proxy
// only. All state (dedup/cursors) is per-connection; a reconnect re-derives from
// scratch and the client dedups.
func (s *TaskStreamService) run(ctx context.Context, w io.Writer, flush func(), orgID, projectID, repo string, issue int, first *TaskSnapshot) {
	seq := 0
	writeFrame := func(f *streamFrame) bool {
		b, err := json.Marshal(f)
		if err != nil {
			return true // skip one bad frame, keep the stream alive
		}
		if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", seq, b); err != nil {
			return false // client gone
		}
		seq++
		flush()
		return true
	}
	writeDone := func(derived string) {
		_ = writeFrame(&streamFrame{Type: "done", DerivedStatus: derived})
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flush()
	}

	// per-connection dedup + cursor state
	var lastTaskJSON []byte
	lastExecJSON := map[string]string{}
	codingCursor := map[string]int64{}            // execID → last emitted ts millis (coding tail)
	seenBuildStep := map[string]map[string]bool{} // execID → step|status → emitted (build re-emits all steps each poll)
	lastDerived := ""

	// deriveTask fetches the Task snapshot (a GitHub read) and emits the `task`
	// frame on change. Returns alive (false ⇒ client gone). A transient read
	// failure keeps the stream and retries next tick.
	deriveTask := func(snap *TaskSnapshot) bool {
		if snap == nil {
			fresh, err := s.tasks.TaskSnapshot(ctx, orgID, projectID, issue)
			if err != nil || fresh == nil {
				return true
			}
			snap = fresh
		}
		lastDerived = snap.DerivedStatus
		if !bytes.Equal(lastTaskJSON, snap.JSON) {
			lastTaskJSON = snap.JSON
			return writeFrame(&streamFrame{Type: "task", Task: snap.JSON})
		}
		return true
	}

	// deriveExecs fetches the execution rows (DB) + their new lines (proxy/OC)
	// and emits changed `execution` + new `line` frames. Returns (anyRunning,
	// alive).
	deriveExecs := func() (anyRunning bool, alive bool) {
		execs, err := s.execs.ByIssue(ctx, orgID, projectID, issue)
		if err != nil {
			return false, true // keep the stream; retry
		}
		for i := range execs {
			e := &execs[i]
			ev := toExecView(e)
			evJSON, _ := json.Marshal(ev)
			if lastExecJSON[e.ID] != string(evJSON) {
				lastExecJSON[e.ID] = string(evJSON)
				if !writeFrame(&streamFrame{Type: "execution", Execution: ev}) {
					return anyRunning, false
				}
			}
			if !taskmeta.ExecutionStatus(e.Status).IsTerminal() {
				anyRunning = true
			}
			if !s.emitLines(ctx, orgID, e, codingCursor, seenBuildStep, writeFrame) {
				return anyRunning, false
			}
		}
		return anyRunning, true
	}

	// refresh re-derives (optionally the GitHub task snapshot) and closes the
	// stream with `done` when the Task has settled. Returns false when the loop
	// must stop — either the Task settled (done written) or the client is gone.
	refresh := func(withTask bool) bool {
		if withTask && !deriveTask(nil) {
			return false
		}
		anyRunning, alive := deriveExecs()
		if !alive {
			return false
		}
		if isTaskSettled(lastDerived) && !anyRunning {
			writeDone(lastDerived)
			return false
		}
		return true
	}

	// Initial full derive (reuses the pre-stream snapshot — no double GitHub read).
	if !deriveTask(first) {
		return
	}
	anyRunning, alive := deriveExecs()
	if !alive {
		return
	}
	if isTaskSettled(lastDerived) && !anyRunning {
		writeDone(lastDerived)
		return
	}

	notify, cancel := s.hub.Subscribe(repo, issue)
	defer cancel()
	lineTick := time.NewTicker(taskStreamLineTick)
	defer lineTick.Stop()
	snapTick := time.NewTicker(taskStreamSnapshotTick)
	defer snapTick.Stop()
	keepAlive := time.NewTicker(taskStreamKeepAlive)
	defer keepAlive.Stop()

	for {
		select {
		case <-ctx.Done():
			return // client disconnected
		case <-keepAlive.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flush()
		case <-notify:
			if !refresh(true) { // a lifecycle transition — re-read the task too
				return
			}
		case <-snapTick.C:
			if !refresh(true) { // safety: catch a missed notify
				return
			}
		case <-lineTick.C:
			if !refresh(false) { // fast path — lines only, no GitHub read
				return
			}
		}
	}
}

// emitLines pulls one execution's NEW timeline lines (via the reused
// per-execution ProgressService) and writes them, attributed to the execution.
// Coding lines advance a ts cursor (incremental); build steps are re-emitted in
// full each poll, so they are deduped by step+status. A source hiccup degrades
// to no new lines — never kills the stream.
func (s *TaskStreamService) emitLines(ctx context.Context, orgID string, e *models.Execution, codingCursor map[string]int64, seenBuildStep map[string]map[string]bool, writeFrame func(*streamFrame) bool) bool {
	if s.progress == nil {
		return true
	}
	resp, err := s.progress.GetProgress(ctx, orgID, e.ID, codingCursor[e.ID])
	if err != nil || resp == nil {
		return true
	}
	for i := range resp.Lines {
		ln := resp.Lines[i]
		if ln.Kind == "build_step" {
			key := ln.Step + "|" + ln.Status
			m := seenBuildStep[e.ID]
			if m == nil {
				m = map[string]bool{}
				seenBuildStep[e.ID] = m
			}
			if m[key] {
				continue
			}
			m[key] = true
		}
		if !writeFrame(&streamFrame{Type: "line", Line: &contracts.TimelineEvent{
			ProgressEvent: ln,
			ExecutionID:   e.ID,
			ExecutionKind: e.Kind,
		}}) {
			return false
		}
	}
	if resp.CursorMillis > codingCursor[e.ID] {
		codingCursor[e.ID] = resp.CursorMillis
	}
	return true
}

func toExecView(e *models.Execution) *execView {
	return &execView{
		ID:        e.ID,
		Kind:      e.Kind,
		Status:    e.Status,
		RunName:   e.RunName,
		Reason:    e.Reason,
		CreatedAt: e.CreatedAt,
		StartedAt: e.StartedAt,
		EndedAt:   e.EndedAt,
	}
}

// isTaskSettled reports when the stream may send `done` and close: ONLY a
// deployed Task is truly finished.
//
// "abandoned" is deliberately NOT settled. During the normal merge→build
// handoff the issue auto-closes (GitHub closes it on a "Closes #N" merge) a
// beat BEFORE the build Execution row is admitted, so the Task derives a
// TRANSIENT "abandoned" (closed issue + a PR that still looks open, because no
// build row exists yet) for that ~2s window. Closing on it froze the detail
// page on "abandoned" and dropped the build/OC logs that appeared moments
// later. Every non-deployed status keeps the stream open (as failed/rejected
// already did): a transient abandoned then advances to building→deployed and
// closes there, and a genuinely abandoned Task just holds a cheap open stream
// until the tab closes.
func isTaskSettled(derived string) bool {
	return taskmeta.DerivedStatus(derived) == taskmeta.StatusDeployed
}
