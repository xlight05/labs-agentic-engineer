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

package gitrepo

import (
	"errors"

	"github.com/wso2/aep/aep-api/internal/platform/gitfs"
)

// Sentinel errors. Workspace.Mutate and the artifacts tag loop key off
// ErrRefNotFastForward and ErrTagAlreadyExists to drive CAS/tag-collision
// retries, and repoService keys off ErrRepoNameConflict (a Host impl —
// clients/github today — MUST reproduce it; IsRepoNameConflict /
// IsHTTPStatus (wire.go) are the caller-side checks).
//
// ErrTagAlreadyExists / ErrRefNotFastForward are aliases of the gitfs values:
// the message strings
// survived the REST→mount migration unchanged, so `errors.Is` checks and log
// lines stayed stable.
var (
	// ErrRepoNotFound is a gitrepo-domain error (no repo row) returned by
	// repoService/issueService lookups.
	ErrRepoNotFound = errors.New("repository not found")
	// ErrRepoNotReady is a gitrepo-domain error (repo row not in "ready" state).
	ErrRepoNotReady = errors.New("repository is not ready")
	// ErrIssueNotFound is returned by GetIssue when no issue with the given
	// number exists on the repo (the host answered 404). Callers map it to
	// their own not-found (e.g. task.ErrTaskNotFound → 404).
	ErrIssueNotFound = errors.New("issue not found")

	// ErrTagAlreadyExists — Workspace.Tag (taken name / rejected push)
	// returns it so the save flow recomputes the next tag.
	// Message: "tag already exists".
	ErrTagAlreadyExists = gitfs.ErrTagAlreadyExists
	// ErrRefNotFastForward — Workspace.Mutate (push-lease rejection, retries
	// exhausted) returns it when the ref tip moved between read and write so
	// the caller re-anchors. Message: "github ref: update is not a
	// fast-forward" (historical wording kept for log-line stability).
	ErrRefNotFastForward = gitfs.ErrRefNotFastForward
	// ErrRepoNameConflict — port contract: CreateOrgRepo returns it when the
	// requested repo name is already taken (repoService retries with a fresh suffix).
	ErrRepoNameConflict = errors.New("repo name already taken")
)

// IsRepoNameConflict reports whether err represents a host name-conflict rejection.
func IsRepoNameConflict(err error) bool {
	return errors.Is(err, ErrRepoNameConflict)
}
