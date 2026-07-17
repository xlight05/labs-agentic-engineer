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
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// fakeComponentEnsurer records EnsureComponent calls and can be configured to
// fail — the synchronous pre-check PromoteAndExecute runs before promoting an
// ad-hoc issue into a Task.
type fakeComponentEnsurer struct {
	mu    sync.Mutex
	calls []string // "orgID/projectID/componentName"
	err   error
}

func (f *fakeComponentEnsurer) EnsureComponent(_ context.Context, orgID, projectID, componentName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("%s/%s/%s", orgID, projectID, componentName))
	return f.err
}

func (f *fakeComponentEnsurer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// adHocIssue is an SRE-agent-style issue: a plain human-authored body, no
// taskmeta block or labels yet — the state ae_create_issue leaves it in
// before ae_dispatch_coding_agent ever runs.
func adHocIssue(number int) sourcecontrol.IssueInfo {
	return sourcecontrol.IssueInfo{
		Number: number,
		Title:  "service1 times out waiting for service2",
		Body:   "## Root cause\nservice1 times out waiting for service2 after 5s.",
		State:  "open",
		URL:    fmt.Sprintf("https://github.com/o/r/issues/%d", number),
	}
}

func newCommandsWithEnsurer(issues *fakeIssues, disp *fakeDispatcher, ensurer *fakeComponentEnsurer) *Commands {
	return NewCommands(issues, fakeRepos{repo: defaultRepo()}, disp, ensurer)
}

func TestCommands_PromoteAndExecute_UnknownComponent_FailsSynchronously(t *testing.T) {
	issues := newFakeIssues()
	issues.seed(adHocIssue(42))
	disp := newFakeDispatcher()
	ensurer := &fakeComponentEnsurer{err: errors.New("resolve design component \"bogus\": not found")}

	err := newCommandsWithEnsurer(issues, disp, ensurer).PromoteAndExecute(
		context.Background(), "org1", "proj1", "bogus", 42)

	if err == nil {
		t.Fatal("expected an error for an unknown component, got nil")
	}
	if ensurer.callCount() != 1 {
		t.Errorf("expected exactly 1 EnsureComponent call, got %d", ensurer.callCount())
	}
	// The whole point: caught BEFORE the issue is touched or the funnel is
	// asked to dispatch — no partial promotion, no async goroutine started.
	if labels := issues.labelsOf(42); len(labels) != 0 {
		t.Errorf("issue should be untouched on a failed pre-check, got labels %v", labels)
	}
	if got := disp.executed(); len(got) != 0 {
		t.Errorf("dispatcher should never be reached on a failed pre-check, got %v", got)
	}
}

func TestCommands_PromoteAndExecute_BlankComponent_ReturnsComponentNameRequired(t *testing.T) {
	issues := newFakeIssues()
	issues.seed(adHocIssue(46))
	disp := newFakeDispatcher()
	ensurer := &fakeComponentEnsurer{}

	err := newCommandsWithEnsurer(issues, disp, ensurer).PromoteAndExecute(
		context.Background(), "org1", "proj1", "  ", 46)

	// A client input error (mapped to 400 at the edge), not a generic failure —
	// and caught before the pre-check or any issue mutation.
	if !errors.Is(err, ErrComponentNameRequired) {
		t.Fatalf("expected ErrComponentNameRequired, got %v", err)
	}
	if ensurer.callCount() != 0 {
		t.Errorf("pre-check should not run for a blank component, got %d calls", ensurer.callCount())
	}
	if got := disp.executed(); len(got) != 0 {
		t.Errorf("dispatcher should never be reached, got %v", got)
	}
}

func TestCommands_PromoteAndExecute_UnknownIssue_ReturnsTaskNotFound(t *testing.T) {
	issues := newFakeIssues() // nothing seeded → GetIssue returns ErrIssueNotFound
	disp := newFakeDispatcher()
	ensurer := &fakeComponentEnsurer{}

	err := newCommandsWithEnsurer(issues, disp, ensurer).PromoteAndExecute(
		context.Background(), "org1", "proj1", "service1", 404)

	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected ErrTaskNotFound for a missing issue, got %v", err)
	}
	if got := disp.executed(); len(got) != 0 {
		t.Errorf("dispatcher should never be reached, got %v", got)
	}
}

