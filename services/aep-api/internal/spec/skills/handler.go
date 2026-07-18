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

package skills

import (
	"context"
	"errors"
	"io"
	"mime/multipart"

	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/apierr"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
	"github.com/wso2/aep/aep-api/internal/platform/validate"
	"github.com/wso2/aep/aep-api/internal/spec"
	"github.com/wso2/aep/aep-api/models"
)

// Handler serves the org-scoped skills catalogue (list/get/create/update/
// delete), the built-in updates badge + sync, and the multipart
// AgentSkills-tarball import. Every operation is org-scoped — the
// deny-by-default tenant gate bound the token org into the context, and the
// handlers pass it to the services as an explicit argument (the services also
// take it as the actor, exactly as the retired Huma handlers did).
type Handler struct {
	skills *spec.SkillService
	mut    *spec.SkillMutationService
	imp    *spec.SkillImportService
}

// New returns the slice's handler.
func New(skills *spec.SkillService, mut *spec.SkillMutationService, imp *spec.SkillImportService) *Handler {
	return &Handler{skills: skills, mut: mut, imp: imp}
}

// skillImportMaxUploadBytes caps the multipart upload the BFF hands to the
// import service — bounds memory on the import path (the service applies its
// own decompressed-payload budget). Mirrors the legacy upload ceiling
// (skills/skill_upload.go's importMaxUploadBytes, which retires with
// skill_huma.go).
const skillImportMaxUploadBytes = 4 << 20 // 4 MiB

func (h *Handler) ListSkills(ctx context.Context, _ gen.ListSkillsRequestObject) (gen.ListSkillsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	summaries, err := h.skills.ListSummaries(ctx, org)
	if err != nil {
		return nil, mapSkillError(err)
	}
	// repoUrl is the org skills repo's HTML URL (contract
	// SkillSummaryList.repoUrl) — "" while the repo can't be provisioned
	// (e.g. GitHub not connected yet); the console keys its Import dialog's
	// via-pull-request guidance off it.
	out := gen.SkillSummaryList{
		Skills:  make([]gen.SkillSummary, 0, len(summaries)),
		RepoURL: h.skills.RepoWebURL(ctx, org),
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

func (h *Handler) CreateSkill(ctx context.Context, request gen.CreateSkillRequestObject) (gen.CreateSkillResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if h.mut == nil {
		return nil, apierr.ServiceUnavailable("skill mutation not configured")
	}
	in := spec.CreateSkillInput{
		Name:       request.Body.Name,
		SkillMD:    request.Body.SkillMd,
		References: request.Body.References,
	}
	sk, err := h.mut.Create(ctx, org, org, in)
	if err != nil {
		return nil, mapSkillError(err)
	}
	return gen.CreateSkill201JSONResponse(skillDetailBody(sk, true)), nil
}

func (h *Handler) ImportSkill(ctx context.Context, request gen.ImportSkillRequestObject) (gen.ImportSkillResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if h.imp == nil {
		return nil, apierr.ServiceUnavailable("skill import not configured")
	}
	file, err := multipartFormFilePart(request.Body, "file")
	if err != nil {
		return nil, err
	}
	// Cap the bytes handed to the import service to the legacy upload
	// ceiling; the service applies its own decompressed-payload budget.
	var reader io.Reader = io.LimitReader(file, skillImportMaxUploadBytes)
	result, err := h.imp.Import(ctx, org, org, reader)
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

func (h *Handler) ListSkillUpdates(ctx context.Context, _ gen.ListSkillUpdatesRequestObject) (gen.ListSkillUpdatesResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	updates, err := h.skills.UpdatesAvailable(ctx, org)
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

func (h *Handler) SyncSkills(ctx context.Context, _ gen.SyncSkillsRequestObject) (gen.SyncSkillsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	updated, err := h.skills.Reconcile(ctx, org)
	if err != nil {
		return nil, mapSkillError(err)
	}
	return gen.SyncSkills200JSONResponse(gen.SkillSyncOutput{
		Status:  "synced",
		Updated: int64(updated),
	}), nil
}

func (h *Handler) GetSkill(ctx context.Context, request gen.GetSkillRequestObject) (gen.GetSkillResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if err := requireSlug("name", request.Name); err != nil {
		return nil, err
	}
	sk, err := h.skills.Resolve(ctx, org, request.Name)
	if err != nil {
		return nil, mapSkillError(err)
	}
	if sk == nil {
		return nil, apierr.NotFound("skill not found")
	}
	return gen.GetSkill200JSONResponse(skillDetailBody(sk, skillEditable(sk.Kind))), nil
}

func (h *Handler) UpdateSkill(ctx context.Context, request gen.UpdateSkillRequestObject) (gen.UpdateSkillResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if h.mut == nil {
		return nil, apierr.ServiceUnavailable("skill mutation not configured")
	}
	if err := requireSlug("name", request.Name); err != nil {
		return nil, err
	}
	in := spec.UpdateSkillInput{
		SkillMD:    request.Body.SkillMd,
		References: request.Body.References,
	}
	sk, err := h.mut.Update(ctx, org, org, request.Name, in)
	if err != nil {
		return nil, mapSkillError(err)
	}
	return gen.UpdateSkill200JSONResponse(skillDetailBody(sk, true)), nil
}

func (h *Handler) DeleteSkill(ctx context.Context, request gen.DeleteSkillRequestObject) (gen.DeleteSkillResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if h.mut == nil {
		return nil, apierr.ServiceUnavailable("skill mutation not configured")
	}
	if err := requireSlug("name", request.Name); err != nil {
		return nil, err
	}
	if err := h.mut.Delete(ctx, org, org, request.Name); err != nil {
		return nil, mapSkillError(err)
	}
	return gen.DeleteSkill200JSONResponse(map[string]any{
		"status": "deleted",
		"name":   request.Name,
	}), nil
}

// skillDetailBody projects a resolved Skill + the derived editable flag onto
// the contract's SkillDetailBody (the full single-skill response).
func skillDetailBody(sk *spec.Skill, editable bool) gen.SkillDetailBody {
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

// requireSlug validates a single DNS-label slug path param, returning a 400
// envelope error on failure. Delegates to validate.Slug.
func requireSlug(name, v string) error {
	if err := validate.Slug(v); err != nil {
		return apierr.BadRequest(name + ": " + err.Error())
	}
	return nil
}

// multipartFormFilePart advances the strict server's multipart.Reader to the
// named file field and returns that part as a stream. A body without the
// field keeps the retired Huma handler's 400.
func multipartFormFilePart(body *multipart.Reader, field string) (io.Reader, error) {
	if body == nil {
		return nil, apierr.BadRequest("missing '" + field + "' field (tarball)")
	}
	for {
		part, err := body.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, apierr.BadRequest("missing '" + field + "' field (tarball)")
		}
		if err != nil {
			return nil, apierr.BadRequest("can't decode multipart body: " + err.Error())
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
	var verr *spec.SkillValidationError
	switch {
	case errors.As(err, &verr):
		return apierr.BadRequest(verr.Error())
	case errors.Is(err, spec.ErrSkillNameCollision):
		return apierr.Conflict(err.Error())
	case errors.Is(err, spec.ErrSkillNotEditable):
		return apierr.Forbidden("built-in skills are read-only")
	case errors.Is(err, spec.ErrSkillNotFound):
		return apierr.NotFound("skill not found")
	}
	return apierr.Internal("internal error")
}
