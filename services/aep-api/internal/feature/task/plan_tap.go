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

package task

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/platform/taskplan"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// planDrainIdleTimeout aborts the upstream drain if no bytes arrive for this
// long. The agents service emits keep-alives ~every 15s, so a longer silence
// means a hung turn — and the drain holds the per-project plan lock (§6, released
// when Stream returns), so without this a hang pins plan_in_progress until a BFF
// restart.
const planDrainIdleTimeout = 90 * time.Second

// taskState is a Task's current machine block + human parts, tracked by the tap
// so an updateTask patch (which sets only the changed fields) recomposes the
// whole body from the latest known state.
type taskState struct {
	block taskmeta.Block
	human taskmeta.Human
}

// planTap streams the agents-service SSE verbatim to the client while parsing
// tool-result frames and performing the GitHub writes for planTask/updateTask
// as they pass (§6). It survives client disconnect: forwarding stops but reading
// continues, so the upstream turn drains to completion and every write lands.
//
// The tap acts on the self-contained tool-RESULT frame (output echoes the
// normalized fields), only when output.ok is true (phase-1 rule). Idempotency:
// before creating, it dedupes against the machine-block key of both the
// pre-existing Tasks and this-run creations (crash re-run safe).
type planTap struct {
	ctx       context.Context // detached — survives client disconnect (drain, §6)
	orgID     string
	projectID string // also the stable project identifier in the idempotency key
	specTag   string
	designTag string
	issues    IssueClient

	// state carries BOTH pre-existing Tasks (preloaded by the plan assembler,
	// addressable by updateTask{issueNumber}) and this-run creations.
	state        map[int]taskState
	existingKeys map[string]bool // machine-block keys of pre-existing Tasks (dedupe)

	// contextNumbers is the frozen set of issue numbers preloaded into the turn's
	// context (the pre-existing open aep:task issues). An updateTask{issueNumber}
	// ref MUST target a member: the agent only ever sees the numbers the assembler
	// pushed, so an out-of-context number is a hallucination (or a human bug
	// report that happens to share the id space) and must NEVER be written — doing
	// so would clobber an unrelated issue's body with a zero-value machine block.
	contextNumbers map[int]bool

	titleToNumber map[string]int  // normalized this-run title → created issue number
	createdKeys   map[string]bool // machine-block keys created this run (dedupe)

	// idleTimeout overrides planDrainIdleTimeout (tests set a small value). Zero
	// uses the default.
	idleTimeout time.Duration

	failures int
}

// streamPartFrame is the minimal shape of an agents-service SSE frame the tap
// reads: the raw StreamPart (type + the self-contained tool output).
type streamPartFrame struct {
	Type   string          `json:"type"`
	Output json.RawMessage `json:"output"`
}

