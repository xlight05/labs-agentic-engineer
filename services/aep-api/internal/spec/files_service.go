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

// Package files is the generic, specs/-scoped Files API
// (docs/design/agents-generation-migration.md §4, §12.2). It replaces the
// per-project local working tree: reads are served from the workspace-mounted
// bare mirror at the branch tip via the sourcecontrol.Workspace port (ls-tree/
// cat-file blob shas are the same git blob shas the GitHub tree API returned,
// so the FE's baseSha CAS flow is unaffected), and the single write is an atomic,
// all-or-nothing `apply`: one Workspace.Mutate that stages the whole batch as
// one commit and pushes it to origin under `--force-with-lease` (origin stays
// the CAS arbiter; Mutate owns the bounded fast-forward retry).
//
// Draft state lives on the frontend; committed truth is the git origin. `apply`
// carries per-file baseSha optimistic-concurrency: a stale baseSha (or a
// baseSha-omitted write to a path that already exists) fails the whole batch
// with 409 and nothing is applied. There are no individual PUT/DELETE routes.
package spec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/wso2/aep/aep-api/internal/platform/designspec"
	"github.com/wso2/aep/aep-api/internal/platform/secrets"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// ---- errors ----------------------------------------------------------------

var (
	// ErrProjectRepoNotFound (the "no git repo for (org, project)" 404) is
	// declared once for the whole spec domain in genai_service.go's error block;
	// the files service returns that same shared sentinel.
	// ErrPathInvalid — a path escapes specs/, is non-canonical, or is oversized;
	// maps to 400.
	ErrPathInvalid = errors.New("invalid file path")
	// ErrFileNotFound — a read for a path absent at HEAD; maps to 404.
	ErrFileNotFound = errors.New("file not found")
	// ErrApplyConflict — one or more baseSha preconditions failed; the caller
	// reads Conflicts and returns 409. Nothing was applied.
	ErrApplyConflict = errors.New("apply conflict: stale baseSha")
	// errConflictSentinel short-circuits the CAS retry loop on a precondition
	// failure (distinct from a transient non-fast-forward, which retries).
	errConflictSentinel = errors.New("precondition conflict")
)

// ---- wire shapes -----------------------------------------------------------

// FileMeta is one entry of the list response / the per-file result of apply.
type FileMeta struct {
	Path string `json:"path"`
	SHA  string `json:"sha"`
	Size int64  `json:"size,omitempty"`
}

// FileContent is the read response. SHA doubles as the draft's baseSha.
type FileContent struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA     string `json:"sha"`
}

// WriteOp is one file write. BaseSHA omitted (empty) means "must not exist yet".
type WriteOp struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	BaseSHA string `json:"baseSha,omitempty"`
}

// DeleteOp is one file delete keyed on its current blob sha.
type DeleteOp struct {
	Path    string `json:"path"`
	BaseSHA string `json:"baseSha,omitempty"`
}

// ApplyRequest is the atomic accept payload.
type ApplyRequest struct {
	Writes  []WriteOp  `json:"writes,omitempty"`
	Deletes []DeleteOp `json:"deletes,omitempty"`
	Message string     `json:"message,omitempty"`
}

// Warning is a non-blocking soft-validation note attached to an applied file.
type Warning struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ApplyResult is the 200 response of a successful apply.
type ApplyResult struct {
	CommitSHA string     `json:"commitSha"`
	Files     []FileMeta `json:"files"`
	Warnings  []Warning  `json:"warnings,omitempty"`
}

// Conflict is one failed baseSha precondition (the 409 body carries a list).
type Conflict struct {
	Path       string `json:"path"`
	BaseSHA    string `json:"baseSha"`
	CurrentSHA string `json:"currentSha"`
}

// ---- ports (consumer-side; concrete gitrepo services satisfy them) ---------

// FilesRepoResolver looks up the project's git repo row. *sourcecontrol.repoService
// satisfies it via GetRepo (which returns sourcecontrol.ErrRepoNotFound when absent).
type FilesRepoResolver interface {
	GetRepo(ctx context.Context, orgID, projectID string) (*sourcecontrol.GitRepository, error)
}

// FilesGitGateway is the narrow git surface + credential resolver + save identities.
// Every operation — reads and the Apply write — goes through the Workspace port
// (the mounted bare mirror); the feature holds no REST Git-Data dependency.
// *sourcecontrol.gitOpsService satisfies it structurally.
type FilesGitGateway interface {
	// Workspace is the mount-backed git engine serving all reads and writes.
	Workspace() sourcecontrol.Workspace
	Resolver() secrets.Resolver
	ResolveSaveIdentities(cred secrets.Credential) (*sourcecontrol.GitIdentity, *sourcecontrol.GitIdentity)
}

// ---- service ---------------------------------------------------------------

// FilesService is the typed entry point for the Files API.
type FilesService interface {
	List(ctx context.Context, orgID, projectID, prefix string) ([]FileMeta, error)
	Read(ctx context.Context, orgID, projectID, path string) (*FileContent, error)
	Apply(ctx context.Context, orgID, projectID string, req ApplyRequest) (*ApplyResult, []Conflict, error)
}

