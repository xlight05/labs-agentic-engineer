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
	"testing"

	"github.com/wso2/aep/aep-api/models"
)

// The on-disk leaf for the per-org skills repo is pinned to
// models.SkillsRepoDirName ("org-skills"), NOT the row's owner-prefixed
// repo_slug. The agents service derives the skills snapshot path structurally
// from that fixed name (snapshot-path.ts) and never receives a slug for it on
// the wire — an owner-prefixed leaf here means every turn dispatch 400s with
// "unknown skills snapshot ref" (found in e2e 2026-07-06).
func TestWorkspaceRefFor_PinsSkillsRepoDirName(t *testing.T) {
	row := &models.GitRepository{
		OrgID:         "default",
		ProjectID:     models.SkillsRepoSentinelProjectID,
		RepoURL:       "https://github.com/asdlc-repos/org-skills",
		RepoSlug:      "asdlc-repos-org-skills", // what phase2 backfill persists
		DefaultBranch: "main",
	}
	ref := WorkspaceRefFor("default", row, nil)
	if ref.RepoSlug != models.SkillsRepoDirName {
		t.Fatalf("skills RepoSlug = %q, want pinned %q", ref.RepoSlug, models.SkillsRepoDirName)
	}
}

// Ordinary project repos keep the row slug (URL-backfilled when absent).
func TestWorkspaceRefFor_ProjectReposKeepRowSlug(t *testing.T) {
	withSlug := &models.GitRepository{
		OrgID:     "default",
		ProjectID: "proj-1",
		RepoURL:   "https://github.com/acme/widgets",
		RepoSlug:  "acme-widgets",
	}
	if got := WorkspaceRefFor("default", withSlug, nil).RepoSlug; got != "acme-widgets" {
		t.Fatalf("RepoSlug = %q, want %q", got, "acme-widgets")
	}
	backfilled := &models.GitRepository{
		OrgID:     "default",
		ProjectID: "proj-2",
		RepoURL:   "https://github.com/acme/Gadgets.git",
	}
	if got := WorkspaceRefFor("default", backfilled, nil).RepoSlug; got != "acme-gadgets" {
		t.Fatalf("backfilled RepoSlug = %q, want %q", got, "acme-gadgets")
	}
}
