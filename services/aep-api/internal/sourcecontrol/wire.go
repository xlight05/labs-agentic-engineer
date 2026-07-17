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
type CreateIssueRequest struct {
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Labels    []string `json:"labels,omitempty"`
	DedupeKey string   `json:"dedupeKey,omitempty"`
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
