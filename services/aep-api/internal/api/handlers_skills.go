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
	"io"
	"mime/multipart"

	"github.com/wso2/aep/aep-api/internal/feature/skills"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
	"github.com/wso2/aep/aep-api/models"
)

// Skills feature on the strict interface: the org-scoped skills catalogue
// (list/get/create/update/delete), the built-in updates badge + sync, and the
// multipart AgentSkills-tarball import. Every operation is org-scoped — the
// deny-by-default tenant gate bound the token org into the context, and the
// handlers pass it to the services as an explicit argument (the services also
// take it as the actor, exactly as the retired Huma handlers did).

// skillImportMaxUploadBytes caps the multipart upload the BFF hands to the
// import service — bounds memory on the import path (the service applies its
// own decompressed-payload budget). Mirrors the legacy upload ceiling
// (skills/skill_upload.go's importMaxUploadBytes, which retires with
// skill_huma.go).
const skillImportMaxUploadBytes = 4 << 20 // 4 MiB

func (s *legacyHandlers) ListSkills(ctx context.Context, _ gen.ListSkillsRequestObject) (gen.ListSkillsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	summaries, err := s.deps.SkillSvc.ListSummaries(ctx, org)
	if err != nil {
		return nil, mapSkillError(err)
	}
	// repoUrl is the org skills repo's HTML URL (contract
	// SkillSummaryList.repoUrl) — "" while the repo can't be provisioned
	// (e.g. GitHub not connected yet); the console keys its Import dialog's
	// via-pull-request guidance off it.
	out := gen.SkillSummaryList{
		Skills:  make([]gen.SkillSummary, 0, len(summaries)),
		RepoURL: s.deps.SkillSvc.RepoWebURL(ctx, org),
	}
	for _, sum := range summaries {
		out.Skills = append(out.Skills, gen.SkillSummary{
			Name:        sum.Name,
			Kind:        sum.Kind,
			Description: sum.Description,
			ContentSha:  sum.ContentSHA,
			Editable:    sum.Editable,
		})
	}
	return gen.ListSkills200JSONResponse(out), nil
}

func (s *legacyHandlers) CreateSkill(ctx context.Context, request gen.CreateSkillRequestObject) (gen.CreateSkillResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if s.deps.SkillMutationSvc == nil {
		return nil, errServiceUnavailable("skill mutation not configured")
	}
	in := skills.CreateSkillInput{
		Name:       request.Body.Name,
		SkillMD:    request.Body.SkillMd,
		References: request.Body.References,
	}
	sk, err := s.deps.SkillMutationSvc.Create(ctx, org, org, in)
	if err != nil {
		return nil, mapSkillError(err)
	}
	return gen.CreateSkill201JSONResponse(skillDetailBody(sk, true)), nil
}

func (s *legacyHandlers) ImportSkill(ctx context.Context, request gen.ImportSkillRequestObject) (gen.ImportSkillResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if s.deps.SkillImportSvc == nil {
		return nil, errServiceUnavailable("skill import not configured")
	}
	file, err := multipartFormFilePart(request.Body, "file")
	if err != nil {
		return nil, err
	}
	// Cap the bytes handed to the import service to the legacy upload
	// ceiling; the service applies its own decompressed-payload budget.
	var reader io.Reader = io.LimitReader(file, skillImportMaxUploadBytes)
	result, err := s.deps.SkillImportSvc.Import(ctx, org, org, reader)
	if err != nil {
		return nil, mapSkillError(err)
	}
	return gen.ImportSkill201JSONResponse(gen.ImportResult{
		Name:          result.Name,
		Kind:          result.Kind,
		License:       result.License,
		Compatibility: result.Compatibility,
		Warnings:      result.Warnings,
	}), nil
}

func (s *legacyHandlers) ListSkillUpdates(ctx context.Context, _ gen.ListSkillUpdatesRequestObject) (gen.ListSkillUpdatesResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	updates, err := s.deps.SkillSvc.UpdatesAvailable(ctx, org)
	if err != nil {
		return nil, mapSkillError(err)
	}
	// Non-nil so JSON marshals as [] not null — the console reads
	// updates.length directly.
	out := gen.SkillUpdateList{
		Updates: make([]gen.SkillUpdate, 0, len(updates)),
		Count:   int64(len(updates)),
	}
	for _, u := range updates {
		out.Updates = append(out.Updates, gen.SkillUpdate{Name: u.Name})
	}
	return gen.ListSkillUpdates200JSONResponse(out), nil
}

