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
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/platform/gitfs/naming"
)

// TestOrphanReconcile is the pass-3 matrix: DB-known dirs and graced young
// dirs are kept, stale unknown dirs are trashed, and _skills subtrees belong
// to any org that owns at least one row.
func TestOrphanReconcile(t *testing.T) {
	// The reaper takes coordinates, not rows: WorkspaceSlug is computed at the
	// composition root (here via gitfs, the same source), so the reaper package
	// never names the GitRepository entity.
	rows := []RepoCoordinate{
		{OrgID: "o1", ProjectID: "p1", WorkspaceSlug: "r1"},
		// A slug-less row exercises the SlugForURL backfill.
		{OrgID: "o1", ProjectID: "p4", WorkspaceSlug: naming.WorkspaceSlug("p4", "", "https://github.com/acme/from-url.git")},
	}
	r, root := newSyntheticReaper(t, testCfg(), staticLister(rows)) // grace = 2×1min

	old := time.Now().Add(-10 * time.Minute)
	age := func(dir string) string { chtimes(t, dir, old); return dir }

	known := age(mkSlugDir(t, root, "o1", "p1", "r1"))
	backfilled := age(mkSlugDir(t, root, "o1", "p4", "acme-from-url")) // SlugForURL = owner-repo
	orphanOld := age(mkSlugDir(t, root, "o1", "p2", "ghost"))
	orphanYoung := mkSlugDir(t, root, "o1", "p3", "newborn") // fresh mtime — inside grace
	skillsKnownOrg := age(mkSlugDir(t, root, "o1", "_skills", "org-skills"))
	skillsGhostOrg := age(mkSlugDir(t, root, "o2", "_skills", "org-skills"))

	if err := r.reconcileOrphans(context.Background()); err != nil {
		t.Fatalf("reconcile orphans: %v", err)
	}

	mustExist(t, known)
	mustExist(t, backfilled)
	mustNotExist(t, orphanOld)
	mustExist(t, orphanYoung)
	mustExist(t, skillsKnownOrg)
	mustNotExist(t, skillsGhostOrg)
	if got := trashEntries(t, root); len(got) != 2 {
		t.Fatalf("want 2 trashed orphans, got %v", got)
	}
}
