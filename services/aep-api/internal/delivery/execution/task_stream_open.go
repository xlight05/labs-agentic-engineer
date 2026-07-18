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

package execution

// task_stream_open.go — the strict-server entry point for the task-log SSE
// stream (contract: GET /projects/{p}/tasks/{issueNumber}/log). The Huma
// registration in task_stream.go ran the same pre-stream fences inline; the
// contract-first edge (internal/api/handlers_task_stream.go) needs an exported
// seam because the connection loop (run) is deliberately unexported. The loop
// itself is reused verbatim — frame framing, dedup/cursor state, hub notify +
// tick cadence, and settle semantics all live in task_stream.go, untouched.

import (
	"context"
	"errors"
	"io"
)

// Pre-stream fence outcomes, mapped onto the error envelope by the HTTP edge.
var (
	// ErrTaskStreamRepoNotFound: the project has no provisioned repository —
	// the hub key half cannot resolve. Mapped to 404.
	ErrTaskStreamRepoNotFound = errors.New("project repository not found")
	// ErrTaskStreamTaskNotFound: the issue is not a Task (no marker) or does
	// not exist — includes the cross-org miss (the snapshot reader is
	// org-fenced and resolves nil). Mapped to 404, never "exists but forbidden".
	ErrTaskStreamTaskNotFound = errors.New("task not found")
	// ErrTaskStreamSnapshot: the pre-stream Task snapshot read failed. Mapped
	// to 500.
	ErrTaskStreamSnapshot = errors.New("task snapshot failed")
)

// OpenTaskLogStream runs the pre-stream fences — resolve the hub key and
// verify the issue is a real Task — so a bad path answers a normal JSON error
// (not a broken half-stream), then returns one connection's run loop with
// everything it needs captured (including ctx: the request context, whose
// cancellation is the client-disconnect signal). The returned func carries the
// whole stream lifetime and reuses the pre-fetched snapshot (no double GitHub
// read).
func (s *TaskStreamService) OpenTaskLogStream(ctx context.Context, orgID, projectID string, issue int) (func(w io.Writer, flush func()), error) {
	repo, err := s.repos.RepoFullName(ctx, orgID, projectID)
	if err != nil {
		return nil, ErrTaskStreamRepoNotFound
	}
	snap, err := s.tasks.TaskSnapshot(ctx, orgID, projectID, issue)
	if err != nil {
		return nil, ErrTaskStreamSnapshot
	}
	if snap == nil {
		return nil, ErrTaskStreamTaskNotFound
	}
	return func(w io.Writer, flush func()) {
		s.run(ctx, w, flush, orgID, projectID, repo, issue, snap)
	}, nil
}
