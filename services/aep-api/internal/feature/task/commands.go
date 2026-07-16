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
	"log/slog"
	"strings"
	"time"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/feature/gitrepo"
	"github.com/wso2/aep/aep-api/internal/platform/async"
)

// Commands is the write surface for the reactive control labels (§5): execute
// (edge-triggered, consumed by the funnel) and hold (level-triggered). The
// effect always flows through the funnel — there is no imperative dispatch path.
//
// Echo-suppression subtlety (§9.2): a platform-stamped aep:execute fires an
// issues.labeled delivery whose sender IS the platform, so the webhook path
// drops it. The console Execute button therefore stamps the label for the audit
// timeline AND calls INTO the funnel directly (the Dispatcher port) — both are
// the SAME single dispatch path; external actors stamping the label in the
// GitHub UI reach the funnel via the (non-dropped) webhook instead.
type Commands struct {
	issues     IssueClient
	repos      RepoResolver
	dispatcher Dispatcher
	components ComponentEnsurer
}

// NewCommands wires the command surface. components may be nil — the only
// caller that needs it is PromoteAndExecute's pre-check, which degrades to
// skipping that check (matching pre-tasks-github-native behavior where no
// such check existed at all) rather than failing every promote-from-issue
// call when it's unset.
func NewCommands(issues IssueClient, repos RepoResolver, dispatcher Dispatcher, components ComponentEnsurer) *Commands {
	return &Commands{issues: issues, repos: repos, dispatcher: dispatcher, components: components}
}

// Execute stamps aep:execute (audit) and dispatches through the funnel. Returns
// ErrTaskNotFound for a non-Task issue and ErrIssueClosed for a closed issue
// (closed = no new dispatches, §4). Idempotent — a second Execute while an
// Execution is active is a no-op inside the funnel.
func (c *Commands) Execute(ctx context.Context, orgID, projectID string, issueNumber int) error {
	repoFullName, issueState, err := c.resolveTaskIssue(ctx, orgID, projectID, issueNumber)
	if err != nil {
		return err
	}
	if !strings.EqualFold(issueState, "open") {
		return ErrIssueClosed
	}
	// Stamp for the audit timeline (best-effort — the funnel consumes it).
	if err := c.issues.AddLabels(ctx, orgID, projectID, issueNumber, []string{taskmeta.LabelExecute}); err != nil {
		slog.WarnContext(ctx, "execute: stamp aep:execute failed", "issue", issueNumber, "error", err)
	}
	// Dispatch through the funnel out-of-band (the effect is async, §9.1 202).
	c.dispatchAsync(func(bg context.Context) error {
		return c.dispatcher.OnExecuteIntent(bg, repoFullName, issueNumber)
	}, "execute dispatch", issueNumber)
	return nil
}

// Hold stamps aep:hold (level-triggered). Idempotent (204).
func (c *Commands) Hold(ctx context.Context, orgID, projectID string, issueNumber int) error {
	if _, _, err := c.resolveTaskIssue(ctx, orgID, projectID, issueNumber); err != nil {
		return err
	}
	return c.issues.AddLabels(ctx, orgID, projectID, issueNumber, []string{taskmeta.LabelHold})
}

// Unhold removes aep:hold and re-evaluates the funnel so any Execution queued
// behind the hold can dispatch (the unlabel webhook is dropped by echo
// suppression, so the release must trigger re-evaluation directly). Idempotent.
func (c *Commands) Unhold(ctx context.Context, orgID, projectID string, issueNumber int) error {
	if _, _, err := c.resolveTaskIssue(ctx, orgID, projectID, issueNumber); err != nil {
		return err
	}
	if err := c.issues.RemoveLabel(ctx, orgID, projectID, issueNumber, taskmeta.LabelHold); err != nil {
		return err
	}
	c.dispatchAsync(func(bg context.Context) error {
		return c.dispatcher.Reevaluate(bg)
	}, "unhold reevaluate", issueNumber)
	return nil
}

