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

// Package workspacetest is the reusable fixture for tests that exercise the
// gitfs Workspace engine against a REAL bare origin: a gittest.Remote
// serving as the file:// origin (arrange/assert via genuine git plumbing —
// Seed/Tag/HeadSHA/FileAt/Tags) plus an Engine rooted in t.TempDir(). It
// supersedes the gittest Git-Data HTTP fake for consumers ported onto the
// Workspace port. file:// origins need no credential, so RepoRef.Cred is
// nil and askpass injection is skipped.
package workspacetest

import (
	"path/filepath"
	"testing"

	"github.com/wso2/aep/aep-api/internal/platform/gitfs"
	"github.com/wso2/aep/aep-api/internal/platform/gittest"
)

// Default path-key segments a Fixture addresses its repo with.
const (
	DefaultOrg     = "org-1"
	DefaultProject = "proj-1"
	DefaultSlug    = "acme-repo"
)

// Fixture bundles one origin, one engine, and the RepoRef addressing the
// origin through the engine. Arrange/assert against origin state via the
// embedded gittest helpers: fx.Origin.Seed / Tag / HeadSHA / FileAt / Tags.
type Fixture struct {
	Origin *gittest.Remote
	Engine *gitfs.Engine
	Ref    gitfs.RepoRef
}

// New builds a Fixture: a bare origin seeded with the given files (nil seeds
// an empty initial commit on main) and a fresh engine rooted in t.TempDir().
func New(t *testing.T, seed map[string]string) *Fixture {
	t.Helper()
	origin := NewOrigin(t, seed)
	return &Fixture{
		Origin: origin,
		Engine: NewEngine(t),
		Ref:    RefFor(origin, DefaultOrg, DefaultProject, DefaultSlug),
	}
}

// NewOrigin creates a real bare origin (file:// clone URL) with an initial
// commit on main carrying the given files (nil → empty tree).
func NewOrigin(t *testing.T, seed map[string]string) *gittest.Remote {
	t.Helper()
	if seed == nil {
		return gittest.NewRemote(t)
	}
	return gittest.NewRemote(t, gittest.WithSeed(seed, "seed"))
}

// NewEngine builds an engine over a fresh workspace root in t.TempDir().
func NewEngine(t *testing.T) *gitfs.Engine {
	t.Helper()
	e, err := gitfs.New(filepath.Join(t.TempDir(), "workspaces"))
	if err != nil {
		t.Fatalf("workspacetest: new engine: %v", err)
	}
	return e
}

// RefFor addresses origin under the given path-key segments. Cred stays nil
// — file:// remotes need no credential.
func RefFor(origin *gittest.Remote, orgID, projectID, slug string) gitfs.RepoRef {
	return gitfs.RepoRef{
		OrgID:         orgID,
		ProjectID:     projectID,
		RepoSlug:      slug,
		CloneURL:      origin.URL(),
		DefaultBranch: "main",
	}
}
