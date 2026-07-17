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
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

func newTestTap(issues IssueClient) *planTap {
	return &planTap{
		ctx:            context.Background(),
		orgID:          "org1",
		projectID:      "proj1",
		specTag:        "req-v1",
		designTag:      "design-v1",
		issues:         issues,
		state:          map[int]taskState{},
		existingKeys:   map[string]bool{},
		contextNumbers: map[int]bool{},
		titleToNumber:  map[string]int{},
		createdKeys:    map[string]bool{},
	}
}

func toolResult(output string) string {
	return fmt.Sprintf("data: {\"type\":\"tool-result\",\"output\":%s}\n\n", output)
}

func planOK(component, title string, deps []string) string {
	depsJSON := "[]"
	if len(deps) > 0 {
		depsJSON = `["` + strings.Join(deps, `","`) + `"]`
	}
	return fmt.Sprintf(`{"ok":true,"op":"plan","component":%q,"title":%q,"dependsOn":%s,"origin":"spec-plan","rationale":"do it"}`, component, title, depsJSON)
}

func updateByTitleBody(title, body string) string {
	return fmt.Sprintf(`{"ok":true,"op":"update","ref":{"title":%q},"set":{"body":%q}}`, title, body)
}

func stream(frames ...string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(strings.Join(frames, "")))
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("client gone") }

func TestPlanTap_PlanCreatesIssueWithLabelsAndBlock(t *testing.T) {
	issues := newFakeIssues()
	tap := newTestTap(issues)
	var buf bytes.Buffer

	tap.Stream(stream(
		toolResult(planOK("order-service", "Implement order-service", []string{"user-service"})),
		"data: [DONE]\n\n",
	), &buf, func() {})

	if len(issues.created) != 1 {
		t.Fatalf("expected 1 issue created, got %d", len(issues.created))
	}
	got := issues.created[0]
	// Labels: marker + class + origin + the spec-version lineage label.
	for _, want := range []string{taskmeta.LabelMarker, taskmeta.LabelCoding, taskmeta.OriginLabel(taskmeta.OriginSpecPlan), taskmeta.SpecTagLabel("req-v1")} {
		if !issueHasAll(got.Labels, []string{want}) {
			t.Errorf("expected label %q, got %v", want, got.Labels)
		}
	}
	// Body must carry a well-formed machine block with the component + lineage.
	block, _, err := taskmeta.ParseBody(got.Body)
	if err != nil {
		t.Fatalf("created body has no valid block: %v", err)
	}
	if block.Component != "order-service" || block.DesignTag != "design-v1" || block.SpecTag != "req-v1" {
		t.Errorf("block facts wrong: %+v", block)
	}
	if block.Key == "" {
		t.Errorf("expected idempotency key on created block")
	}
	// The stream must have been forwarded verbatim.
	if !strings.Contains(buf.String(), "[DONE]") {
		t.Errorf("stream not forwarded verbatim")
	}
}

// orderWriter snapshots the fake's created-count at the moment each line is
// forwarded to the client.
type orderWriter struct {
	issues        *fakeIssues
	createdAtLine []int
	buf           bytes.Buffer
}

func (w *orderWriter) Write(p []byte) (int, error) {
	w.createdAtLine = append(w.createdAtLine, len(w.issues.created))
	return w.buf.Write(p)
}

// The FE refreshes its task list the moment an ok tool-result frame arrives, so
// the tap MUST perform the GitHub write BEFORE forwarding that frame (§6/§8) —
// otherwise the row cannot materialize in the pending section on that refresh.
func TestPlanTap_WritesLandBeforeFrameIsForwarded(t *testing.T) {
	issues := newFakeIssues()
	tap := newTestTap(issues)
	w := &orderWriter{issues: issues}

	tap.Stream(stream(
		toolResult(planOK("order-service", "Implement order-service", nil)),
		"data: [DONE]\n\n",
	), w, func() {})

	if len(w.createdAtLine) == 0 {
		t.Fatal("nothing forwarded")
	}
	// Line 0 is the planTask tool-result: the issue must already exist.
	if w.createdAtLine[0] != 1 {
		t.Errorf("issue created AFTER its result frame was forwarded (created=%d at forward time)", w.createdAtLine[0])
	}
	if !strings.Contains(w.buf.String(), "[DONE]") {
		t.Errorf("stream not forwarded verbatim")
	}
}

func TestPlanTap_PlanNotOK_NoCreate(t *testing.T) {
	issues := newFakeIssues()
	tap := newTestTap(issues)
	var buf bytes.Buffer

	tap.Stream(stream(
		toolResult(`{"ok":false,"op":"plan","code":"UNKNOWN_COMPONENT","message":"nope","knownComponents":["a"]}`),
	), &buf, func() {})

	if len(issues.created) != 0 {
		t.Fatalf("ok:false planTask must not create an issue, got %d", len(issues.created))
	}
}

