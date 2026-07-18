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
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/wso2/aep/aep-api/internal/platform/gitfs"
)

// snapshotEvictMinAge is the floor age a snapshot must reach before quota/LRU
// eviction may trash it. An in-flight turn reads its base/_skills snapshot
// lazily for the whole run, so anything younger than the turn runner's
// detached timeout could still be feeding a live turn. It is set to that
// timeout (turnRunTimeout in internal/spec/turn_runner.go, 30m), or
// 30m, whichever is larger. The mirror's current-HEAD snapshot is protected
// unconditionally (isHead) regardless of age; mirrors themselves stay
// evictable.
const snapshotEvictMinAge = 30 * time.Minute

// enforceQuota is pass 4 (leader-only): per-org quota first, then the global
// statfs watermark. Both funnel into evictWithin — snapshots go first
// (oldest mtime), then whole mirrors LRU by git/ activity, and every
// eviction is a trash rename (two-phase like all deletes). Eviction cost is
// a re-clone / re-Ensure on next use — always safe, because the mount is a
// rebuildable cache (D12/D13).
//
// Freed bytes are ESTIMATED from the candidates' sizes: a trash rename keeps
// the bytes on the filesystem until pass 1 purges them, so re-polling statfs
// mid-eviction would never converge.
func (r *Reaper) enforceQuota(ctx context.Context) error {
	root := r.engine.Root()
	if r.cfg.OrgQuotaBytes > 0 {
		if err := r.enforceOrgQuotas(ctx); err != nil {
			return err
		}
	}
	total, avail, err := r.diskUsage(root)
	if err != nil || total == 0 {
		return err
	}
	used := total - avail
	if used*100 <= uint64(r.cfg.DiskHighPct)*total {
		return nil // under the high watermark — nothing to do
	}
	lowBytes := total * uint64(r.cfg.DiskLowPct) / 100
	target := int64(used - lowBytes) // free down to the LOW watermark, not just under high
	slog.InfoContext(ctx, "reaper: disk over high watermark — evicting",
		"usedPct", used*100/total, "highPct", r.cfg.DiskHighPct, "lowPct", r.cfg.DiskLowPct, "targetBytes", target)
	return r.evictWithin(ctx, gitfs.ReposDir(root), target)
}

// enforceOrgQuotas walks repos/<orgId> subtrees and evicts within any org
// whose on-disk usage exceeds OrgQuotaBytes, down to the quota.
func (r *Reaper) enforceOrgQuotas(ctx context.Context) error {
	reposDir := gitfs.ReposDir(r.engine.Root())
	orgDirs, err := os.ReadDir(reposDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, org := range orgDirs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !org.IsDir() {
			continue
		}
		orgDir := filepath.Join(reposDir, org.Name())
		usage := duDir(orgDir)
		if usage <= r.cfg.OrgQuotaBytes {
			continue
		}
		slog.InfoContext(ctx, "reaper: org over quota — evicting",
			"org", org.Name(), "usageBytes", usage, "quotaBytes", r.cfg.OrgQuotaBytes)
		if err := r.evictWithin(ctx, orgDir, usage-r.cfg.OrgQuotaBytes); err != nil {
			slog.WarnContext(ctx, "reaper: org quota eviction failed", "org", org.Name(), "error", err)
		}
	}
	return nil
}

// snapshotCandidate / mirrorCandidate are eviction units within a scope.
type snapshotCandidate struct {
	path    string
	slugDir string // owning repo dir, for the mirror-size adjustment
	size    int64
	mtime   time.Time
	isHead  bool // sha == mirror's current HEAD — an in-flight turn may read it
}

type mirrorCandidate struct {
	ref     gitfs.RepoRef
	slugDir string
	size    int64 // whole repo dir (git + lock + remaining snapshots)
	lastUse time.Time
}

