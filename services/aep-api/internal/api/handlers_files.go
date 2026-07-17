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

package api

import (
	"context"
	"errors"

	"github.com/wso2/aep/aep-api/internal/feature/files"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
)

// Files feature on the strict interface: list-files / read-file / apply-files.
// Reads are served at the branch tip through the workspace mirror; the single
// write is the atomic apply. Every operation is org-scoped — the tenant gate
// bound the token org before these run. read-file's {path} spans multiple
// segments: the generated single-segment pattern serves plain paths and the
// ServeMux catch-all registered in server.go routes nested ones — both land
// here with PathValue-decoded (unescaped) bytes, so unicode/escaped paths
// survive the chain byte-identically.

func (s *legacyHandlers) ListFiles(ctx context.Context, request gen.ListFilesRequestObject) (gen.ListFilesResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	prefix := ""
	if request.Params.Prefix != "" {
		prefix = request.Params.Prefix
	}
	metas, err := s.deps.FilesSvc.List(ctx, org, request.ProjectName, prefix)
	if err != nil {
		return nil, mapFilesError(err)
	}
	out := make([]gen.FileMeta, 0, len(metas))
	for _, m := range metas {
		out = append(out, gen.FileMeta{Path: m.Path, Sha: m.SHA, Size: m.Size})
	}
	return gen.ListFiles200JSONResponse(out), nil
}

func (s *legacyHandlers) ReadFile(ctx context.Context, request gen.ReadFileRequestObject) (gen.ReadFileResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if request.Path == "" {
		return nil, errBadRequest("path is required")
	}
	fc, err := s.deps.FilesSvc.Read(ctx, org, request.ProjectName, request.Path)
	if err != nil {
		return nil, mapFilesError(err)
	}
	return gen.ReadFile200JSONResponse(gen.FileContent{
		Path:    fc.Path,
		Content: fc.Content,
		Sha:     fc.SHA,
	}), nil
}

func (s *legacyHandlers) ApplyFiles(ctx context.Context, request gen.ApplyFilesRequestObject) (gen.ApplyFilesResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if request.Body == nil {
		return nil, errBadRequest("request body required")
	}
	res, conflicts, err := s.deps.FilesSvc.Apply(ctx, org, request.ProjectName, applyRequestFromWire(*request.Body))
	if err != nil {
		if errors.Is(err, files.ErrApplyConflict) {
			return applyConflictsToWire(conflicts), nil
		}
		return nil, mapFilesError(err)
	}
	return gen.ApplyFiles200JSONResponse(applyResultToWire(res)), nil
}

// applyConflictsToWire projects the service conflicts onto the contract's
// declared 409 body (ApplyConflicts — the FE's baseSha CAS flow consumes it;
// nothing was applied when this is returned). files_component_test.go pins the
// field set + values.
func applyConflictsToWire(conflicts []files.Conflict) gen.ApplyFiles409JSONResponse {
	out := gen.ApplyConflicts{Conflicts: make([]gen.ApplyConflict, 0, len(conflicts))}
	for _, c := range conflicts {
		out.Conflicts = append(out.Conflicts, gen.ApplyConflict{
			Path: c.Path, BaseSha: c.BaseSHA, CurrentSha: c.CurrentSHA,
		})
	}
	return gen.ApplyFiles409JSONResponse(out)
}

// applyRequestFromWire converts the generated body into the service's shape.
func applyRequestFromWire(in gen.ApplyRequest) files.ApplyRequest {
	out := files.ApplyRequest{Message: in.Message}
	for _, w := range in.Writes {
		out.Writes = append(out.Writes, files.WriteOp{Path: w.Path, Content: w.Content, BaseSHA: w.BaseSha})
	}
	for _, d := range in.Deletes {
		out.Deletes = append(out.Deletes, files.DeleteOp{Path: d.Path, BaseSHA: d.BaseSha})
	}
	return out
}

// applyResultToWire converts the service result into the contract schema.
func applyResultToWire(res *files.ApplyResult) gen.ApplyResult {
	out := gen.ApplyResult{CommitSha: res.CommitSHA, Files: make([]gen.FileMeta, 0, len(res.Files))}
	for _, f := range res.Files {
		out.Files = append(out.Files, gen.FileMeta{Path: f.Path, Sha: f.SHA, Size: f.Size})
	}
	for _, w := range res.Warnings {
		out.Warnings = append(out.Warnings, gen.Warning{Path: w.Path, Code: w.Code, Message: w.Message})
	}
	return out
}

// mapFilesError maps the files service's typed errors onto the envelope —
// the strict-server port of the feature's Huma-era mapper.
func mapFilesError(err error) error {
	switch {
	case errors.Is(err, files.ErrProjectRepoNotFound):
		return errNotFound("project repository not found")
	case errors.Is(err, files.ErrFileNotFound):
		return errNotFound("file not found")
	case errors.Is(err, files.ErrPathInvalid):
		return errBadRequest(err.Error())
	case errors.Is(err, sourcecontrol.ErrRefNotFastForward):
		// Workspace.Mutate exhausted its CAS retries: the ref tip moved under
		// us on every attempt. That is a concurrent-write conflict, not a
		// server fault — surface it as a retryable 409, never a 500.
		return errConflict("the repository changed during the write; retry")
	default:
		return errInternal("internal error")
	}
}