func TestCommands_PromoteAndExecute_KnownComponent_PromotesAndDispatches(t *testing.T) {
	issues := newFakeIssues()
	issues.seed(adHocIssue(43))
	disp := newFakeDispatcher()
	ensurer := &fakeComponentEnsurer{}

	err := newCommandsWithEnsurer(issues, disp, ensurer).PromoteAndExecute(
		context.Background(), "org1", "proj1", "service1", 43)
	if err != nil {
		t.Fatalf("PromoteAndExecute: %v", err)
	}
	if ensurer.callCount() != 1 {
		t.Errorf("expected exactly 1 EnsureComponent call, got %d", ensurer.callCount())
	}

	labels := issues.labelsOf(43)
	if !issueHasAll(labels, taskmeta.NewTaskLabels(taskmeta.ClassCoding, taskmeta.OriginIncident)) {
		t.Errorf("expected Task/coding/incident-origin labels, got %v", labels)
	}

	body := issues.bodyOf(43)
	block, human, err := taskmeta.ParseBody(body)
	if err != nil {
		t.Fatalf("promoted body did not carry a parseable machine block: %v", err)
	}
	if block.Component != "service1" || block.Origin != taskmeta.OriginIncident {
		t.Errorf("unexpected block: %+v", block)
	}
	if human.Body != adHocIssue(43).Body {
		t.Errorf("the original human-authored body should be preserved verbatim, got %q", human.Body)
	}

	select {
	case <-disp.signal:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher.OnExecuteIntent was not called")
	}
	if got := disp.executed(); len(got) != 1 || got[0] != 43 {
		t.Errorf("expected dispatch for issue 43, got %v", got)
	}
}

func TestCommands_PromoteAndExecute_NilEnsurer_SkipsPreCheck(t *testing.T) {
	issues := newFakeIssues()
	issues.seed(adHocIssue(44))
	disp := newFakeDispatcher()

	// components=nil (the pre-tasks-github-native default) must still work —
	// NewCommands' nil-safety, not just PromoteAndExecute's.
	err := NewCommands(issues, fakeRepos{repo: defaultRepo()}, disp, nil).PromoteAndExecute(
		context.Background(), "org1", "proj1", "service1", 44)
	if err != nil {
		t.Fatalf("PromoteAndExecute with nil ensurer: %v", err)
	}
	select {
	case <-disp.signal:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher.OnExecuteIntent was not called")
	}
}

func TestCommands_PromoteAndExecute_AlreadyPromoted_LeavesBlockAlone(t *testing.T) {
	issues := newFakeIssues()
	// Already has a machine block (e.g. a retried dispatch) — a different
	// component than we're about to pass, so a mismatch would be obvious.
	issues.seed(gitrepoIssue(45, "service1", ""))
	disp := newFakeDispatcher()
	ensurer := &fakeComponentEnsurer{}

	err := newCommandsWithEnsurer(issues, disp, ensurer).PromoteAndExecute(
		context.Background(), "org1", "proj1", "service1", 45)
	if err != nil {
		t.Fatalf("PromoteAndExecute: %v", err)
	}
	// The pre-check still runs every call (cheap, idempotent) even though the
	// issue was already promoted.
	if ensurer.callCount() != 1 {
		t.Errorf("expected the pre-check to still run, got %d calls", ensurer.callCount())
	}
	block, _, err := taskmeta.ParseBody(issues.bodyOf(45))
	if err != nil {
		t.Fatalf("block should still parse: %v", err)
	}
	if block.Component != "service1" {
		t.Errorf("existing block should be left alone, got component %q", block.Component)
	}
	select {
	case <-disp.signal:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher.OnExecuteIntent was not called")
	}
}