func TestPlanTap_UpdateByTitle_SetsBody(t *testing.T) {
	issues := newFakeIssues()
	tap := newTestTap(issues)
	var buf bytes.Buffer

	tap.Stream(stream(
		toolResult(planOK("order-service", "Implement order-service", nil)),
		toolResult(updateByTitleBody("Implement order-service", "## Scope\nWrite the order service.")),
		"data: [DONE]\n\n",
	), &buf, func() {})

	if len(issues.created) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues.created))
	}
	body := issues.bodyOf(100) // first created number
	if !strings.Contains(body, "Write the order service.") {
		t.Errorf("expected body updated via updateTask by title, got %q", body)
	}
	// The block must still be present and canonical after the body patch.
	if _, _, err := taskmeta.ParseBody(body); err != nil {
		t.Errorf("updated body lost its block: %v", err)
	}
}

func TestPlanTap_UpdateByIssueNumber_PreExisting(t *testing.T) {
	issues := newFakeIssues()
	issues.seed(gitrepoIssue(42, "user-service", "design-v1"))
	tap := newTestTap(issues)
	tap.state[42] = taskState{block: taskmeta.Block{Component: "user-service", Origin: taskmeta.OriginSpecPlan, DesignTag: "design-v1"}, human: taskmeta.Human{Rationale: "orig"}}
	tap.contextNumbers[42] = true // #42 was preloaded into the turn's context
	var buf bytes.Buffer

	tap.Stream(stream(
		toolResult(`{"ok":true,"op":"update","ref":{"issueNumber":42},"set":{"body":"## Scope\nnew scope","rationale":"revised"}}`),
		"data: [DONE]\n\n",
	), &buf, func() {})

	got := issues.bodyOf(42)
	if !strings.Contains(got, "new scope") || !strings.Contains(got, "revised") {
		t.Errorf("expected pre-existing issue patched, got %q", got)
	}
}

// TestPlanTap_UpdateByIssueNumber_OutOfContext_NoWrite pins the gate-review
// fence: an updateTask{issueNumber} pointing at an issue NOT preloaded into the
// turn's context (e.g. a human bug report sharing the id space) must NOT be
// written — no title/body edit, no attention label — and must be recorded in the
// write-failure accounting so the terminal surface reports it.
func TestPlanTap_UpdateByIssueNumber_OutOfContext_NoWrite(t *testing.T) {
	issues := newFakeIssues()
	// #999 exists on the repo but was never part of the plan context (unrelated).
	issues.seed(sourcecontrol.IssueInfo{Number: 999, Title: "Prod bug: checkout 500", Body: "Users can't check out.", State: "open"})
	tap := newTestTap(issues) // contextNumbers is empty → 999 is out of context
	var buf bytes.Buffer

	tap.Stream(stream(
		toolResult(`{"ok":true,"op":"update","ref":{"issueNumber":999},"set":{"body":"## Scope\nclobbered","title":"clobbered"}}`),
		"data: [DONE]\n\n",
	), &buf, func() {})

	// The unrelated issue is untouched — body, title, and labels unchanged.
	if got := issues.bodyOf(999); got != "Users can't check out." {
		t.Errorf("out-of-context issue body must be untouched, got %q", got)
	}
	if got := issues.byNumber[999].Title; got != "Prod bug: checkout 500" {
		t.Errorf("out-of-context issue title must be untouched, got %q", got)
	}
	if len(issues.labelsOf(999)) != 0 {
		t.Errorf("out-of-context issue must not be labeled, got %v", issues.labelsOf(999))
	}
	// The skipped op is recorded and surfaced.
	if tap.failures != 1 {
		t.Errorf("out-of-context update must be recorded as a write-failure, got %d", tap.failures)
	}
	if !strings.Contains(buf.String(), "aep-plan-write-failures 1") {
		t.Errorf("expected terminal in-band failure surface, got %q", buf.String())
	}
}

func TestPlanTap_Rename_RemapsTitleRef(t *testing.T) {
	issues := newFakeIssues()
	tap := newTestTap(issues)
	var buf bytes.Buffer

	tap.Stream(stream(
		toolResult(planOK("order-service", "Old title", nil)),
		// Rename via updateTask (ref.title is the canonical pre-rename title).
		toolResult(`{"ok":true,"op":"update","ref":{"title":"Old title"},"set":{"title":"New title"}}`),
		// A subsequent update addressing the NEW title must resolve.
		toolResult(updateByTitleBody("New title", "## Scope\nafter rename")),
		"data: [DONE]\n\n",
	), &buf, func() {})

	if issues.byNumber[100].Title != "New title" {
		t.Errorf("expected title renamed, got %q", issues.byNumber[100].Title)
	}
	if !strings.Contains(issues.bodyOf(100), "after rename") {
		t.Errorf("expected post-rename body update to resolve via the new title")
	}
}

func TestPlanTap_Dedupe_SamePlanTwice(t *testing.T) {
	issues := newFakeIssues()
	tap := newTestTap(issues)
	var buf bytes.Buffer

	tap.Stream(stream(
		toolResult(planOK("order-service", "Implement order-service", nil)),
		toolResult(planOK("order-service", "Implement order-service", nil)),
		"data: [DONE]\n\n",
	), &buf, func() {})

	if len(issues.created) != 1 {
		t.Fatalf("duplicate planTask (same key) must dedupe to one create, got %d", len(issues.created))
	}
}