// evictWithin trashes candidates under scopeDir until ~target bytes are
// freed: snapshots first (oldest mtime first — they are pure derived data),
// then whole mirror dirs in LRU order. A mirror whose repo.lock is currently
// held is in live use and is SKIPPED, never waited on: TrashRepo acquires
// the EX flock through the gitfs Locker, and the bounded lockCtx turns that
// into a non-blocking try.
func (r *Reaper) evictWithin(ctx context.Context, scopeDir string, target int64) error {
	snaps, mirrors, err := r.collectCandidates(ctx, scopeDir)
	if err != nil {
		return err
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].mtime.Before(snaps[j].mtime) })
	sort.Slice(mirrors, func(i, j int) bool { return mirrors[i].lastUse.Before(mirrors[j].lastUse) })

	freed := int64(0)
	now := time.Now()
	evictedSnapBytes := map[string]int64{} // slugDir → snapshot bytes already trashed
	for _, s := range snaps {
		if freed >= target || ctx.Err() != nil {
			break
		}
		// Protect in-flight turns (unlike a plain mtime sweep): never evict the
		// snapshot the mirror HEAD points at, nor one younger than the turn
		// runner's detached timeout — the agents pod reads a turn's
		// base/_skills tree lazily, so a current-HEAD or recent snapshot may
		// still be feeding a live turn. Mirrors stay evictable; a resulting
		// shortfall is covered by the "exhausted candidates" warning below.
		if s.isHead || now.Sub(s.mtime) < snapshotEvictMinAge {
			continue
		}
		if err := os.Rename(s.path, r.trashDest()); err != nil {
			if !os.IsNotExist(err) {
				slog.WarnContext(ctx, "reaper: evict snapshot failed", "path", s.path, "error", err)
			}
			continue
		}
		freed += s.size
		evictedSnapBytes[s.slugDir] += s.size
	}
	for _, m := range mirrors {
		if freed >= target || ctx.Err() != nil {
			break
		}
		lockCtx, cancel := context.WithTimeout(ctx, evictLockTimeout)
		err := r.engine.TrashRepo(lockCtx, m.ref)
		cancel()
		if err != nil {
			// Lock held (live use) or validation/rename failure — skip to the
			// next LRU candidate; never block the sweep on a busy mirror.
			slog.InfoContext(ctx, "reaper: skipping mirror eviction",
				"org", m.ref.OrgID, "project", m.ref.ProjectID, "slug", m.ref.RepoSlug, "reason", err)
			continue
		}
		freed += m.size - evictedSnapBytes[m.slugDir] // don't double-count its evicted snapshots
		slog.InfoContext(ctx, "reaper: evicted LRU mirror",
			"org", m.ref.OrgID, "project", m.ref.ProjectID, "slug", m.ref.RepoSlug, "bytes", m.size)
	}
	if freed < target {
		slog.WarnContext(ctx, "reaper: eviction exhausted candidates short of target",
			"scope", scopeDir, "freedBytes", freed, "targetBytes", target)
	}
	return nil
}

// collectCandidates gathers the snapshot + mirror eviction candidates under
// scopeDir (the whole repos/ tree, or one org subtree).
func (r *Reaper) collectCandidates(ctx context.Context, scopeDir string) ([]snapshotCandidate, []mirrorCandidate, error) {
	var snaps []snapshotCandidate
	var mirrors []mirrorCandidate
	scopePrefix := scopeDir + string(filepath.Separator)
	err := r.walkRepoDirs(ctx, func(orgID, projectID, repoSlug, slugDir string) {
		if slugDir != scopeDir && !strings.HasPrefix(slugDir, scopePrefix) {
			return
		}
		snapsDir := gitfs.SnapshotsSubdir(slugDir)
		gitDir := gitfs.GitSubdir(slugDir)
		if entries, err := os.ReadDir(snapsDir); err == nil {
			// Resolve the mirror HEAD once per slugDir so the current-HEAD
			// snapshot (a running turn's base) is never a candidate. "" when
			// the mirror is missing/unresolvable → nothing is treated as HEAD.
			head := mirrorHead(gitDir)
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				path := filepath.Join(snapsDir, e.Name())
				snaps = append(snaps, snapshotCandidate{
					path:    path,
					slugDir: slugDir,
					size:    duDir(path),
					mtime:   info.ModTime(),
					isHead:  head != "" && e.Name() == head,
				})
			}
		}
		gitInfo, err := os.Stat(gitDir)
		if err != nil {
			return // no mirror (snapshot-only husk) — snapshots above still count
		}
		lastUse := gitInfo.ModTime()
		// FETCH_HEAD is rewritten by every fetch — a better recency signal
		// than the dir mtime (which only moves when a direct entry changes).
		if fh, err := os.Stat(filepath.Join(gitDir, "FETCH_HEAD")); err == nil && fh.ModTime().After(lastUse) {
			lastUse = fh.ModTime()
		}
		mirrors = append(mirrors, mirrorCandidate{
			ref:     gitfs.RepoRef{OrgID: orgID, ProjectID: projectID, RepoSlug: repoSlug},
			slugDir: slugDir,
			size:    duDir(slugDir),
			lastUse: lastUse,
		})
	})
	return snaps, mirrors, err
}

// duDir sums the file sizes under path (directory entries themselves are
// not counted; errors are skipped — a trash rename mid-walk is normal).
func duDir(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entries, keep walking
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// statfsUsage is the default diskUsage seam: total/available bytes of the
// filesystem backing path, df-style (available = unprivileged Bavail).
func statfsUsage(path string) (total, avail uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize)
	return st.Blocks * bsize, st.Bavail * bsize, nil
}