type service struct {
	repos FilesRepoResolver
	git   FilesGitGateway
}

// NewFilesService wires the Files API. Either dep may be nil in degraded boot; the
// operations then surface ErrProjectRepoNotFound.
func NewFilesService(repos FilesRepoResolver, git FilesGitGateway) FilesService {
	return &service{repos: repos, git: git}
}

// repoRow looks up the project's repo row, mapping absence to
// ErrProjectRepoNotFound (the 404).
func (s *service) repoRow(ctx context.Context, orgID, projectID string) (*sourcecontrol.GitRepository, error) {
	if s == nil || s.repos == nil || s.git == nil {
		return nil, ErrProjectRepoNotFound
	}
	repo, err := s.repos.GetRepo(ctx, orgID, projectID)
	if err != nil {
		if errors.Is(err, sourcecontrol.ErrRepoNotFound) {
			return nil, ErrProjectRepoNotFound
		}
		return nil, fmt.Errorf("resolve project repo: %w", err)
	}
	if repo == nil {
		return nil, ErrProjectRepoNotFound
	}
	return repo, nil
}

// resolveRef derives the workspace-mount address every operation starts from:
// the repo row keyed by the AUTHENTICATED org (never client input) plus the
// org credential for mirror freshening and pushes.
func (s *service) resolveRef(ctx context.Context, orgID, projectID string) (sourcecontrol.RepoRef, error) {
	repo, err := s.repoRow(ctx, orgID, projectID)
	if err != nil {
		return sourcecontrol.RepoRef{}, err
	}
	return sourcecontrol.ResolveWorkspaceRef(ctx, s.git.Resolver(), orgID, repo)
}

