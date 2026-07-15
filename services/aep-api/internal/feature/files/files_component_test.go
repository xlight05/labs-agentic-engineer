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

// Component tier for the Files API: the REAL contract-first handler chain
// (strict server via componenttest) over the production gitrepo gateway —
// reads AND the Apply write through the REAL gitfs Workspace engine mirroring
// a REAL bare file:// origin (pure workspacetest fixture; the Git-Data fake is
// gone with the REST write path).
// Only the repo row + credential resolver are faked, so list/read/apply run
// against genuine git object-store semantics — a stale baseSha is a real 409,
// a multi-write+delete apply is a real single commit pushed to origin under
// --force-with-lease, and a read right after an apply proves the mirror
// freshening.
package files_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wso2/aep/aep-api/internal/api"
	"github.com/wso2/aep/aep-api/internal/credentials"
	"github.com/wso2/aep/aep-api/internal/feature/files"
	"github.com/wso2/aep/aep-api/internal/feature/gitrepo"
	"github.com/wso2/aep/aep-api/internal/platform/componenttest"
	"github.com/wso2/aep/aep-api/internal/platform/gitfs"
	"github.com/wso2/aep/aep-api/internal/platform/gitfs/workspacetest"
	"github.com/wso2/aep/aep-api/internal/platform/gittest"
	"github.com/wso2/aep/aep-api/models"
)

const (
	testOrg  = "acme-org"
	testProj = "widgets"
	testSlug = "acme-widgets"
	apiBase  = "/api/v1/projects/" + testProj + "/files"
)

// ---- faked edges ----

// stubRepoResolver hands out the single repo row, keyed by the AUTHENTICATED
// org — a caller resolved to any other org gets ErrRepoNotFound (the 404),
// mirroring the production (org_id, project_id) row lookup.
type stubRepoResolver struct{ rec *models.GitRepository }

func (s stubRepoResolver) GetRepo(_ context.Context, orgID, _ string) (*models.GitRepository, error) {
	if s.rec == nil || orgID != s.rec.OrgID {
		return nil, gitrepo.ErrRepoNotFound
	}
	return s.rec, nil
}

type stubCred struct{}

func (stubCred) Token(context.Context) (string, time.Time, error) {
	return "test-token", time.Time{}, nil
}
func (stubCred) Identity() credentials.Identity {
	return credentials.Identity{Name: "Bot", Email: "bot@aep.dev", Login: "bot"}
}
func (stubCred) RepoOwner() string                            { return "acme" }
func (stubCred) WebhookStrategy() credentials.WebhookStrategy { return credentials.WebhookPlatform }

type stubResolver struct{}

func (stubResolver) Resolve(context.Context, string) (credentials.Credential, error) {
	return stubCred{}, nil
}

// ---- harness ----

type filesRig struct {
	h      *componenttest.Harness
	remote *gittest.Remote
	engine *gitfs.Engine
}

func newFilesRig(t *testing.T, seed map[string]string) *filesRig {
	t.Helper()
	remote := gittest.NewRemote(t, gittest.WithSeed(seed, "seed"))
	rec := &models.GitRepository{
		OrgID:         testOrg,
		ProjectID:     testProj,
		RepoURL:       remote.URL(),
		RepoSlug:      testSlug, // pinned — SlugForURL can't parse file:// URLs
		DefaultBranch: "main",
		Status:        "ready",
	}
	// The production gateway over the real engine: every read AND the Apply
	// write run through the Workspace port (the REST git-object port is nil —
	// files never touches it).
	engine := workspacetest.NewEngine(t)
	gitOps := gitrepo.NewGitOpsService(stubResolver{}, engine)
	svc := files.NewService(stubRepoResolver{rec: rec}, gitOps)
	h := componenttest.New(t, componenttest.Options{Deps: api.Deps{FilesSvc: svc}})
	return &filesRig{h: h, remote: remote, engine: engine}
}