// Stream forwards the upstream body to w verbatim while tapping tool frames —
// each line is consumed (GitHub write) before it is forwarded, so a delivered
// ok tool-result implies the corresponding issue write already landed. It
// closes body on return. Forwarding stops on the first client write error; the
// read loop continues so the upstream drains and all GitHub writes land (§6). An
// idle-read watchdog aborts the drain (closing body) if the upstream goes silent
// past the idle deadline, so a hung turn can't pin the per-project plan lock.
func (t *planTap) Stream(body io.ReadCloser, w io.Writer, flush func()) {
	defer body.Close()

	idle := t.idleTimeout
	if idle <= 0 {
		idle = planDrainIdleTimeout
	}
	// Watchdog: reset on every read; on expiry close body to unblock the pending
	// ReadBytes and end the drain. atomic flag distinguishes an idle-abort from a
	// clean EOF so it surfaces in the write-failure accounting.
	var idleAborted atomic.Bool
	activity := make(chan struct{}, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		timer := time.NewTimer(idle)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				return
			case <-activity:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(idle)
			case <-timer.C:
				idleAborted.Store(true)
				_ = body.Close()
				return
			}
		}
	}()

	reader := bufio.NewReader(body)
	clientAlive := true
	for {
		line, err := reader.ReadBytes('\n')
		// Reset the idle watchdog on any read return (bytes or a keep-alive line).
		select {
		case activity <- struct{}{}:
		default:
		}
		if len(line) > 0 {
			// Consume BEFORE forwarding: an ok tool-result frame reaching the
			// client means its GitHub write already landed, so the FE can
			// refresh its task list on that frame and see the issue (§8).
			t.consume(line)
			if clientAlive {
				if _, werr := w.Write(line); werr != nil {
					clientAlive = false
				} else {
					flush()
				}
			}
		}
		if err != nil {
			break
		}
	}
	if idleAborted.Load() {
		// Record the aborted drain so the terminal surface reports it; the plan
		// lock releases as PlanSession.Stream returns (its defer).
		t.failures++
		slog.WarnContext(t.ctx, "plan tap: upstream idle past deadline — drain aborted, plan lock released", "idleTimeout", idle)
	}
	// Terminal in-band surface of mid-stream write failures (§6, the OPEN item):
	// an SSE comment line (ignored by StreamPart readers) so the failure count is
	// visible in the raw stream / logs without corrupting the frame protocol.
	if t.failures > 0 && clientAlive {
		_, _ = fmt.Fprintf(w, ": aep-plan-write-failures %d\n\n", t.failures)
		flush()
	}
}

// consume parses one SSE line and, if it is a successful task tool-result,
// performs the corresponding GitHub write.
func (t *planTap) consume(line []byte) {
	data, ok := strings.CutPrefix(strings.TrimSpace(string(line)), "data:")
	if !ok {
		return
	}
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return
	}
	var frame streamPartFrame
	if err := json.Unmarshal([]byte(data), &frame); err != nil {
		return // partial / non-JSON / keep-alive
	}
	if frame.Type != "tool-result" || len(frame.Output) == 0 {
		return
	}
	ok, op, err := taskplan.ToolResultOK(frame.Output)
	if err != nil || !ok {
		return // skip ok:false and non-task results (self-correction, other tools)
	}
	switch op {
	case "plan":
		if out, derr := taskplan.DecodePlanTaskOk(frame.Output); derr == nil {
			t.handlePlan(out)
		}
	case "update":
		if out, derr := taskplan.DecodeUpdateTaskOk(frame.Output); derr == nil {
			t.handleUpdate(out)
		}
	}
}

// handlePlan creates an issue for a planTask result (deduped by machine-block key).
func (t *planTap) handlePlan(out *taskplan.PlanTaskOk) {
	origin := out.Origin
	if !origin.Valid() {
		origin = taskmeta.OriginSpecPlan
	}
	block, body := composePlannedIssue(t.projectID, plannedTask{
		Component: out.Component,
		Title:     out.Title,
		DependsOn: out.DependsOn,
		Origin:    origin,
		SpecTag:   t.specTag,
		DesignTag: t.designTag,
		Rationale: out.Rationale,
	})
	norm := normalizeTitle(out.Title)
	if t.existingKeys[block.Key] || t.createdKeys[block.Key] {
		return // already created (re-plan converges to no-ops, crash re-run safe)
	}
	if _, dup := t.titleToNumber[norm]; dup {
		return
	}
	labels := taskmeta.NewTaskLabels(taskmeta.ClassCoding, origin)
	// Stamp the build/spec version as a filterable label so Tasks can be listed
	// server-side by the version they were planned from (aep:spec/<tag>). The
	// same value is the block's structured specTag; the label is its flat mirror.
	if l := taskmeta.SpecTagLabel(t.specTag); l != "" {
		labels = append(labels, l)
	}
	issue, err := t.issues.CreateIssue(t.ctx, t.orgID, t.projectID, sourcecontrol.CreateIssueRequest{
		Title:  out.Title,
		Body:   body,
		Labels: labels,
	})
	if err != nil {
		// No issue exists to flag — record for the terminal in-band surface.
		t.failures++
		slog.WarnContext(t.ctx, "plan tap: create issue failed", "title", out.Title, "error", err)
		return
	}
	t.createdKeys[block.Key] = true
	t.titleToNumber[norm] = issue.Number
	t.state[issue.Number] = taskState{block: block, human: taskmeta.Human{Rationale: out.Rationale}}
}