func TestPlanTap_WriteFailure_FlagsAttention(t *testing.T) {
	issues := newFakeIssues()
	issues.seed(gitrepoIssue(42, "user-service", "design-v1"))
	tap := newTestTap(issues)
	tap.state[42] = taskState{block: taskmeta.Block{Component: "user-service", Origin: taskmeta.OriginSpecPlan, DesignTag: "design-v1"}}
	tap.contextNumbers[42] = true // in-context; the failure is at the GitHub write
	issues.failEditBody = true
	var buf bytes.Buffer

	tap.Stream(stream(
		toolResult(`{"ok":true,"op":"update","ref":{"issueNumber":42},"set":{"body":"x"}}`),
		"data: [DONE]\n\n",
	), &buf, func() {})

	if !issueHasAll(issues.labelsOf(42), []string{taskmeta.LabelAttention}) {
		t.Errorf("expected aep:attention flagged on write failure, got %v", issues.labelsOf(42))
	}
	if tap.failures != 1 {
		t.Errorf("expected 1 recorded failure, got %d", tap.failures)
	}
	if !strings.Contains(buf.String(), "aep-plan-write-failures 1") {
		t.Errorf("expected terminal in-band failure surface, got %q", buf.String())
	}
}

func TestPlanTap_DrainOnDisconnect(t *testing.T) {
	issues := newFakeIssues()
	tap := newTestTap(issues)

	// The client writer errors immediately (disconnect), but the tap must keep
	// reading upstream and perform the GitHub write.
	tap.Stream(stream(
		toolResult(planOK("order-service", "Implement order-service", nil)),
		"data: [DONE]\n\n",
	), failWriter{}, func() {})

	if len(issues.created) != 1 {
		t.Fatalf("tap must drain and create the issue even after client disconnect, got %d", len(issues.created))
	}
}

// hangingBody blocks on Read until Close is called (a hung agents turn sending
// no bytes / keep-alives), then returns EOF — the idle watchdog's Close unblocks it.
type hangingBody struct {
	closed chan struct{}
	once   sync.Once
}

func newHangingBody() *hangingBody { return &hangingBody{closed: make(chan struct{})} }

func (b *hangingBody) Read([]byte) (int, error) {
	<-b.closed
	return 0, io.EOF
}
func (b *hangingBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

// TestPlanTap_IdleDeadline_AbortsHungDrain pins the gate-review idle-read
// deadline: a turn that goes silent past the idle timeout must abort the drain
// (so the per-project plan lock releases) rather than block forever, and record
// the abort in the write-failure accounting.
func TestPlanTap_IdleDeadline_AbortsHungDrain(t *testing.T) {
	issues := newFakeIssues()
	tap := newTestTap(issues)
	tap.idleTimeout = 20 * time.Millisecond
	var buf bytes.Buffer

	done := make(chan struct{})
	go func() {
		tap.Stream(newHangingBody(), &buf, func() {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stream did not abort a hung drain — the plan lock would be pinned")
	}
	if tap.failures != 1 {
		t.Errorf("an idle-aborted drain must be recorded as a write-failure, got %d", tap.failures)
	}
	if !strings.Contains(buf.String(), "aep-plan-write-failures 1") {
		t.Errorf("expected terminal in-band failure surface, got %q", buf.String())
	}
}

// gitrepoIssue builds a seeded pre-existing Task issue with a valid block.
func gitrepoIssue(number int, component, designTag string) sourcecontrol.IssueInfo {
	block := taskmeta.Block{Component: component, Origin: taskmeta.OriginSpecPlan, DesignTag: designTag}
	body := taskmeta.ComposeBody(block, taskmeta.Human{Rationale: "orig"})
	return sourcecontrol.IssueInfo{
		Number: number,
		Title:  "Implement " + component,
		Body:   body,
		State:  "open",
		URL:    fmt.Sprintf("https://github.com/o/r/issues/%d", number),
		Labels: taskmeta.NewTaskLabels(taskmeta.ClassCoding, taskmeta.OriginSpecPlan),
	}
}

// taggedIssue builds a seeded Task issue stamped with a spec version, exactly as
// plan_tap writes it: the aep:spec/<tag> label plus the specTag in its machine
// block.
func taggedIssue(number int, component, specTag string) sourcecontrol.IssueInfo {
	block := taskmeta.Block{Component: component, Origin: taskmeta.OriginSpecPlan, SpecTag: specTag, DesignTag: "design-v1"}
	body := taskmeta.ComposeBody(block, taskmeta.Human{Rationale: "orig"})
	labels := append(taskmeta.NewTaskLabels(taskmeta.ClassCoding, taskmeta.OriginSpecPlan), taskmeta.SpecTagLabel(specTag))
	return sourcecontrol.IssueInfo{
		Number: number,
		Title:  "Implement " + component,
		Body:   body,
		State:  "open",
		URL:    fmt.Sprintf("https://github.com/o/r/issues/%d", number),
		Labels: labels,
	}
}