// List returns every blob at the branch tip, filtered to those whose path has
// the given prefix (empty prefix ⇒ all), sorted by path.
func (s *service) List(ctx context.Context, orgID, projectID, prefix string) ([]FileMeta, error) {
	ref, err := s.resolveRef(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	entries, _, err := s.git.Workspace().List(ctx, ref, "")
	if err != nil {
		return nil, fmt.Errorf("list files at head: %w", err)
	}
	out := make([]FileMeta, 0, len(entries))
	for _, e := range entries {
		if prefix != "" && !strings.HasPrefix(e.Path, prefix) {
			continue
		}
		out = append(out, FileMeta{Path: e.Path, SHA: e.SHA, Size: e.Size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Read returns the content + blob sha of a single file at the branch tip.
func (s *service) Read(ctx context.Context, orgID, projectID, path string) (*FileContent, error) {
	if err := validatePath(path); err != nil {
		return nil, err
	}
	ref, err := s.resolveRef(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	content, blobSHA, err := s.git.Workspace().ReadFile(ctx, ref, "", path)
	if err != nil {
		if errors.Is(err, sourcecontrol.ErrPathNotFound) {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("read %s at head: %w", path, err)
	}
	return &FileContent{Path: path, Content: string(content), SHA: blobSHA}, nil
}

// Apply validates + commits a batch atomically: one Workspace.Mutate whose fn
// checks every per-file baseSha precondition against the fetched branch tip
// (Tx.Base()) and stages the whole batch as one commit. A non-fast-forward
// push (a concurrent writer) re-runs fn against the new base, bounded by the
// retry policy; a precondition failure aborts immediately with no retry and
// returns (nil, conflicts, ErrApplyConflict) — nothing applied.
func (s *service) Apply(ctx context.Context, orgID, projectID string, req ApplyRequest) (*ApplyResult, []Conflict, error) {
	if len(req.Writes) == 0 && len(req.Deletes) == 0 {
		return nil, nil, fmt.Errorf("%w: empty apply (no writes or deletes)", ErrPathInvalid)
	}
	// Path + size validation happens once, before any git operation: a bad
	// path is a 400, never a partial commit.
	seen := map[string]bool{}
	for _, w := range req.Writes {
		if err := validatePath(w.Path); err != nil {
			return nil, nil, err
		}
		if len(w.Content) > maxFileBytes {
			return nil, nil, fmt.Errorf("%w: %s exceeds %d bytes", ErrPathInvalid, w.Path, maxFileBytes)
		}
		if seen[w.Path] {
			return nil, nil, fmt.Errorf("%w: %s appears more than once", ErrPathInvalid, w.Path)
		}
		seen[w.Path] = true
	}
	for _, d := range req.Deletes {
		if err := validatePath(d.Path); err != nil {
			return nil, nil, err
		}
		if seen[d.Path] {
			return nil, nil, fmt.Errorf("%w: %s is both written and deleted", ErrPathInvalid, d.Path)
		}
		seen[d.Path] = true
	}

	ref, err := s.resolveRef(ctx, orgID, projectID)
	if err != nil {
		return nil, nil, err
	}
	author, committer := s.git.ResolveSaveIdentities(ref.Cred)

	var conflicts []Conflict
	var files []FileMeta
	var warnings []Warning
	res, err := s.git.Workspace().Mutate(ctx, ref, func(tx sourcecontrol.Tx) error {
		// fn re-runs against a fresh base on a CAS retry — start clean.
		conflicts, files, warnings = nil, nil, nil

		// The committed base tree this attempt builds on: path → blob sha,
		// the input to every per-file baseSha precondition.
		current := map[string]string{}
		if werr := tx.Base().Walk("", func(rel, blobSHA string) error {
			current[rel] = blobSHA
			return nil
		}); werr != nil {
			return fmt.Errorf("walk base tree: %w", werr)
		}

		conflicts = checkPreconditions(req, current)
		if len(conflicts) > 0 {
			return errConflictSentinel // fn error aborts Mutate — no retry, the 409 path
		}

		for _, w := range req.Writes {
			tx.Write(w.Path, []byte(w.Content))
			// The staged blob's object name is a pure function of its content
			// (what `git hash-object` will produce), so the response carries
			// the exact sha a subsequent read returns — the FE folds it into
			// its baseShas.
			files = append(files, FileMeta{Path: w.Path, SHA: blobSHA([]byte(w.Content))})
			warnings = append(warnings, softValidate(w.Path, w.Content)...)
		}
		for _, d := range req.Deletes {
			tx.Delete(d.Path)
		}
		return nil
	}, sourcecontrol.CommitOpts{
		Message:   applyMessage(req.Message),
		Author:    author,
		Committer: committer,
		Retry:     sourcecontrol.RetryPolicy{Attempts: casAttempts},
	})
	if errors.Is(err, errConflictSentinel) {
		slog.InfoContext(ctx, "files apply conflict — nothing applied",
			"project", projectID, "conflicts", len(conflicts))
		return nil, conflicts, ErrApplyConflict
	}
	if err != nil {
		slog.ErrorContext(ctx, "files apply failed",
			"project", projectID, "writes", len(req.Writes), "deletes", len(req.Deletes), "error", err)
		return nil, nil, err
	}
	// res.Changed == false means the batch was byte-identical to the base tree
	// (every precondition passed) — no commit was made and CommitSHA is the
	// unchanged tip, which already carries exactly the requested state.
	result := &ApplyResult{CommitSHA: res.CommitSHA, Files: files, Warnings: warnings}
	slog.InfoContext(ctx, "files apply committed",
		"project", projectID, "repo", ref.OrgID+"/"+ref.ProjectID+"/"+ref.RepoSlug,
		"commit", result.CommitSHA, "changed", res.Changed,
		"writes", len(req.Writes), "deletes", len(req.Deletes))
	return result, nil, nil
}

// checkPreconditions compares each op's baseSha against the current tree.
// baseSha == "" on a write means "must not exist"; on a delete it means
// "delete whatever is there" but the path must still exist.
func checkPreconditions(req ApplyRequest, current map[string]string) []Conflict {
	var conflicts []Conflict
	for _, w := range req.Writes {
		cur, exists := current[w.Path]
		if w.BaseSHA == "" {
			if exists {
				conflicts = append(conflicts, Conflict{Path: w.Path, BaseSHA: "", CurrentSHA: cur})
			}
			continue
		}
		if !exists || cur != w.BaseSHA {
			conflicts = append(conflicts, Conflict{Path: w.Path, BaseSHA: w.BaseSHA, CurrentSHA: cur})
		}
	}
	for _, d := range req.Deletes {
		cur, exists := current[d.Path]
		if !exists {
			conflicts = append(conflicts, Conflict{Path: d.Path, BaseSHA: d.BaseSHA, CurrentSHA: ""})
			continue
		}
		if d.BaseSHA != "" && cur != d.BaseSHA {
			conflicts = append(conflicts, Conflict{Path: d.Path, BaseSHA: d.BaseSHA, CurrentSHA: cur})
		}
	}
	return conflicts
}

// softValidate returns non-blocking warnings for a written file (§8's soft tier
// — the hard semantic gate stays at save/tag). A component design.json is
// validated against the published schema (the same definition the agent's write
// gate uses) plus the name==dir rule; any other .json gets a cheap parseability
// check. Warnings never block the commit.
func softValidate(path, content string) []Warning {
	if dir, ok := componentDesignDir(path); ok {
		if err := designspec.ValidateComponentDesignInDir([]byte(content), dir); err != nil {
			var ve *designspec.ValidationError
			if errors.As(err, &ve) {
				return []Warning{{Path: path, Code: ve.Code, Message: ve.Message}}
			}
		}
		return nil
	}
	if strings.HasSuffix(path, ".json") && !json.Valid([]byte(content)) {
		return []Warning{{Path: path, Code: designspec.CodeInvalidJSON, Message: "content is not valid JSON"}}
	}
	return nil
}

// componentDesignDir returns the <name> directory of a component design.json
// path (specs/design/components/<name>/design.json), and whether path is one.
func componentDesignDir(path string) (string, bool) {
	const prefix = "specs/design/components/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/design.json") {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) != 2 {
		return "", false
	}
	return parts[0], true
}

func applyMessage(suffix string) string {
	base := "aep: apply file changes"
	if strings.TrimSpace(suffix) != "" {
		return base + ": " + suffix
	}
	return base
}