// handleUpdate patches an existing or this-run Task for an updateTask result.
func (t *planTap) handleUpdate(out *taskplan.UpdateTaskOk) {
	number, ok := t.resolveRef(out.Ref)
	if !ok {
		// An out-of-context {issueNumber} is a real hazard, not a benign miss: it
		// would clobber an unrelated issue. Skip the op with NO GitHub write and
		// record it in the write-failure accounting (same surface as other tap
		// failures). A {title} miss is a benign skip (unknown this-run title).
		if out.Ref.IssueNumber != nil {
			t.failures++
			slog.WarnContext(t.ctx, "plan tap: updateTask ref out of context — skipped (would clobber an unrelated issue)", "issue", *out.Ref.IssueNumber)
		}
		return
	}
	st := t.state[number] // zero value is a benign empty block for a pre-existing miss

	set := out.Set
	if set.Title != nil && strings.TrimSpace(*set.Title) != "" {
		if err := t.issues.EditIssueTitle(t.ctx, t.orgID, t.projectID, number, *set.Title); err != nil {
			t.recordFlag(number, err)
		}
		// Remap the this-run title index: the echoed ref.title is the canonical
		// pre-rename title (phase-1); future {title} refs use the new title.
		if out.Ref.Title != nil {
			delete(t.titleToNumber, normalizeTitle(*out.Ref.Title))
		}
		t.titleToNumber[normalizeTitle(*set.Title)] = number
	}
	if set.DependsOn != nil {
		st.block.DependsOn = *set.DependsOn
	}
	if set.Rationale != nil {
		st.human.Rationale = *set.Rationale
	}
	if set.Body != nil {
		st.human.Body = *set.Body
	}
	body := recomposeBody(st.block, st.human.Rationale, st.human.Body)
	if err := t.issues.EditIssueBody(t.ctx, t.orgID, t.projectID, number, body); err != nil {
		t.recordFlag(number, err)
	}
	t.state[number] = st
}

// resolveRef resolves an updateTask ref to an issue number: {issueNumber} for a
// pre-existing Task, {title} for one planned earlier this run (§9.3). The agent
// never sees issue numbers it did not receive, so an {issueNumber} that is not a
// member of the preloaded context set is rejected (ok=false) — the tenant-scoped
// fence against clobbering an unrelated issue — and a {title} miss is skipped.
func (t *planTap) resolveRef(ref taskplan.TaskRef) (int, bool) {
	if ref.IssueNumber != nil {
		n := *ref.IssueNumber
		return n, t.contextNumbers[n]
	}
	if ref.Title != nil {
		n, ok := t.titleToNumber[normalizeTitle(*ref.Title)]
		return n, ok
	}
	return 0, false
}

// recordFlag records a mid-stream write failure and flags aep:attention on the
// affected issue (§6, the OPEN surfacing item — the minimal honest thing).
func (t *planTap) recordFlag(number int, err error) {
	t.failures++
	slog.WarnContext(t.ctx, "plan tap: update issue failed", "issue", number, "error", err)
	flagAttention(t.ctx, t.issues, t.orgID, t.projectID, number,
		"A plan update to this Task failed to apply. Re-run planning or edit it by hand.")
}

// normalizeTitle matches the agents-service title normalization (trim + lower)
// so this side's title→issue map keys identically to the echoed refs (§9.3).
func normalizeTitle(title string) string {
	return strings.ToLower(strings.TrimSpace(title))
}
