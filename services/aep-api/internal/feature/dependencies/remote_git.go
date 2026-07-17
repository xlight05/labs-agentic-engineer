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

package dependencies

// remote_git.go is a thin, READ-ONLY GitHub REST client backing the two MCP
// tools an agent uses to read a provider's OpenAPI contract straight out of its
// repo (endpoint spec discovery) — no local clone. It exposes exactly two
// GitHub reads:
//
//   - Contents API GET /repos/{owner}/{repo}/contents/{path}?ref=  (file or dir)
//   - Code Search GET /search/code?q=<query>+repo:{owner}/{repo}
//
// There is deliberately NO create/update/delete/branch/PR surface here — the
// agent can read the org's own repos and nothing else. Two guardrails bound it:
//
//  1. Org from the verified MCP claim only. The caller passes ocOrgID (the
//     ocOrgId claim bound by the auth middleware, never a tool parameter); the
//     org's credential — token AND owner — is resolved from it.
//  2. Owner-must-match-org. Every read REFUSES (ErrOwnerNotInOrg) any `owner`
//     that is not the resolved credential's RepoOwner(), BEFORE any network
//     call. This stops an agent in org A from reading org B's / arbitrary repos.
//
// The read is bounded: a fixed GitHub API base host (a test seam overrides it
// for the httptest stub), a short HTTP timeout, and a cap on decoded content
// size.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/wso2/aep/aep-api/internal/platform/secrets"
)

// ErrOwnerNotInOrg is returned when a requested repo owner is not the GitHub
// account the caller's org credential is installed on. This is the cross-org
// read guard — it fires before any GitHub request is issued.
var ErrOwnerNotInOrg = errors.New("dependencies: repo owner is not owned by the caller's organization")

// ErrQueryScopeQualifier is returned when an agent-supplied SearchCode query
// embeds its own repo/org/user/fork scope qualifier. GitHub OR-combines
// multiple `repo:` (and `org:`/`user:`) qualifiers in one search, so an
// embedded qualifier would widen the search beyond the authorized repo (e.g.
// query `"secret repo:acme/other-private"` searches BOTH repos). REFUSED
// before any network call so the appended `repo:{owner}/{repo}` is always the
// query's sole scope.
var ErrQueryScopeQualifier = errors.New("dependencies: query must not contain a repo/org/user/fork scope qualifier")

// ErrFileTooLargeToInline is returned when the Contents API reports a file it
// cannot inline as base64 (encoding "none" — GitHub's signal for a file over
// its ~1 MiB inline limit, typically sized 1-100 MiB). This client is
// deliberately read-only/simple and has no blob-API fallback, so such a file
// is refused rather than silently surfaced as empty content.
var ErrFileTooLargeToInline = errors.New("dependencies: file too large to inline (GitHub reported encoding=none)")

// scopeQualifierPattern matches a GitHub code-search scope qualifier
// (repo:, org:, user:, fork:) anywhere in a query, case-insensitively.
var scopeQualifierPattern = regexp.MustCompile(`(?i)\b(repo|org|user|fork):`)

const (
	// defaultRemoteGitAPIBase is the real GitHub REST API root (matches the
	// clients/github default). Overridable only via WithRemoteGitAPIBase.
	defaultRemoteGitAPIBase = "https://api.github.com"
	// defaultRemoteGitTimeout bounds a single Contents/Search read.
	defaultRemoteGitTimeout = 15 * time.Second
	// defaultMaxContentBytes caps decoded file content (GitHub's own inline
	// Contents limit is 1 MiB; a larger file must be fetched some other way).
	defaultMaxContentBytes = 1 << 20
	// maxSearchItems caps how many code-search hits we surface.
	maxSearchItems = 30
	// per_page bound sent to the Code Search API.
	searchPerPage = 30
)

// RemoteGitFile is one Contents API read: a file (decoded Content + SHA,
// IsDirectory=false) or a directory (IsDirectory=true, Entries populated,
// Content empty).
type RemoteGitFile struct {
	Content     string
	SHA         string
	IsDirectory bool
	Entries     []RemoteGitEntry
}

// RemoteGitEntry is one child listed when a Contents read resolves a directory.
type RemoteGitEntry struct {
	Path string
	Type string // "file" | "dir"
	SHA  string
}

// RemoteGitSearchHit is one Code Search result: the path and blob SHA.
type RemoteGitSearchHit struct {
	Path string
	SHA  string
}

