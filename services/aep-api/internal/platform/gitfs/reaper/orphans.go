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

package reaper

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/wso2/aep/aep-api/internal/platform/gitfs"
	"github.com/wso2/aep/aep-api/internal/platform/gitfs/naming"
)

// reconcileOrphans is pass 3 (leader-only): set-difference the on-disk
// repos/<orgId>/<projectId>/<repoSlug> tree against the git_repositories
// rows and trash every dir no row owns — the self-healing backstop behind
// the best-effort delete/disconnect hooks. Two carve-outs:
//
//   - grace: a dir younger than 2×ReapInterval (by its own mtime) is kept,
//     so a mirror mid-materialization never races its own DB row;
//   - _skills: repos/<orgId>/_skills/… belongs to the org, not to one
//     project row — it is legitimate whenever the org owns ANY row
//     (including the sentinel project_id='_skills' row itself).
func (r *Reaper) reconcileOrphans(ctx context.Context) error {
	rows, err := r.repos.ListAll(ctx)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(rows)) // "org/project/slug" path keys
	orgs := make(map[string]bool)
	for _, row := range rows {
		known[row.OrgID+"/"+row.ProjectID+"/"+row.WorkspaceSlug] = true
		orgs[row.OrgID] = true
	}

	grace := 2 * r.cfg.ReapInterval
	now := time.Now()
	walkErr := r.walkRepoDirs(ctx, func(orgID, projectID, repoSlug, slugDir string) {
		if projectID == naming.SkillsRepoSentinelProjectID && orgs[orgID] {
			return // the org exists → it owns its whole _skills subtree
		}
		if known[orgID+"/"+projectID+"/"+repoSlug] {
			return
		}
		info, err := os.Stat(slugDir)
		if err != nil {
			return // raced with a concurrent trash — already gone
		}
		if now.Sub(info.ModTime()) <= grace {
			return // just created — its DB row may simply not be visible yet
		}
		ref := gitfs.RepoRef{OrgID: orgID, ProjectID: projectID, RepoSlug: repoSlug}
		// TrashRepo takes the repo's EX flock; bound the wait so a held lock
		// (someone IS using it — the strongest not-an-orphan signal) skips.
		lockCtx, cancel := context.WithTimeout(ctx, evictLockTimeout)
		err = r.engine.TrashRepo(lockCtx, ref)
		cancel()
		if err != nil {
			slog.WarnContext(ctx, "reaper: trash orphan dir failed",
				"org", orgID, "project", projectID, "slug", repoSlug, "error", err)
			return
		}
		slog.InfoContext(ctx, "reaper: trashed orphan repo dir",
			"org", orgID, "project", projectID, "slug", repoSlug)
	})
	if walkErr != nil {
		return walkErr
	}
	r.removeEmptyParents(ctx, grace)
	return nil
}

// removeEmptyParents prunes empty repos/<orgId>[/<projectId>] dirs left
// behind by trashed slugs so the tree doesn't accumulate husks. os.Remove
// (never RemoveAll) only succeeds on an EMPTY dir, and the grace gate skips
// anything freshly touched — a concurrent MkdirAll either repopulates the
// dir (Remove fails, ignored) or bumps its mtime out of range.
func (r *Reaper) removeEmptyParents(ctx context.Context, grace time.Duration) {
	reposDir := gitfs.ReposDir(r.engine.Root())
	orgDirs, err := os.ReadDir(reposDir)
	if err != nil {
		return
	}
	now := time.Now()
	removeIfStaleEmpty := func(path string) {
		info, err := os.Stat(path)
		if err != nil || now.Sub(info.ModTime()) <= grace {
			return
		}
		_ = os.Remove(path) // fails on non-empty — exactly the safety we want
	}
	for _, org := range orgDirs {
		if ctx.Err() != nil {
			return
		}
		if !org.IsDir() {
			continue
		}
		orgDir := filepath.Join(reposDir, org.Name())
		if projects, err := os.ReadDir(orgDir); err == nil {
			for _, proj := range projects {
				if proj.IsDir() {
					removeIfStaleEmpty(filepath.Join(orgDir, proj.Name()))
				}
			}
		}
		removeIfStaleEmpty(orgDir)
	}
}