func (s *legacyHandlers) SyncSkills(ctx context.Context, _ gen.SyncSkillsRequestObject) (gen.SyncSkillsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	updated, err := s.deps.SkillSvc.Reconcile(ctx, org)
	if err != nil {
		return nil, mapSkillError(err)
	}
	return gen.SyncSkills200JSONResponse(gen.SkillSyncOutput{
		Status:  "synced",
		Updated: int64(updated),
	}), nil
}

func (s *legacyHandlers) GetSkill(ctx context.Context, request gen.GetSkillRequestObject) (gen.GetSkillResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := requireSlug("name", request.Name); err != nil {
		return nil, err
	}
	sk, err := s.deps.SkillSvc.Resolve(ctx, org, request.Name)
	if err != nil {
		return nil, mapSkillError(err)
	}
	if sk == nil {
		return nil, errNotFound("skill not found")
	}
	return gen.GetSkill200JSONResponse(skillDetailBody(sk, skillEditable(sk.Kind))), nil
}

func (s *legacyHandlers) UpdateSkill(ctx context.Context, request gen.UpdateSkillRequestObject) (gen.UpdateSkillResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if s.deps.SkillMutationSvc == nil {
		return nil, errServiceUnavailable("skill mutation not configured")
	}
	if err := requireSlug("name", request.Name); err != nil {
		return nil, err
	}
	in := skills.UpdateSkillInput{
		SkillMD:    request.Body.SkillMd,
		References: request.Body.References,
	}
	sk, err := s.deps.SkillMutationSvc.Update(ctx, org, org, request.Name, in)
	if err != nil {
		return nil, mapSkillError(err)
	}
	return gen.UpdateSkill200JSONResponse(skillDetailBody(sk, true)), nil
}

func (s *legacyHandlers) DeleteSkill(ctx context.Context, request gen.DeleteSkillRequestObject) (gen.DeleteSkillResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if s.deps.SkillMutationSvc == nil {
		return nil, errServiceUnavailable("skill mutation not configured")
	}
	if err := requireSlug("name", request.Name); err != nil {
		return nil, err
	}
	if err := s.deps.SkillMutationSvc.Delete(ctx, org, org, request.Name); err != nil {
		return nil, mapSkillError(err)
	}
	return gen.DeleteSkill200JSONResponse(map[string]any{
		"status": "deleted",
		"name":   request.Name,
	}), nil
}

// skillDetailBody projects a resolved Skill + the derived editable flag onto
// the contract's SkillDetailBody (the full single-skill response).
func skillDetailBody(sk *skills.Skill, editable bool) gen.SkillDetailBody {
	return gen.SkillDetailBody{
		OrgID:         sk.OrgID,
		Name:          sk.Name,
		Kind:          sk.Kind,
		Description:   sk.Description,
		SkillMd:       sk.SkillMD,
		References:    sk.References,
		ContentSha:    sk.ContentSHA,
		License:       sk.License,
		Compatibility: sk.Compatibility,
		UpdatedAt:     sk.UpdatedAt,
		Editable:      editable,
	}
}

// skillEditable mirrors the skills feature's user-kind rule: only user-owned
// kinds (custom/imported) are editable — org + platform are reconcile-managed.
func skillEditable(kind string) bool {
	return kind == models.SkillKindCustom || kind == models.SkillKindImported
}

// multipartFormFilePart advances the strict server's multipart.Reader to the
// named file field and returns that part as a stream. A body without the
// field keeps the retired Huma handler's 400.
func multipartFormFilePart(body *multipart.Reader, field string) (io.Reader, error) {
	if body == nil {
		return nil, errBadRequest("missing '" + field + "' field (tarball)")
	}
	for {
		part, err := body.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, errBadRequest("missing '" + field + "' field (tarball)")
		}
		if err != nil {
			return nil, errBadRequest("can't decode multipart body: " + err.Error())
		}
		if part.FormName() == field {
			return part, nil
		}
	}
}

// mapSkillError translates skill service sentinels + structured validation
// errors onto the flat envelope, mirroring the retired Huma handler's status
// classification.
func mapSkillError(err error) error {
	var verr *skills.SkillValidationError
	switch {
	case errors.As(err, &verr):
		return errBadRequest(verr.Error())
	case errors.Is(err, skills.ErrSkillNameCollision):
		return errConflict(err.Error())
	case errors.Is(err, skills.ErrSkillNotEditable):
		return errForbidden("built-in skills are read-only")
	case errors.Is(err, skills.ErrSkillNotFound):
		return errNotFound("skill not found")
	}
	return errInternal("internal error")
}
