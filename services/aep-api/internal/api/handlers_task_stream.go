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
	"net/http"

	"github.com/wso2/aep/aep-api/internal/feature/execution"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// stream-task-log on the strict interface: GET
// /projects/{projectName}/tasks/{issueNumber}/log → text/event-stream. The
// handler runs the pre-stream fences via the service (a bad path answers a
// normal JSON envelope, never a broken half-stream) and returns a response
// object whose Visit method writes the SSE preamble and hands the body writer
// to the connection loop — the loop itself (frame framing, task/execution/
// line/done frames, keep-alives, settle semantics) is TaskStreamService.run,
// reused verbatim.

func (s *legacyHandlers) StreamTaskLog(ctx context.Context, request gen.StreamTaskLogRequestObject) (gen.StreamTaskLogResponseObject, error) {
	if s.deps.TaskStream == nil {
		return nil, errServiceUnavailable("task stream not configured")
	}
	org := tenant.BoundOrgFromContext(ctx)
	run, err := s.deps.TaskStream.OpenTaskLogStream(ctx, org, request.ProjectName, int(request.IssueNumber))
	if err != nil {
		return nil, mapTaskStreamError(err)
	}
	return taskLogStreamResponse{run: run}, nil
}

// taskLogStreamResponse adapts the connection loop onto the generated
// ResponseObject: the strict wrapper calls Visit after the handler returns,
// which is where the stream actually runs (the captured request ctx stays
// alive until the loop exits; its cancellation is the client-disconnect
// signal).
type taskLogStreamResponse struct {
	run func(w io.Writer, flush func())
}

func (r taskLogStreamResponse) VisitStreamTaskLogResponse(w http.ResponseWriter) error {
	return sseStream(w, r.run)
}

// mapTaskStreamError translates the pre-stream fence sentinels into the
// envelope, mirroring the retired Huma registration's ladder.
func mapTaskStreamError(err error) error {
	switch {
	case errors.Is(err, execution.ErrTaskStreamRepoNotFound):
		return errNotFound("project repository not found")
	case errors.Is(err, execution.ErrTaskStreamTaskNotFound):
		return errNotFound("task not found")
	case errors.Is(err, execution.ErrTaskStreamSnapshot):
		return errInternal("task snapshot failed")
	default:
		return errInternal("internal error")
	}
}
