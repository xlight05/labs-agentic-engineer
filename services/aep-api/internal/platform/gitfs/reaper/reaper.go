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

// Package reaper is the single disk-lifecycle authority for the shared
// workspace mount. git_repositories is authoritative and the mount is a rebuildable cache in
// every part, so the synchronous delete hooks (TrashRepo / TrashOrg) are
// best-effort and CORRECTNESS lives here, in the background sweep. Four
// passes per tick, each isolated (one failing never stops the next):
//
//  1. trash reclamation — purge trash/<id> entries older than TrashMaxAge
//     (local + idempotent: every replica runs it);
//  2. snapshot age-reap — trash snapshots/<sha> dirs older than
//     SnapshotMaxAge that are not the mirror's current HEAD (immutability
//     makes this safe: no leases, no heartbeats);
//  3. orphan reconciliation — set-difference the on-disk repos/… tree
//     against the DB rows, with a 2×interval mtime grace (leader-only);
//  4. quota/LRU eviction — statfs high-watermark + per-org quota; snapshots
//     evicted first (oldest mtime), then whole mirrors LRU by git/ mtime,
//     until under the low watermark (leader-only).
//
// The leader gate for passes 3–4 is a non-blocking flock on
// <root>/.reaper.lock — replicas that lose it skip the global passes for the
// tick and retry next tick.
package reaper

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wso2/aep/aep-api/internal/config"
	"github.com/wso2/aep/aep-api/internal/platform/gitfs"
)

// RepoCoordinate is the reaper's own view of a repo row: just its on-disk
// identity. Deliberately NOT models.GitRepository — the reaper is a kernel
// package, and naming the domain entity would be the kernel depending on a
// domain. It needs three things to decide whether a directory is an orphan;
// this names exactly those. The composition root projects the DB rows onto it.
type RepoCoordinate struct {
	OrgID         string
	ProjectID     string
	WorkspaceSlug string
}

// RepoLister is the reaper-side port: every repo coordinate across all orgs and
// statuses. Pending/error rows count — their on-disk dirs must not be misread as
// orphans. The concrete repository is adapted onto it at the composition root.
type RepoLister interface {
	ListAll(ctx context.Context) ([]RepoCoordinate, error)
}

// leaderLockName is the root-level flock file serializing the global passes
// (orphan reconciliation + quota/LRU) to one replica per tick.
const leaderLockName = ".reaper.lock"

// evictLockTimeout bounds the "non-blocking try" on a mirror's repo.lock
// during LRU eviction: long enough for a few flock polls, far shorter than
// any real fetch/push critical section — a busy mirror is skipped, never
// waited on.
const evictLockTimeout = 100 * time.Millisecond

// Reaper is the app.Watcher running the four disk-lifecycle passes on
// cfg.ReapInterval.
type Reaper struct {
	engine *gitfs.Engine
	repos  RepoLister
	cfg    config.WorkspaceConfig
	// diskUsage is the statfs seam (total/available bytes of the filesystem
	// backing the workspace root). Defaults to syscall.Statfs; tests inject a
	// fake to simulate watermark pressure.
	diskUsage func(path string) (total, avail uint64, err error)
}

// New wires the reaper over the engine's workspace root. Zero/negative cfg
// knobs fall back to the design §14 defaults (defensive — the config loader
// already bakes them in). OrgQuotaBytes <= 0 disables the per-org quota
// check; the global watermark check always runs.
func New(engine *gitfs.Engine, repos RepoLister, cfg config.WorkspaceConfig) *Reaper {
	if cfg.ReapInterval <= 0 {
		cfg.ReapInterval = 5 * time.Minute
	}
	if cfg.SnapshotMaxAge <= 0 {
		cfg.SnapshotMaxAge = 24 * time.Hour
	}
	if cfg.TrashMaxAge <= 0 {
		cfg.TrashMaxAge = 24 * time.Hour
	}
	if cfg.DiskHighPct <= 0 {
		cfg.DiskHighPct = 85
	}
	if cfg.DiskLowPct <= 0 {
		cfg.DiskLowPct = 70
	}
	return &Reaper{engine: engine, repos: repos, cfg: cfg, diskUsage: statfsUsage}
}

// Run drives the sweep on cfg.ReapInterval until ctx is canceled. Matches
// the watcher lifecycle convention (execution.Sweep): a plain goroutine per
// watcher, all durable state on disk / in Postgres.
func (r *Reaper) Run(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.ReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Sweep(ctx)
		}
	}
}

// Sweep runs one full reap cycle. Exported so a test (or an admin trigger)
// can drive a single pass without the ticker. Passes are isolated: a failing
// pass is logged and the next one still runs.
func (r *Reaper) Sweep(ctx context.Context) {
	r.pass(ctx, "trash-reclamation", r.reclaimTrash)
	r.pass(ctx, "snapshot-age-reap", r.reapSnapshots)

	// Global passes run on at most one replica per tick: non-blocking leader
	// flock — losing it simply defers to whichever replica holds it.
	release, held := r.tryLeaderLock(ctx)
	if !held {
		return
	}
	defer release()
	r.pass(ctx, "orphan-reconciliation", r.reconcileOrphans)
	r.pass(ctx, "quota-lru-eviction", r.enforceQuota)
}

// pass runs one pass, isolating its failure to a log line so the remaining
// passes still run (design §14 — hooks and passes are all best-effort; the
// next tick retries).
func (r *Reaper) pass(ctx context.Context, name string, fn func(context.Context) error) {
	if ctx.Err() != nil {
		return
	}
	if err := fn(ctx); err != nil {
		slog.WarnContext(ctx, "reaper: pass failed", "pass", name, "error", err)
	}
}

// tryLeaderLock takes the global <root>/.reaper.lock exclusively without
// blocking. held=false means another replica leads this tick (or the lock
// file could not be opened — logged, treated as not-leader).
func (r *Reaper) tryLeaderLock(ctx context.Context) (release func(), held bool) {
	path := filepath.Join(r.engine.Root(), leaderLockName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		slog.WarnContext(ctx, "reaper: open leader lock failed", "path", path, "error", err)
		return nil, false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			slog.WarnContext(ctx, "reaper: leader flock failed", "path", path, "error", err)
		}
		return nil, false
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true
}

// walkRepoDirs visits every repos/<orgId>/<projectId>/<repoSlug> directory.
// Non-directory strays are skipped; visit errors are the visitor's own
// business (it logs and continues) — the walk only fails on an unreadable
// repos/ root.
func (r *Reaper) walkRepoDirs(ctx context.Context, visit func(orgID, projectID, repoSlug, slugDir string)) error {
	reposDir := gitfs.ReposDir(r.engine.Root())
	orgs, err := os.ReadDir(reposDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, org := range orgs {
		if !org.IsDir() {
			continue
		}
		orgDir := filepath.Join(reposDir, org.Name())
		projects, err := os.ReadDir(orgDir)
		if err != nil {
			continue // raced with a trash rename — next tick reconverges
		}
		for _, proj := range projects {
			if !proj.IsDir() {
				continue
			}
			projDir := filepath.Join(orgDir, proj.Name())
			slugs, err := os.ReadDir(projDir)
			if err != nil {
				continue
			}
			for _, slug := range slugs {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if !slug.IsDir() {
					continue
				}
				visit(org.Name(), proj.Name(), slug.Name(), filepath.Join(projDir, slug.Name()))
			}
		}
	}
	return nil
}