// PromoteAndExecute turns an ad-hoc GitHub issue — one created outside the
// spec-plan pipeline, e.g. by the OpenChoreo SRE/RCA agent handoff (see
// AE-HANDOFF-DESIGN.md in openchoreo/agents/sre-agent) — into a dispatchable
// coding Task, then dispatches it through Execute. Unlike a planned Task, the
// issue already exists with a human-authored body by the time this runs (the
// caller files it via the plain create-issue operation first); this only
// injects the taskmeta machine block ahead of that body and stamps the
// Task/coding/incident-origin labels, preserving everything the caller wrote.
//
// componentName must name a component AE already knows about. The funnel's
// coding executor enforces this too, but only inside its async goroutine
// (Execute returns 202 before it runs) — an unknown name would otherwise fail
// silently from this call's point of view. Checking it here, synchronously,
// surfaces that failure back to the caller instead.
// Idempotent: if the issue was already promoted (e.g. a retried dispatch),
// the existing block/labels are left alone and this just re-issues Execute.
func (c *Commands) PromoteAndExecute(ctx context.Context, orgID, projectID, componentName string, issueNumber int) error {
	if strings.TrimSpace(componentName) == "" {
		return ErrComponentNameRequired
	}
	if c.components != nil {
		if err := c.components.EnsureComponent(ctx, orgID, projectID, componentName); err != nil {
			return fmt.Errorf("promote task from issue: %w", err)
		}
	}

	// Fetch the one issue by number (O(1)) rather than paging the whole repo —
	// ListIssues stops at 100, so a scan would silently miss issues beyond that
	// on a busy repo.
	issue, err := c.issues.GetIssue(ctx, orgID, projectID, issueNumber)
	if err != nil {
		if errors.Is(err, gitrepo.ErrIssueNotFound) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("promote task from issue: get issue: %w", err)
	}

	if _, parseErr := taskmeta.ParseBlock(issue.Body); parseErr != nil {
		block := taskmeta.Block{Component: componentName, Origin: taskmeta.OriginIncident}
		body := taskmeta.ComposeBody(block, taskmeta.Human{Body: issue.Body})
		if err := c.issues.EditIssueBody(ctx, orgID, projectID, issueNumber, body); err != nil {
			return fmt.Errorf("promote task from issue: inject machine block: %w", err)
		}
		labels := taskmeta.NewTaskLabels(taskmeta.ClassCoding, taskmeta.OriginIncident)
		if err := c.issues.AddLabels(ctx, orgID, projectID, issueNumber, labels); err != nil {
			return fmt.Errorf("promote task from issue: add task labels: %w", err)
		}
	}

	return c.Execute(ctx, orgID, projectID, issueNumber)
}

// resolveTaskIssue resolves the repo full name and finds the Task issue by
// number, returning its GitHub state plus ErrProjectRepoNotFound / ErrTaskNotFound
// as appropriate.
func (c *Commands) resolveTaskIssue(ctx context.Context, orgID, projectID string, issueNumber int) (repoFullName, issueState string, err error) {
	repoFullName, err = resolveRepoFullName(ctx, c.repos, orgID, projectID)
	if err != nil {
		return "", "", err
	}

	issues, err := c.issues.ListIssues(ctx, orgID, projectID, []string{taskmeta.LabelMarker})
	if err != nil {
		return "", "", err
	}
	for i := range issues {
		if issues[i].Number == issueNumber {
			return repoFullName, issues[i].State, nil
		}
	}
	return "", "", ErrTaskNotFound
}

// dispatchAsync runs a funnel call out-of-band with a bounded detached context
// so it survives the request return (the effect is async, §9.1). async.Go adds
// the panic barrier so a panic in the funnel path fails just this dispatch, not
// the process.
func (c *Commands) dispatchAsync(fn func(context.Context) error, what string, issueNumber int) {
	async.Go(context.Background(), "task command "+what, func(context.Context) {
		bg, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 2*time.Minute)
		defer cancel()
		if err := fn(bg); err != nil && !errors.Is(err, context.Canceled) {
			slog.WarnContext(bg, "task command "+what+" failed", "issue", issueNumber, "error", err)
		}
	})
}
