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
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/config"
	"github.com/wso2/aep/aep-api/internal/platform/gitfs"
)

// testCfg is the baseline knob set: minute-scale interval (grace = 2min),
// day-scale ages, standard watermarks, per-org quota disabled.
func testCfg() config.WorkspaceConfig {
	return config.WorkspaceConfig{
		ReapInterval:   time.Minute,
		SnapshotMaxAge: 24 * time.Hour,
		TrashMaxAge:    24 * time.Hour,
		DiskHighPct:    85,
		DiskLowPct:     70,
	}
}

// staticLister is a fixed-rows RepoLister.
type staticLister []RepoCoordinate

func (l staticLister) ListAll(context.Context) ([]RepoCoordinate, error) { return l, nil }

// errLister always fails — for the pass-isolation test.
type errLister struct{ err error }

func (l errLister) ListAll(context.Context) ([]RepoCoordinate, error) { return nil, l.err }

// newSyntheticReaper builds a reaper over a fresh workspace root (no real
// origin — dirs are laid out by hand with the mk* helpers below).
func newSyntheticReaper(t *testing.T, cfg config.WorkspaceConfig, repos RepoLister) (*Reaper, string) {
	t.Helper()
	eng, err := gitfs.New(filepath.Join(t.TempDir(), "workspaces"))
	if err != nil {
		t.Fatalf("gitfs.New: %v", err)
	}
	return New(eng, repos, cfg), eng.Root()
}

// mkSlugDir creates repos/<org>/<proj>/<slug> and returns its path.
func mkSlugDir(t *testing.T, root, org, proj, slug string) string {
	t.Helper()
	dir := filepath.Join(gitfs.ReposDir(root), org, proj, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

// mkFile writes size bytes at path (parents created).
func mkFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), size), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// mkGitDir creates <slugDir>/git carrying one payload file of the given
// size, then pins the git dir's mtime (the LRU signal) to `when`.
func mkGitDir(t *testing.T, slugDir string, size int, when time.Time) string {
	t.Helper()
	gitDir := filepath.Join(slugDir, "git")
	mkFile(t, filepath.Join(gitDir, "payload"), size)
	chtimes(t, gitDir, when)
	return gitDir
}

// mkSnapshot creates <slugDir>/snapshots/<sha> with one payload file of the
// given size, then pins the snapshot dir's mtime to `when`.
func mkSnapshot(t *testing.T, slugDir, sha string, size int, when time.Time) string {
	t.Helper()
	dir := filepath.Join(slugDir, "snapshots", sha)
	mkFile(t, filepath.Join(dir, "payload"), size)
	chtimes(t, dir, when)
	return dir
}

// writeGitHead points the synthetic mirror's HEAD at sha (detached form) so
// mirrorHead(gitDir) resolves to it — used to mark a snapshot as current HEAD.
// Returns gitDir for chaining with mkGitDir.
func writeGitHead(t *testing.T, gitDir, sha string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(sha+"\n"), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
	return gitDir
}

func chtimes(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// fakeSha returns a distinct valid 40-hex object name per suffix.
func fakeSha(suffix byte) string {
	return fmt.Sprintf("%039x%x", 0, suffix%16)
}

// trashEntries lists the current trash/<id> entry names.
func trashEntries(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(gitfs.TrashDir(root))
	if err != nil {
		t.Fatalf("read trash dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// mustExist / mustNotExist assert canonical-path presence.
func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be gone, stat err=%v", path, err)
	}
}