// RemoteGitClient reads files and searches code in an org's OWN GitHub repos
// over the REST API. It resolves the org's credential (token + owner) from the
// credential resolver and enforces the owner-must-match-org guard on every read.
type RemoteGitClient struct {
	resolver        secrets.Resolver
	httpClient      *http.Client
	apiBase         string
	maxContentBytes int64
}

// RemoteGitOption configures a RemoteGitClient. Production wiring passes none;
// WithRemoteGitAPIBase/WithRemoteGitMaxContentBytes are test seams.
type RemoteGitOption func(*RemoteGitClient)

// WithRemoteGitAPIBase overrides the GitHub REST API base URL — a TEST SEAM
// pointing the client at an httptest fake. Not wired in production.
func WithRemoteGitAPIBase(base string) RemoteGitOption {
	return func(c *RemoteGitClient) { c.apiBase = strings.TrimRight(base, "/") }
}

// WithRemoteGitMaxContentBytes overrides the decoded-content cap (test seam).
func WithRemoteGitMaxContentBytes(n int64) RemoteGitOption {
	return func(c *RemoteGitClient) { c.maxContentBytes = n }
}

// NewRemoteGitClient builds the read-only GitHub client over the org credential
// resolver. Production wiring passes no options.
func NewRemoteGitClient(resolver secrets.Resolver, opts ...RemoteGitOption) *RemoteGitClient {
	c := &RemoteGitClient{
		resolver:        resolver,
		httpClient:      &http.Client{Timeout: defaultRemoteGitTimeout},
		apiBase:         defaultRemoteGitAPIBase,
		maxContentBytes: defaultMaxContentBytes,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Compile-time proof the concrete client satisfies the consumer port.
var _ RemoteGitReader = (*RemoteGitClient)(nil)

// authorize resolves the org credential and enforces the owner-must-match-org
// guard. It returns the fresh token on success, or ErrOwnerNotInOrg when owner
// is not the credential's RepoOwner(). No network call to GitHub happens until
// this passes.
func (c *RemoteGitClient) authorize(ctx context.Context, ocOrgID, owner string) (token string, err error) {
	cred, err := c.resolver.Resolve(ctx, ocOrgID)
	if err != nil {
		return "", fmt.Errorf("resolve org credential: %w", err)
	}
	orgOwner := cred.RepoOwner()
	// Case-insensitive compare: GitHub logins are case-insensitive, so treat
	// "Acme" and "acme" as the same owner. Empty owners never match.
	if owner == "" || orgOwner == "" || !strings.EqualFold(owner, orgOwner) {
		return "", fmt.Errorf("%w: %q (org owns %q)", ErrOwnerNotInOrg, owner, orgOwner)
	}
	tok, _, err := cred.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve token: %w", err)
	}
	return tok, nil
}

// setHeaders applies the standard read-only GitHub API headers + bearer auth.
func setHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

// GetFileContents reads one path via the Contents API. A file returns decoded
// Content + SHA; a directory returns Entries (IsDirectory=true). REFUSES a
// cross-org owner before any network call.
func (c *RemoteGitClient) GetFileContents(ctx context.Context, ocOrgID, owner, repo, path, ref string) (*RemoteGitFile, error) {
	token, err := c.authorize(ctx, ocOrgID, owner)
	if err != nil {
		return nil, err
	}

	// Path-escape each segment so a path like "specs/openapi.yaml" maps to
	// .../contents/specs/openapi.yaml (slashes preserved, other reserved
	// characters encoded). Empty path = repo root.
	u := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.apiBase,
		url.PathEscape(owner), url.PathEscape(repo), escapePath(path))
	if ref != "" {
		u += "?ref=" + url.QueryEscape(ref)
	}

	body, err := c.get(ctx, u, token, "contents")
	if err != nil {
		return nil, err
	}

	// The Contents API returns a JSON array for a directory and a JSON object
	// for a file. Peek the first non-space byte to fold both.
	trimmed := strings.TrimLeftFunc(string(body), func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '\r'
	})
	if strings.HasPrefix(trimmed, "[") {
		var dir []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		}
		if err := json.Unmarshal(body, &dir); err != nil {
			return nil, fmt.Errorf("decode directory listing: %w", err)
		}
		entries := make([]RemoteGitEntry, 0, len(dir))
		for _, e := range dir {
			entries = append(entries, RemoteGitEntry{Path: e.Path, Type: e.Type, SHA: e.SHA})
		}
		return &RemoteGitFile{IsDirectory: true, Entries: entries}, nil
	}

	var file struct {
		Type     string `json:"type"`
		SHA      string `json:"sha"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, fmt.Errorf("decode file contents: %w", err)
	}

	content, err := decodeContent(file.Content, file.Encoding, c.maxContentBytes)
	if err != nil {
		return nil, err
	}
	return &RemoteGitFile{Content: content, SHA: file.SHA, IsDirectory: false}, nil
}

// SearchCode runs a code search scoped to the org's repo. REFUSES a cross-org
// owner before any network call.
func (c *RemoteGitClient) SearchCode(ctx context.Context, ocOrgID, owner, repo, query string) ([]RemoteGitSearchHit, error) {
	token, err := c.authorize(ctx, ocOrgID, owner)
	if err != nil {
		return nil, err
	}

	// Reject an agent-supplied scope qualifier BEFORE building q. Without this,
	// a query like "secret repo:acme/other-private" would hand GitHub two
	// repo: qualifiers, which it OR-combines — widening the search to any repo
	// the token can see, not just the authorized one.
	if scopeQualifierPattern.MatchString(query) {
		return nil, fmt.Errorf("%w: %q", ErrQueryScopeQualifier, query)
	}

	// Scope the query to the org's repo. The sanitizer above guarantees query
	// carries no scope qualifier of its own, so the repo: qualifier appended
	// here is the query's SOLE scope — it cannot be OR-combined with another
	// repo:/org:/user: qualifier to escape the authorized repo.
	q := strings.TrimSpace(query) + fmt.Sprintf(" repo:%s/%s", owner, repo)
	u := fmt.Sprintf("%s/search/code?q=%s&per_page=%d", c.apiBase, url.QueryEscape(q), searchPerPage)

	body, err := c.get(ctx, u, token, "code search")
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []struct {
			Path string `json:"path"`
			SHA  string `json:"sha"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode search results: %w", err)
	}
	hits := make([]RemoteGitSearchHit, 0, len(out.Items))
	for _, it := range out.Items {
		if len(hits) >= maxSearchItems {
			break
		}
		hits = append(hits, RemoteGitSearchHit{Path: it.Path, SHA: it.SHA})
	}
	return hits, nil
}

