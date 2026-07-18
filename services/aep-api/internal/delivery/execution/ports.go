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

import (
	"context"

	"github.com/wso2/aep/aep-api/internal/delivery"

	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// The consumer ports the funnel / sweep / events drive. Each is the narrow
// slice of a larger service the funnel actually needs (the house consumer-port
// pattern). The concrete providers are wired at the composition root.

// IssueClient is the GitHub issue surface the funnel reads and stamps: list the
// project's Task issues, consume the aep:execute command label, and post
// attention comments. sourcecontrol.IssueService satisfies it.
type IssueClient interface {
	ListIssues(ctx context.Context, orgID, projectID string, labels []string) ([]sourcecontrol.IssueInfo, error)
	AddLabels(ctx context.Context, orgID, projectID string, number int, labels []string) error
	RemoveLabel(ctx context.Context, orgID, projectID string, number int, label string) error
	CommentIssue(ctx context.Context, orgID, projectID string, number int, body string) error
}

// RepoLookup resolves a GitHub "owner/name" to the owning org + project — the
// webhook delivers a repo full name; the funnel needs org+project to drive the
// org-scoped issue ops. Wired from repositories at the composition root.
type RepoLookup interface {
	ByFullName(ctx context.Context, fullName string) (orgID, projectID string, err error)
}

// PRReader reads a pull request's live state for the sweep's PR-state
// reconciliation (§5). sourcecontrol.IssueService satisfies it.
type PRReader interface {
	GetPullRequestState(ctx context.Context, orgID, projectID string, number int) (*sourcecontrol.PullRequestState, error)
}

// RepoRef identifies one project repository for the reconciliation sweep.
type RepoRef struct {
	OrgID     string
	ProjectID string
	FullName  string // "owner/name"
}

// RepoLister enumerates every project repository the sweep reconciles (§5
// disaster-recovery / missed-webhook pickup). Wired from repositories at the
// composition root. Optional — a nil lister limits the sweep to re-gating
// already-tracked executions.
type RepoLister interface {
	ListAll(ctx context.Context) ([]RepoRef, error)
}

// DesignReader re-verifies a Task against the design at HEAD at the moment of
// dispatch (§5): the funnel refuses to dispatch a coding Task whose component no
// longer exists in the design. Wired from the artifacts store.
type DesignReader interface {
	// ComponentNames returns the lowercased set of component names present in
	// the project's design at HEAD, or nil when the project has no design yet.
	ComponentNames(ctx context.Context, orgID, projectID string) (map[string]bool, error)
	// ProvisionDepNames returns, per component (lowercased name), the names of
	// that component's provisioning dependencies (external + platform-resource) —
	// the deps the funnel's dependency-kind-aware gate holds a consumer coding
	// task on until each dep's aep:provision issue derives deployed
	// (dependency-management §3.6). org-service deps are excluded (proceed-gated).
	// Returns nil when the project has no design yet.
	ProvisionDepNames(ctx context.Context, orgID, projectID string) (map[string][]string, error)
	// OrgServiceDepNames returns, per component (lowercased name), the names of
	// that component's cross-project org-service dependencies (issue #164, Task 4).
	// The funnel gates a consumer coding task on an org-service dep ONLY when a
	// consumer-side aep:provision visibility gate exists for it (an approved
	// unresolved dep at build); a resolved org-service has no gate and does NOT
	// block — so this is deliberately kept OUT of ProvisionDepNames (whose deps
	// block unconditionally). Returns nil when the project has no design yet.
	OrgServiceDepNames(ctx context.Context, orgID, projectID string) (map[string][]string, error)
}

// ExecutionStore is the executions rows repository (delivery.ExecutionRepository).
// Restated as a local port so the funnel depends on the verbs it drives.
type ExecutionStore interface {
	TryAdmit(ctx context.Context, e *delivery.Execution) (admitted bool, row *delivery.Execution, err error)
	Finish(ctx context.Context, id, status, reason string) (*delivery.Execution, error)
	LatestPerKind(ctx context.Context, repo string, issueNumber int) (map[string]*delivery.Execution, error)
	LatestPerKindForRepo(ctx context.Context, repo string) (map[int]map[string]*delivery.Execution, error)
	ListByIssue(ctx context.Context, repo string, issueNumber int) ([]delivery.Execution, error)
	ListActive(ctx context.Context) ([]delivery.Execution, error)
}

// Compile-time proof the concrete repository satisfies the port.
var _ ExecutionStore = (delivery.ExecutionRepository)(nil)