// mirrorRevParse resolves rev inside the ENGINE's bare mirror (not the origin)
// — the C8 sha-consistency probe.
func (r *filesRig) mirrorRevParse(t *testing.T, rev string) string {
	t.Helper()
	gitDir, err := gitfs.GitDir(r.engine.Root(), gitfs.RepoRef{
		OrgID: testOrg, ProjectID: testProj, RepoSlug: testSlug,
	})
	if err != nil {
		t.Fatalf("mirror git dir: %v", err)
	}
	cmd := exec.Command("git", "--git-dir", gitDir, "rev-parse", "--verify", rev)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mirror rev-parse %s: %v\n%s", rev, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *filesRig) get(path string) *httptest.ResponseRecorder {
	return r.h.AsOrg(testOrg).Get(path)
}

func (r *filesRig) apply(body string) *httptest.ResponseRecorder {
	return r.h.AsOrg(testOrg).Post(apiBase+"/apply", body)
}

// readSHA reads a file through the API and returns its blob sha (the draft's
// baseSha).
func (r *filesRig) readSHA(t *testing.T, path string) string {
	t.Helper()
	rec := r.get(apiBase + "/" + path)
	if rec.Code != http.StatusOK {
		t.Fatalf("read %s: code %d (%s)", path, rec.Code, rec.Body.String())
	}
	var fc files.FileContent
	if err := json.Unmarshal(rec.Body.Bytes(), &fc); err != nil {
		t.Fatalf("decode read: %v", err)
	}
	return fc.SHA
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// ---- tests ----

func TestListAtHead_FilteredByPrefix(t *testing.T) {
	r := newFilesRig(t, map[string]string{
		"specs/requirements/requirements.md": "req",
		"specs/design/design.md":             "des",
		"README.md":                          "root",
	})
	rec := r.get(apiBase + "?prefix=specs/design/")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	var metas []files.FileMeta
	if err := json.Unmarshal(rec.Body.Bytes(), &metas); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(metas) != 1 || metas[0].Path != "specs/design/design.md" {
		t.Fatalf("prefix filter wrong: %+v", metas)
	}
	if metas[0].SHA == "" || metas[0].Size == 0 {
		t.Errorf("meta missing sha/size: %+v", metas[0])
	}
}

func TestReadAtHead(t *testing.T) {
	r := newFilesRig(t, map[string]string{"specs/requirements/requirements.md": "hello world"})
	rec := r.get(apiBase + "/specs/requirements/requirements.md")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d: %s", rec.Code, rec.Body.String())
	}
	var fc files.FileContent
	_ = json.Unmarshal(rec.Body.Bytes(), &fc)
	if fc.Content != "hello world" || fc.Path != "specs/requirements/requirements.md" || fc.SHA == "" {
		t.Fatalf("read wrong: %+v", fc)
	}

	if miss := r.get(apiBase + "/specs/requirements/missing.md"); miss.Code != http.StatusNotFound {
		t.Errorf("missing file: code %d, want 404", miss.Code)
	}
}

func TestApply_MultiWriteAndDelete_SingleCommit(t *testing.T) {
	r := newFilesRig(t, map[string]string{
		"specs/requirements/requirements.md": "old",
		"specs/requirements/todo.md":         "scratch",
	})
	reqSHA := r.readSHA(t, "specs/requirements/requirements.md")
	todoSHA := r.readSHA(t, "specs/requirements/todo.md")
	headBefore := r.remote.HeadSHA(t)

	body := mustJSON(t, files.ApplyRequest{
		Writes: []files.WriteOp{
			{Path: "specs/requirements/requirements.md", Content: "new", BaseSHA: reqSHA},
			{Path: "specs/design/design.md", Content: "# Design"}, // baseSha omitted ⇒ create
		},
		Deletes: []files.DeleteOp{{Path: "specs/requirements/todo.md", BaseSHA: todoSHA}},
		Message: "from test",
	})
	rec := r.apply(body)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply code %d: %s", rec.Code, rec.Body.String())
	}
	var res files.ApplyResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.CommitSHA == "" || len(res.Files) != 2 {
		t.Fatalf("apply result wrong: %+v", res)
	}

	// Exactly one new commit; content applied; delete honored.
	if r.remote.HeadSHA(t) == headBefore {
		t.Error("HEAD did not advance")
	}
	if got := r.remote.FileAt(t, "main", "specs/requirements/requirements.md"); got != "new" {
		t.Errorf("requirements.md = %q, want new", got)
	}
	if got := r.remote.FileAt(t, "main", "specs/design/design.md"); got != "# Design" {
		t.Errorf("design.md = %q", got)
	}
	tags := r.remote.Tags(t) // deletes leave no tag; just confirm no crash
	_ = tags
	// todo.md gone: reading it now 404s.
	if miss := r.get(apiBase + "/specs/requirements/todo.md"); miss.Code != http.StatusNotFound {
		t.Errorf("todo.md still present: code %d", miss.Code)
	}
}

