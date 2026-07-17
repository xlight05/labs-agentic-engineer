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

	"github.com/wso2/aep/aep-api/internal/feature/task"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// Task reads (list-tasks / get-task) on the strict interface. Org comes from
// the gate-bound context and is passed to the service explicitly. The command
// and plan operations the retired Huma surface also carried (plan-tasks,
// execute-task, hold-task, unhold-task) are NOT in the committed contract and
// were deliberately dropped from the HTTP edge (parked proposal in
// packages/contracts/workflows). promote-task-from-issue STAYS: it is the
// dispatch leg of the SRE/RCA alert handoff, called by the deployed
// aep-mcp-server (AE-HANDOFF-DESIGN.md).

func (s *legacyHandlers) ListTasks(ctx context.Context, request gen.ListTasksRequestObject) (gen.ListTasksResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if s.deps.TaskReads == nil {
		return nil, errTasksNotConfigured()
	}
	state, tag := "", ""
	if request.Params.State != "" {
		state = string(request.Params.State)
	}
	if request.Params.Tag != "" {
		tag = request.Params.Tag
	}
	views, err := s.deps.TaskReads.ListByTag(ctx, org, request.ProjectName, state, tag)
	if err != nil {
		return nil, mapTaskReadError(err)
	}
	return listTasksJSONResponse(views), nil
}

func (s *legacyHandlers) GetTask(ctx context.Context, request gen.GetTaskRequestObject) (gen.GetTaskResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	if s.deps.TaskReads == nil {
		return nil, errTasksNotConfigured()
	}
	detail, err := s.deps.TaskReads.Get(ctx, org, request.ProjectName, int(request.IssueNumber))
	if err != nil {
		return nil, mapTaskReadError(err)
	}
	return getTaskJSONResponse(*detail), nil
}

// The 200 bodies are served from the feature's own view types (task.TaskView /
// task.TaskDetail) instead of the generated ListTasks200JSONResponse /
// GetTask200JSONResponse: the models generator's prefer-skip-optional-pointer
// renders the contract's OPTIONAL startedAt/endedAt (ExecutionView) as value
// time.Time fields whose `omitempty` never fires, so converting would stamp
// "0001-01-01T00:00:00Z" onto every absent timestamp — a wire regression the
// contract does not require. Marshaling the feature views verbatim keeps the
// wire identical to the retired Huma edge (generated-type defect noted in the
// migration report).

type listTasksJSONResponse []task.TaskView

func (r listTasksJSONResponse) VisitListTasksResponse(w http.ResponseWriter) error {
	return writeJSONBody(w, http.StatusOK, r)
}

type getTaskJSONResponse task.TaskDetail

func (r getTaskJSONResponse) VisitGetTaskResponse(w http.ResponseWriter) error {
	return writeJSONBody(w, http.StatusOK, task.TaskDetail(r))
}

// errTasksNotConfigured is the nil-service guard the Huma registration carried
// (503 "tasks not configured") — kept verbatim on the strict edge.
func errTasksNotConfigured() error {
	return errServiceUnavailable("tasks not configured")
}

// mapTaskReadError translates the read-path sentinels into the envelope,
// mirroring the retired mapReadError ladder.
func mapTaskReadError(err error) error {
	switch {
	case errors.Is(err, task.ErrTaskNotFound):
		return errNotFound("task not found")
	case errors.Is(err, task.ErrProjectRepoNotFound):
		return errNotFound(task.ErrProjectRepoNotFound.Error())
	default:
		return errInternal("internal error")
	}
}

// PromoteTaskFromIssue turns an ad-hoc GitHub issue into a coding Task and
// dispatches it through the funnel (async 202, empty body). The second half
// of the SRE/RCA handoff: aep-mcp-server calls this right after create-issue.
func (s *legacyHandlers) PromoteTaskFromIssue(ctx context.Context, request gen.PromoteTaskFromIssueRequestObject) (gen.PromoteTaskFromIssueResponseObject, error) {
	if s.deps.TaskCommands == nil {
		return nil, errServiceUnavailable("tasks not configured")
	}
	org := tenant.BoundOrgFromContext(ctx)
	if err := s.deps.TaskCommands.PromoteAndExecute(ctx, org, request.ProjectName, request.Body.ComponentName, int(request.IssueNumber)); err != nil {
		return nil, mapTaskCommandError(err)
	}
	return gen.PromoteTaskFromIssue202Response{}, nil
}

// mapTaskCommandError mirrors the retired mapCommandError ladder.
func mapTaskCommandError(err error) error {
	switch {
	case errors.Is(err, task.ErrTaskNotFound):
		return errNotFound("task not found")
	case errors.Is(err, task.ErrProjectRepoNotFound):
		return errNotFound(task.ErrProjectRepoNotFound.Error())
	case errors.Is(err, task.ErrIssueClosed):
		return errConflict("issue is closed")
	case errors.Is(err, task.ErrComponentNameRequired):
		return errBadRequest(task.ErrComponentNameRequired.Error())
	default:
		return errInternal("internal error")
	}
}
