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

package sourcecontrol

import (
	"errors"
	"fmt"
	"strings"

	"github.com/wso2/aep/aep-api/internal/platform/gitfs"
)

// The request/response DTOs exchanged across the git-provider ports (ports.go).
// They are part of the port contract: any provider implementation
// (clients/github today) marshals its wire format to and from these types, and
// consumers (gitrepo services, orgcreds, task) read them. Kept
// provider-neutral — only the fields our code actually consumes.

// ----- Repo / issue -----

// CreateOrgRepoRequest maps to the fields we send to POST /orgs/{org}/repos.
//
// The owning org/user is derived from the Credential's RepoOwner() — the
// caller does not pass it explicitly, which keeps the multi-tenant invariant
// (repo creation is parametrised by the credential, not by ambient config).
type CreateOrgRepoRequest struct {
	Name        string
	Private     bool
	AutoInit    bool
	Description string
}

// CreateIssueRequest maps to the fields we send to POST /repos/{owner}/{repo}/issues.
//
// DedupeKey is aep-api-only and never reaches GitHub (issueService clears it
// before the GitHub call; omitempty keeps it off the wire). When set, issue
// creation is idempotent per open issue: the key is normalised into a
// `dedupe:<normalised-key>` label (lowercased, whitespace runs collapsed to
// "-", then truncated to GitHub's 50-char label limit — see dedupeLabelFor in
// issue_service.go), and if an OPEN issue carrying that label already exists
// the existing issue is returned instead of creating a duplicate. Because the
// label is a lossy transform of the raw key, callers cannot reconstruct the
// exact label name from the key alone. This is the correctness layer for
// callers that may fire concurrently for one incident (e.g. the OpenChoreo
// SRE/RCA handoff, one run per alert rule) — they pass a stable key like
// `sre-rca/<component>` so only the first run files an issue.
// This struct is marshalled straight onto the wire by the host adapter, so
// every field but DedupeKey is a GitHub field.
type CreateIssueRequest struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
	// Milestone assigns the issue to a milestone at creation time — one call
	// instead of create-then-patch, which is what keeps a plan's API cost at
	// 1+N. It is the milestone NUMBER; GitHub answers 422 to a title here. Nil
	// leaves the issue unassigned.
	Milestone *int   `json:"milestone,omitempty"`
	DedupeKey string `json:"dedupeKey,omitempty"`
}

// ----- Milestones -----

// A milestone is one spec version's delivery increment and ledger: the tag's
// issues (implementation, gate, validation, incident) join it over time.
//
// Number is the only stable key — titles are freely renamable, and while
// create-uniqueness is case-SENSITIVE the issues-list title filter is
// case-INSENSITIVE, so a case-twin pair would silently merge. Platform code
// therefore resolves by number and never matches on title.

// CreateMilestoneRequest maps to the fields we send to
// POST /repos/{owner}/{repo}/milestones.
type CreateMilestoneRequest struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// MilestoneResult is the outcome of a milestone create. Created reports whether
// this call minted the milestone; false means one with that title already
// existed (found by the case-insensitive pre-check, or recovered from GitHub's
// 422 already_exists) and Number refers to it. Either way the caller holds a
// usable number, which makes creation idempotent.
type MilestoneResult struct {
	Number  int
	Created bool
}

// Milestone is the subset of a GitHub milestone the platform reads. State is
// "open" | "closed" — display only: platform logic never branches on it, and
// closed milestones still accept new issues.
type Milestone struct {
	Number      int
	Title       string
	State       string
	Description string
	NodeID      string
}

// MilestoneIssuesFilter narrows a milestone's issue list.
// State is "open" | "closed" | "all" (empty ⇒ the host default, "open").
//
// Labels is AND-semantics — an issue must carry all of them. That is the REST
// endpoint behind this call. It does NOT generalise: the GraphQL query behind
// MilestoneIssueCounts filters on labels too, and there the argument is a
// UNION. Adding a label here narrows; adding one there widens.
type MilestoneIssuesFilter struct {
	Number int
	State  string
	Labels []string
}