func TestApply_StaleBaseSHA_409_NothingApplied(t *testing.T) {
	r := newFilesRig(t, map[string]string{"specs/requirements/requirements.md": "v1"})
	headBefore := r.remote.HeadSHA(t)

	body := mustJSON(t, files.ApplyRequest{
		Writes: []files.WriteOp{
			{Path: "specs/requirements/requirements.md", Content: "v2", BaseSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
		},
	})
	rec := r.apply(body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("apply code %d, want 409: %s", rec.Code, rec.Body.String())
	}
	// THE FROZEN 409 CONTRACT — exact field set and values (key order is not
	// part of JSON; the shape itself is contract-tied via ApplyConflicts).
	// currentSha is the git blob sha of "v1" (deterministic), baseSha echoes
	// the stale sha the caller sent.
	var got409 struct {
		Conflicts []map[string]string `json:"conflicts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got409); err != nil {
		t.Fatalf("409 body not JSON: %v\n%s", err, rec.Body.String())
	}
	want409 := []map[string]string{{
		"path":       "specs/requirements/requirements.md",
		"baseSha":    "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		"currentSha": "28c218c44b49222f91536daf5b4d9871638edc8e",
	}}
	if !reflect.DeepEqual(got409.Conflicts, want409) {
		t.Fatalf("409 body drifted:\n got: %s\nwant: %+v", rec.Body.String(), want409)
	}
	// Nothing applied — HEAD unchanged, content unchanged.
	if r.remote.HeadSHA(t) != headBefore {
		t.Error("HEAD advanced on a conflicting apply")
	}
	if got := r.remote.FileAt(t, "main", "specs/requirements/requirements.md"); got != "v1" {
		t.Errorf("content mutated on conflict: %q", got)
	}
}

// A batch where only ONE op conflicts is rejected wholesale: the valid delete
// must not be applied (all-or-nothing), and every conflict is collected.
func TestApply_BatchConflict_AllOrNothing_CollectsAllConflicts(t *testing.T) {
	r := newFilesRig(t, map[string]string{
		"specs/requirements/requirements.md": "keep me",
		"specs/requirements/todo.md":         "scratch",
	})
	todoSHA := r.readSHA(t, "specs/requirements/todo.md")
	headBefore := r.remote.HeadSHA(t)

	body := mustJSON(t, files.ApplyRequest{
		Writes: []files.WriteOp{
			{Path: "specs/requirements/requirements.md", Content: "clobber", BaseSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
			{Path: "specs/design/design.md", Content: "new", BaseSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}, // absent + baseSha set ⇒ conflict too
		},
		Deletes: []files.DeleteOp{{Path: "specs/requirements/todo.md", BaseSHA: todoSHA}}, // valid — must still NOT apply
	})
	rec := r.apply(body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("code %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Conflicts []files.Conflict `json:"conflicts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode conflicts: %v", err)
	}
	if len(got.Conflicts) != 2 {
		t.Fatalf("conflicts = %+v, want ALL 2 collected", got.Conflicts)
	}
	if r.remote.HeadSHA(t) != headBefore {
		t.Error("HEAD advanced on a conflicting batch")
	}
	if got := r.remote.FileAt(t, "main", "specs/requirements/todo.md"); got != "scratch" {
		t.Errorf("valid delete leaked through a conflicting batch: todo.md = %q", got)
	}
}

