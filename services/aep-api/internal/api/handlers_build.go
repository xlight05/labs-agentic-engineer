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
	"net/http"

	"github.com/wso2/aep/aep-api/internal/feature/build"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// Build feature on the strict interface: build-project / get-project-build /
// list-project-builds plus the dependency-drawer get-build-preflight. Every
// operation is org-scoped — the tenant gate bound the token org before these
// run, and the handlers pass it to the service explicitly.
//
// Error dialect: build.Run is the same sequence the non-HTTP
// StartProjectBuild trigger uses, whose error sites still speak the huma-era
// edge vocabulary (one live copy — no fork until the central *_huma.go
// deletion). mapBuildRunError translates those onto the flat envelope; the
// former 422 spec-gate problem is a 400 validation_failed with details[] now
// (the error-model break).

func (s *legacyHandlers) BuildProject(ctx context.Context, request gen.BuildProjectRequestObject) (gen.BuildProjectResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	var inputs []build.BuildInputItem
	if request.Body != nil {
		inputs = toBuildInputItems(request.Body.Inputs)
	}
	tag, failures, err := s.deps.BuildSvc.Run(ctx, org, request.ProjectName, inputs)
	if err != nil {
		return nil, mapBuildRunError(err)
	}
	if len(failures) > 0 {
		return gen.BuildProject200JSONResponse(gen.BuildResponse{Failures: toInputFailures(failures)}), nil
	}
	return gen.BuildProject200JSONResponse(gen.BuildResponse{Tag: tag}), nil
}

func (s *legacyHandlers) GetProjectBuild(ctx context.Context, request gen.GetProjectBuildRequestObject) (gen.GetProjectBuildResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	st, err := s.deps.BuildSvc.Status(ctx, org, request.ProjectName, request.Tag)
	if err != nil {
		if errors.Is(err, build.ErrBuildNotFound) {
			return nil, errNotFound("build not found")
		}
		return nil, errInternal("lookup build")
	}
	return gen.GetProjectBuild200JSONResponse(toBuildStatus(st)), nil
}

func (s *legacyHandlers) ListProjectBuilds(ctx context.Context, request gen.ListProjectBuildsRequestObject) (gen.ListProjectBuildsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	list, err := s.deps.BuildSvc.List(ctx, org, request.ProjectName)
	if err != nil {
		return nil, errInternal("list builds")
	}
	return gen.ListProjectBuilds200JSONResponse(toBuildList(list)), nil
}

// GetBuildPreflight computes the build dependency-drawer preflight. A nil
// service answers 503, mirroring the retired RegisterPreflight nil guard (the
// surface exists with the feature unwired).
func (s *legacyHandlers) GetBuildPreflight(ctx context.Context, request gen.GetBuildPreflightRequestObject) (gen.GetBuildPreflightResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if s.deps.PreflightSvc == nil {
		return nil, errServiceUnavailable("build preflight is not configured")
	}
	pf, err := s.deps.PreflightSvc.Preflight(ctx, org, request.ProjectName)
	if err != nil {
		return nil, errInternal("compute build preflight: " + err.Error())
	}
	return gen.GetBuildPreflight200JSONResponse(toBuildPreflight(pf)), nil
}

// mapBuildRunError translates build.Run's failures onto the envelope: the
// already-running sentinel is a 409, and every build.Run *build.EdgeError
// carries its status, message, and per-file spec-gate details across (a 400
// with details is the validation_failed dialect).
func mapBuildRunError(err error) error {
	if errors.Is(err, build.ErrBuildAlreadyRunning) {
		return errConflict("a build is already running for this project")
	}
	var ee *build.EdgeError
	if !errors.As(err, &ee) {
		return errInternal("internal error")
	}
	switch ee.Status {
	case http.StatusBadRequest:
		return &apiError{http.StatusBadRequest, CodeValidationFailed, ee.Message, ee.Details}
	case http.StatusServiceUnavailable:
		return errServiceUnavailable(ee.Message)
	case http.StatusBadGateway:
		return errBadGateway(ee.Message)
	default:
		return errFromStatus(ee.Status, ee.Message)
	}
}

// --- schema <-> feature projections ------------------------------------------

func toBuildInputItems(in []gen.BuildInputItem) []build.BuildInputItem {
	if len(in) == 0 {
		return nil
	}
	out := make([]build.BuildInputItem, 0, len(in))
	for _, it := range in {
		var values []build.ConfigValue
		for _, v := range it.Values {
			values = append(values, build.ConfigValue{Key: v.Key, Value: v.Value})
		}
		out = append(out, build.BuildInputItem{
			Component:   it.Component,
			Dependency:  it.Dependency,
			Kind:        string(it.Kind),
			Values:      values,
			SpecContent: it.SpecContent,
			SpecURL:     it.SpecURL,
			Parameters:  it.Parameters,
			Approved:    it.Approved,
		})
	}
	return out
}

func toInputFailures(in []build.InputFailure) []gen.InputFailure {
	out := make([]gen.InputFailure, 0, len(in))
	for _, f := range in {
		out = append(out, gen.InputFailure{
			Component:  f.Component,
			Dependency: f.Dependency,
			Kind:       f.Kind,
			Reason:     f.Reason,
		})
	}
	return out
}

func toBuildStatus(st build.BuildStatus) gen.BuildStatus {
	var tasks []gen.BuildStatusTask
	for _, t := range st.Tasks {
		tasks = append(tasks, gen.BuildStatusTask{
			IssueNumber: t.IssueNumber,
			Title:       t.Title,
			Status:      gen.BuildStatusTaskStatus(t.Status),
		})
	}
	return gen.BuildStatus{
		Status:         st.Status,
		WorkflowStatus: st.WorkflowStatus,
		Reason:         st.Reason,
		Tasks:          tasks,
	}
}

func toBuildList(l build.BuildList) gen.BuildList {
	// Builds stays non-nil so the JSON body is [] rather than null.
	builds := make([]gen.BuildSummary, 0, len(l.Builds))
	for _, b := range l.Builds {
		s := gen.BuildSummary{
			Tag:       b.Tag,
			Status:    gen.BuildSummaryStatus(b.Status),
			Reason:    b.Reason,
			StartedAt: b.StartedAt,
			Tasks: gen.BuildTally{
				Total:  b.Tasks.Total,
				Done:   b.Tasks.Done,
				Failed: b.Tasks.Failed,
				Active: b.Tasks.Active,
			},
		}
		s.CompletedAt = b.CompletedAt // nil while running — omitted on the wire
		builds = append(builds, s)
	}
	return gen.BuildList{Builds: builds}
}

func toBuildPreflight(pf build.BuildPreflight) gen.BuildPreflight {
	// Items stays non-nil so the JSON body is [] rather than null.
	items := make([]gen.PreflightItem, 0, len(pf.Items))
	for _, it := range pf.Items {
		var cfg []gen.ConfigKeyView
		for _, k := range it.Config {
			cfg = append(cfg, gen.ConfigKeyView{
				Key:          k.Key,
				Secret:       k.Secret,
				Description:  k.Description,
				DefaultValue: k.DefaultValue,
			})
		}
		items = append(items, gen.PreflightItem{
			Component:    it.Component,
			Dependency:   it.Dependency,
			Kind:         gen.PreflightItemKind(it.Kind),
			Description:  it.Description,
			Config:       cfg,
			ResourceType: it.ResourceType,
			Parameters:   it.Parameters,
		})
	}
	return gen.BuildPreflight{NeedsInput: pf.NeedsInput, Items: items}
}