// get performs an authenticated GET requiring 200 and returns the (bounded)
// response body. Reads at most maxContentBytes+overhead so a hostile/oversized
// response cannot exhaust memory.
func (c *RemoteGitClient) get(ctx context.Context, u, token, label string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	setHeaders(req, token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github %s request: %w", label, err)
	}
	defer resp.Body.Close()

	// Bound the read. Base64 inflates ~4/3, plus JSON envelope; a generous
	// headroom over the content cap keeps well-formed responses intact while
	// still refusing an unbounded body.
	limit := c.maxContentBytes*2 + (1 << 16)
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("read github %s response: %w", label, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github %s failed (status %d): %s", label, resp.StatusCode, truncateForError(body))
	}
	return body, nil
}

// decodeContent base64-decodes the Contents API `content` field, enforcing the
// size cap. GitHub only inlines base64; any other encoding (or an over-cap
// file) is refused rather than returned partial/undecoded.
//
// encoding "none" is GitHub's explicit "too large to inline" signal (files
// roughly 1-100 MiB come back with content:"" and encoding:"none"): that is
// refused via ErrFileTooLargeToInline rather than treated as a genuinely
// empty file. A real empty file still comes back as encoding:"base64" with
// content:"" and is returned as an empty string, unaffected by this check.
func decodeContent(content, encoding string, maxBytes int64) (string, error) {
	if encoding == "none" {
		return "", ErrFileTooLargeToInline
	}
	if content == "" {
		return "", nil
	}
	if encoding != "" && encoding != "base64" {
		return "", fmt.Errorf("unsupported content encoding %q", encoding)
	}
	// GitHub wraps the base64 payload at 60 cols with newlines.
	clean := strings.NewReplacer("\n", "", "\r", "").Replace(content)
	decoded, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return "", fmt.Errorf("decode base64 content: %w", err)
	}
	if int64(len(decoded)) > maxBytes {
		return "", fmt.Errorf("file content %d bytes exceeds cap %d", len(decoded), maxBytes)
	}
	return string(decoded), nil
}

// escapePath path-escapes each segment of a repo-relative path while preserving
// the separating slashes.
func escapePath(p string) string {
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// truncateForError bounds an error body so a large failure response is not
// echoed wholesale into logs/errors.
func truncateForError(b []byte) string {
	const max = 512
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