// MilestoneIssueCounts is the run supervisor's dispatch predicate input: the
// OPEN-issue populations of one milestone, gathered in a single host round trip
// so the per-cycle-boundary predicate stays one call.
//
// A milestone holds three populations, told apart by label: agent work ("aep"),
// dispatch gates ("aep:provision") and ledger-only human issues (no "aep"). The
// run's WORKING SET is the first minus the gates and minus the validation issue
// — read it through OpenNonGateWork, never by subtracting fields by hand.
//
// The label kinds are NOT assumed disjoint: a gate may also carry "aep". The
// populations are therefore expressed as UNIONS, because the host's labels:
// argument is a union filter — an issue matches when it carries ANY of the
// listed labels, not all of them. A union filter cannot express an
// intersection, so the working set is taken as a set DIFFERENCE of two unions
// instead, which needs no intersection term at all.
//
// These are issue counts, never pull-request counts — the reason the predicate
// is a GraphQL query over milestone.issues rather than the REST milestone's
// open_issues field, which counts PRs too.
type MilestoneIssueCounts struct {
	// OpenProvision is every open gate, whether or not it also carries "aep".
	// One open gate holds the next dispatch.
	OpenProvision int
	// OpenTotal is every open issue in the milestone, ledger included. It says
	// whether the milestone is finished, not whether it is workable.
	OpenTotal int
	// OpenWorkOrExcluded is |"aep" ∪ "aep:provision" ∪ "aep:validation"|: every
	// open issue that is agent work or an exclusion from it.
	OpenWorkOrExcluded int
	// OpenExcluded is |"aep:provision" ∪ "aep:validation"|: the exclusions on
	// their own. Gates are never a coding cycle's work, and the validation issue
	// is the validation cycle's.
	OpenExcluded int
}

// OpenNonGateWork is the size of the run's working set: open, "aep"-labelled,
// not a gate, not the validation issue. It is the ONE place the exclusions are
// computed, so the dispatch predicate and any later settle check cannot drift
// apart on what "work" means.
//
// The set difference (A ∪ E) \ E, which is exactly the "aep" issues carrying
// neither exclusion label — exact even when an issue carries several label
// kinds at once, and without needing an intersection the host cannot count.
// Nil-tolerant: an unknown milestone has no work.
func (c *MilestoneIssueCounts) OpenNonGateWork() int {
	if c == nil {
		return 0
	}
	n := c.OpenWorkOrExcluded - c.OpenExcluded
	if n < 0 {
		// Unreachable against a consistent host: OpenExcluded counts a subset of
		// what OpenWorkOrExcluded counts. Clamped anyway so a host that answers
		// inconsistently degrades to "nothing to work" instead of inventing a
		// negative working set.
		return 0
	}
	return n
}

// IssueResult is the issue metadata returned after creation. Deduped reports
// that no issue was created because an open issue with the same DedupeKey
// already existed — Number/URL then refer to that existing issue (NodeID may
// be empty in that case; the list API doesn't return it).
type IssueResult struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	NodeID  string `json:"nodeId"`
	Deduped bool   `json:"deduped,omitempty"`
}

// IssueInfo represents an issue returned when listing.
type IssueInfo struct {
	Number int
	Title  string
	Body   string
	URL    string
	State  string
	Labels []string
}

// CompareResult is the per-file change summary between two refs the lineage
// diff consumes (§6) — produced by the Workspace engine's local
// `git diff base...head` (Workspace.Diff). Alias of the gitfs definition
// (identical fields). Truncated is always false there (a local diff never
// truncates); the field survives from the retired GitHub compare shape.
type CompareResult = gitfs.CompareResult

// ChangedFile is one entry of a compare's files[] list. Alias of the gitfs
// definition. Status vocabulary is GitHub-compatible: added | removed |
// modified | renamed | copied | changed | unchanged.
type ChangedFile = gitfs.ChangedFile

// ----- Account / App installation -----