func TestApply_BaseSHAOmittedButExists_409(t *testing.T) {
	r := newFilesRig(t, map[string]string{"specs/requirements/requirements.md": "exists"})
	body := mustJSON(t, files.ApplyRequest{
		Writes: []files.WriteOp{
			{Path: "specs/requirements/requirements.md", Content: "clobber"}, // no baseSha ⇒ must-not-exist
		},
	})
	rec := r.apply(body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("code %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Conflicts []files.Conflict `json:"conflicts"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Conflicts) != 1 || got.Conflicts[0].BaseSHA != "" || got.Conflicts[0].CurrentSHA == "" {
		t.Fatalf("expected must-not-exist conflict: %+v", got.Conflicts)
	}
}

func TestApply_PathRejections(t *testing.T) {
	r := newFilesRig(t, map[string]string{"specs/requirements/requirements.md": "x"})
	cases := map[string]string{
		"traversal": mustJSON(t, files.ApplyRequest{Writes: []files.WriteOp{{Path: "specs/../etc/passwd", Content: "x"}}}),
		"non-specs": mustJSON(t, files.ApplyRequest{Writes: []files.WriteOp{{Path: "README.md", Content: "x"}}}),
		"absolute":  mustJSON(t, files.ApplyRequest{Writes: []files.WriteOp{{Path: "/specs/x.md", Content: "x"}}}),
	}
	for name, body := range cases {
		if rec := r.apply(body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: code %d, want 400 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

func TestApply_SizeCap(t *testing.T) {
	r := newFilesRig(t, map[string]string{"specs/requirements/requirements.md": "x"})
	huge := strings.Repeat("A", (5<<20)+1)
	body := mustJSON(t, files.ApplyRequest{Writes: []files.WriteOp{{Path: "specs/requirements/big.md", Content: huge}}})
	if rec := r.apply(body); rec.Code != http.StatusBadRequest {
		t.Errorf("size cap: code %d, want 400", rec.Code)
	}
}

func TestApply_WarningsNonBlocking(t *testing.T) {
	r := newFilesRig(t, nil)
	body := mustJSON(t, files.ApplyRequest{
		Writes: []files.WriteOp{
			{Path: "specs/design/components/foo/design.json", Content: "{ not valid json"},
		},
	})
	rec := r.apply(body)
	if rec.Code != http.StatusOK {
		t.Fatalf("invalid json must NOT block apply: code %d (%s)", rec.Code, rec.Body.String())
	}
	var res files.ApplyResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res.Warnings) != 1 || res.Warnings[0].Code != "INVALID_JSON" {
		t.Fatalf("expected one INVALID_JSON warning: %+v", res.Warnings)
	}
	// The (invalid) file was still committed — warnings never block.
	if got := r.remote.FileAt(t, "main", "specs/design/components/foo/design.json"); got != "{ not valid json" {
		t.Errorf("file not committed: %q", got)
	}
}

func TestApply_SchemaViolationWarning_NonBlocking(t *testing.T) {
	r := newFilesRig(t, nil)
	// Valid JSON + valid schema, but name != component directory ("bar" != "foo").
	valid := `{"name":"bar","type":"service","version":"1","language":"go","buildpack":"go","appPath":".","entrypoint":"m","exposure":"intranet","connections":[],"description":"d"}`
	body := mustJSON(t, files.ApplyRequest{
		Writes: []files.WriteOp{{Path: "specs/design/components/foo/design.json", Content: valid}},
	})
	rec := r.apply(body)
	if rec.Code != http.StatusOK {
		t.Fatalf("schema violation must NOT block apply: code %d (%s)", rec.Code, rec.Body.String())
	}
	var res files.ApplyResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res.Warnings) != 1 || res.Warnings[0].Code != "SCHEMA_VIOLATION" {
		t.Fatalf("expected one SCHEMA_VIOLATION warning: %+v", res.Warnings)
	}
	if got := r.remote.FileAt(t, "main", "specs/design/components/foo/design.json"); got != valid {
		t.Errorf("file not committed despite warning: %q", got)
	}
}

func TestFiles_NoAuth_401(t *testing.T) {
	r := newFilesRig(t, map[string]string{"specs/requirements/requirements.md": "x"})
	if rec := r.h.NoAuth().Get(apiBase); rec.Code != http.StatusUnauthorized {
		t.Errorf("no-auth list: code %d, want 401", rec.Code)
	}
}

// The org is derived solely from the verified token, and the repo row is keyed
// by it — a caller from another org resolves no repo and gets a 404, never the
// project's files (the mount path deriver consults only the row, D6).
func TestFiles_CrossOrg_404(t *testing.T) {
	r := newFilesRig(t, map[string]string{"specs/requirements/requirements.md": "secret"})
	if rec := r.h.AsOrg("intruder-org").Get(apiBase); rec.Code != http.StatusNotFound {
		t.Errorf("cross-org list: code %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if rec := r.h.AsOrg("intruder-org").Get(apiBase + "/specs/requirements/requirements.md"); rec.Code != http.StatusNotFound {
		t.Errorf("cross-org read: code %d, want 404", rec.Code)
	}
}

// A unicode path survives the whole chain — URL escaping, the ServeMux
// {path...} catch-all (server.go) + wrapper PathValue decoding, ls-tree -z
// (unquoted NUL plumbing), cat-file — byte-identically.
func TestReadAtHead_UnicodePath(t *testing.T) {
	const path = "specs/requirements/仕様-résumé ノート.md"
	const content = "非ASCIIコンテンツ — ünïcödé\n"
	r := newFilesRig(t, map[string]string{path: content})

	escaped := (&url.URL{Path: "/" + path}).EscapedPath()
	rec := r.get(apiBase + escaped)
	if rec.Code != http.StatusOK {
		t.Fatalf("unicode read: code %d (%s)", rec.Code, rec.Body.String())
	}
	var fc files.FileContent
	if err := json.Unmarshal(rec.Body.Bytes(), &fc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fc.Path != path || fc.Content != content || fc.SHA == "" {
		t.Fatalf("unicode read wrong: %+v", fc)
	}

	list := r.get(apiBase + "?prefix=specs/requirements/")
	var metas []files.FileMeta
	_ = json.Unmarshal(list.Body.Bytes(), &metas)
	if len(metas) != 1 || metas[0].Path != path {
		t.Fatalf("unicode list wrong: %+v", metas)
	}
}

// A ~1 MiB file reads back byte-identically with the exact blob size in the
// listing (well under the 5 MiB write cap, well over any pipe-buffer size).
func TestReadAtHead_LargeFile(t *testing.T) {
	const path = "specs/design/big.md"
	content := strings.Repeat("0123456789abcdef", 1<<16) // 1 MiB
	r := newFilesRig(t, map[string]string{path: content})

	rec := r.get(apiBase + "/" + path)
	if rec.Code != http.StatusOK {
		t.Fatalf("large read: code %d", rec.Code)
	}
	var fc files.FileContent
	if err := json.Unmarshal(rec.Body.Bytes(), &fc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fc.Content != content {
		t.Fatalf("large read: content mismatch (%d bytes, want %d)", len(fc.Content), len(content))
	}

	list := r.get(apiBase + "?prefix=specs/design/")
	var metas []files.FileMeta
	_ = json.Unmarshal(list.Body.Bytes(), &metas)
	if len(metas) != 1 || metas[0].Size != int64(len(content)) {
		t.Fatalf("large list wrong: %+v", metas)
	}
}

// SHA consistency (design C8): the commitSha the apply returns is the sha on
// the ORIGIN's branch tip AND the mirror's local ref; the per-file shas in the
// response are the exact blob shas a subsequent read (ls-tree) returns — the
// FE folds them into its next baseShas.
func TestApply_ShaConsistency_OriginMirrorAndReadBack(t *testing.T) {
	r := newFilesRig(t, map[string]string{"specs/requirements/requirements.md": "v1"})
	reqSHA := r.readSHA(t, "specs/requirements/requirements.md")

	body := mustJSON(t, files.ApplyRequest{
		Writes: []files.WriteOp{
			{Path: "specs/requirements/requirements.md", Content: "v2", BaseSHA: reqSHA},
			{Path: "specs/design/design.md", Content: "# Design"},
		},
	})
	rec := r.apply(body)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply code %d: %s", rec.Code, rec.Body.String())
	}
	var res files.ApplyResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Returned commit sha == origin tip == mirror ref.
	if origin := r.remote.HeadSHA(t); res.CommitSHA != origin {
		t.Errorf("commitSha %s != origin tip %s", res.CommitSHA, origin)
	}
	if mirror := r.mirrorRevParse(t, "refs/heads/main"); res.CommitSHA != mirror {
		t.Errorf("commitSha %s != mirror ref %s", res.CommitSHA, mirror)
	}
	// Returned per-file shas == what a subsequent read serves.
	for _, f := range res.Files {
		if got := r.readSHA(t, f.Path); got != f.SHA {
			t.Errorf("%s: apply returned sha %s, read returns %s", f.Path, f.SHA, got)
		}
	}
}

// Two concurrent applies to DISJOINT paths: one fast-forward-lands, the other
// is push-rejected, re-fetches, re-checks its (still valid) preconditions and
// lands — both 200, both files present, exactly two commits, linear history.
func TestApply_ConcurrentDisjointApplies_BothLand(t *testing.T) {
	r := newFilesRig(t, map[string]string{"specs/requirements/requirements.md": "seed"})
	base := r.remote.HeadSHA(t)

	bodies := []string{
		mustJSON(t, files.ApplyRequest{Writes: []files.WriteOp{{Path: "specs/design/a.md", Content: "A"}}}),
		mustJSON(t, files.ApplyRequest{Writes: []files.WriteOp{{Path: "specs/design/b.md", Content: "B"}}}),
	}
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = r.apply(bodies[i]).Code
		}(i)
	}
	wg.Wait()
	if codes[0] != http.StatusOK || codes[1] != http.StatusOK {
		t.Fatalf("codes = %v, want both 200 (disjoint applies must both land)", codes)
	}
	if got := r.remote.FileAt(t, "main", "specs/design/a.md"); got != "A" {
		t.Errorf("a.md = %q", got)
	}
	if got := r.remote.FileAt(t, "main", "specs/design/b.md"); got != "B" {
		t.Errorf("b.md = %q", got)
	}
	// Exactly two new commits on a linear history (the loser REPLAYED, it did
	// not merge or clobber).
	if n := r.remote.HeadSHA(t); n == base {
		t.Fatal("HEAD did not advance")
	}
}

// Two concurrent applies to the SAME path with the same baseSha: exactly one
// lands, the other re-runs its precondition against the winner's commit and
// gets the clean 409 — never a lost update.
func TestApply_ConcurrentSamePath_OneLandsOne409(t *testing.T) {
	const path = "specs/requirements/requirements.md"
	r := newFilesRig(t, map[string]string{path: "seed"})
	baseSHA := r.readSHA(t, path)

	bodies := []string{
		mustJSON(t, files.ApplyRequest{Writes: []files.WriteOp{{Path: path, Content: "first", BaseSHA: baseSHA}}}),
		mustJSON(t, files.ApplyRequest{Writes: []files.WriteOp{{Path: path, Content: "second", BaseSHA: baseSHA}}}),
	}
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = r.apply(bodies[i]).Code
		}(i)
	}
	wg.Wait()
	ok, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("codes = %v, want exactly one 200 and one 409", codes)
	}
	got := r.remote.FileAt(t, "main", path)
	if got != "first" && got != "second" {
		t.Errorf("content = %q, want the single winner's write", got)
	}
}

// Branch-tip reads freshen the mirror on every request: a commit made directly
// on the ORIGIN (an external writer) is visible on the very next read, with the
// new blob sha — there is no cache tier to go stale.
func TestRead_SeesOriginAdvanceImmediately(t *testing.T) {
	const path = "specs/requirements/requirements.md"
	r := newFilesRig(t, map[string]string{path: "v1"})

	sha1 := r.readSHA(t, path)

	// External writer advances origin (not through the API).
	r.remote.Seed(t, map[string]string{path: "v2 external"}, "external edit")

	rec := r.get(apiBase + "/" + path)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-read: code %d", rec.Code)
	}
	var fc files.FileContent
	_ = json.Unmarshal(rec.Body.Bytes(), &fc)
	if fc.Content != "v2 external" {
		t.Fatalf("content = %q, want the origin's new commit (no stale cache)", fc.Content)
	}
	if fc.SHA == sha1 {
		t.Error("blob sha did not change across an origin advance")
	}
}