// GitHubUser is the subset of GET /user we consume.
type GitHubUser struct {
	Login string `json:"login"`
	Name  string `json:"name"`
	Email string `json:"email"`
	ID    int64  `json:"id"`
}

// AppInstallationInfo is the subset of GET /app/installations/{id} we consume.
// account.login is the GitHub org/user the install belongs to; it can drift
// when the org is renamed on GitHub.
type AppInstallationInfo struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
	Suspended *string `json:"suspended_at,omitempty"`
}

// AppInstallationSummary is the flat projection of /app/installations[i]
// the discover endpoint returns to the BFF and console. Mirrors the wire
// shape used in the response (camelCase). Distinct from
// AppInstallationInfo (which preserves the nested account.* shape used
// by the validator's GetAppInstallation probe).
type AppInstallationSummary struct {
	InstallationID int64  `json:"installationId"`
	AccountLogin   string `json:"accountLogin"`
	AccountType    string `json:"accountType"`
}

// ----- Git identity -----

// GitIdentity mirrors a git author/committer/tagger identity. Date is
// optional (defaults to the commit/tag time when omitted). Named with the
// `Git` prefix to avoid collision with the `Identity` type already declared
// in credential_service.go. Alias of the gitfs definition — consumers keep
// importing sourcecontrol.GitIdentity while the engine owns the type.
type GitIdentity = gitfs.GitIdentity

// PullRequestState is the subset of a pull request the sweep's PR-state
// reconciliation reads (§5): open/closed + merged + the merge commit SHA.
type PullRequestState struct {
	State          string // "open" | "closed"
	Merged         bool
	MergeCommitSHA string
}

// ----- Status errors -----

// HTTPStatusError surfaces HTTP status codes from the git host client so the
// validator can branch on 401 / 404 / 410. Wraps the response body for
// debug logging at the call site.
type HTTPStatusError struct {
	StatusCode int
	Body       string
	URL        string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("github API %s: status %d: %s", e.URL, e.StatusCode, e.Body)
}

// IsHTTPStatus reports true when err is an HTTPStatusError with the given code.
func IsHTTPStatus(err error, code int) bool {
	var he *HTTPStatusError
	if errors.As(err, &he) {
		return he.StatusCode == code
	}
	return false
}

// GraphQLError carries the errors[] array of a GraphQL response. GraphQL
// answers 200 with a populated errors[] rather than an HTTP status, so this is
// the GraphQL analogue of HTTPStatusError: the whole array is preserved (not
// flattened to a first message) because the machine-readable Type is what
// callers branch on — NOT_FOUND for a stale milestone number is recoverable,
// RATE_LIMITED is retryable, anything else is a bug.
type GraphQLError struct {
	Errors []GraphQLErrorDetail
	// Query is the operation that failed, for debug logging at the call site.
	Query string
}

// GraphQLErrorDetail is one entry of a GraphQL response's errors[]. Path is the
// response path the error applies to; its elements are field names or list
// indices, hence any.
type GraphQLErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Path    []any  `json:"path"`
}

func (e *GraphQLError) Error() string {
	msgs := make([]string, 0, len(e.Errors))
	for _, d := range e.Errors {
		if d.Type != "" {
			msgs = append(msgs, d.Type+": "+d.Message)
			continue
		}
		msgs = append(msgs, d.Message)
	}
	return "github graphql error: " + strings.Join(msgs, "; ")
}

// IsGraphQLType reports true when err is a GraphQLError carrying at least one
// error of the given machine-readable type (e.g. "NOT_FOUND", "RATE_LIMITED").
//
// Retained infra, not a phase leftover: the milestone predicate already
// surfaces *GraphQLError, but no caller branches on its type yet (the plan path
// recovers a duplicate milestone through REST's 422 instead).
//
//deadcode:keep the typed discriminator for the GraphQL seam — see above.
func IsGraphQLType(err error, typ string) bool {
	var ge *GraphQLError
	if !errors.As(err, &ge) {
		return false
	}
	for _, d := range ge.Errors {
		if d.Type == typ {
			return true
		}
	}
	return false
}
