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

package runread

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/apierr"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// Handler serves the run read surface on the strict interface: the version's
// runs, the per-run progress stream, and cancel.
type Handler struct {
	reads       *Reads
	progress    *ProgressService
	commands    *Commands
	cycleBuilds *CycleBuilds
}

// NewHandler returns the slice's handler. Any nil service leaves its operations
// answering 503 rather than panicking — the degraded-boot contract every slice
// handler follows.
func NewHandler(reads *Reads, progress *ProgressService, commands *Commands, cycleBuilds *CycleBuilds) *Handler {
	return &Handler{reads: reads, progress: progress, commands: commands, cycleBuilds: cycleBuilds}
}

// ListBuildRuns serves GET /projects/{p}/builds/{tag}/runs.
func (h *Handler) ListBuildRuns(ctx context.Context, request gen.ListBuildRunsRequestObject) (gen.ListBuildRunsResponseObject, error) {
	if h.reads == nil {
		return nil, apierr.ServiceUnavailable("run reads not configured")
	}
	out, err := h.reads.RunsForTag(ctx, tenant.BoundOrgFromContext(ctx), request.ProjectName, request.Tag)
	if err != nil {
		return nil, mapRunError(err)
	}
	return gen.ListBuildRuns200JSONResponse(*out), nil
}

// ListCycleBuilds serves GET /projects/{p}/builds/{tag}/cycles/{cycleId}/builds.
func (h *Handler) ListCycleBuilds(ctx context.Context, request gen.ListCycleBuildsRequestObject) (gen.ListCycleBuildsResponseObject, error) {
	if h.cycleBuilds == nil {
		return nil, apierr.ServiceUnavailable("cycle build reads not configured")
	}
	out, err := h.cycleBuilds.ForCycle(ctx, tenant.BoundOrgFromContext(ctx), request.ProjectName, request.Tag, request.CycleID)
	if err != nil {
		return nil, mapRunError(err)
	}
	return gen.ListCycleBuilds200JSONResponse(*out), nil
}

// StreamRunProgress serves GET /projects/{p}/runs/{runId}/progress.
//
// The fences run HERE, before any byte is written, so a bad path answers a JSON
// envelope; the stream itself runs in Visit, after this returns.
func (h *Handler) StreamRunProgress(ctx context.Context, request gen.StreamRunProgressRequestObject) (gen.StreamRunProgressResponseObject, error) {
	if h.progress == nil {
		return nil, apierr.ServiceUnavailable("run progress stream not configured")
	}
	run, err := h.progress.OpenRunProgressStream(ctx, tenant.BoundOrgFromContext(ctx), request.ProjectName, request.RunID)
	if err != nil {
		return nil, mapRunError(err)
	}
	return runProgressStreamResponse{run: run}, nil
}

// CancelRun serves POST /projects/{p}/runs/{runId}/cancel.
func (h *Handler) CancelRun(ctx context.Context, request gen.CancelRunRequestObject) (gen.CancelRunResponseObject, error) {
	if h.commands == nil {
		return nil, apierr.ServiceUnavailable("run cancel not configured")
	}
	if err := h.commands.Cancel(ctx, tenant.BoundOrgFromContext(ctx), request.ProjectName, request.RunID); err != nil {
		return nil, mapRunError(err)
	}
	return gen.CancelRun202Response{}, nil
}

// runProgressStreamResponse adapts the connection loop onto the generated
// ResponseObject: the strict wrapper calls Visit after the handler returns,
// which is where the stream actually runs (the captured request ctx stays alive
// until the loop exits; its cancellation is the client-disconnect signal).
type runProgressStreamResponse struct {
	run func(w io.Writer, flush func())
}

func (r runProgressStreamResponse) VisitStreamRunProgressResponse(w http.ResponseWriter) error {
	return sseStream(w, r.run)
}

// mapRunError translates this slice's sentinels into the error envelope.
// Both not-found sentinels are 404 including the cross-org case: the org-scoped
// read misses, so "belongs to someone else" is indistinguishable from "does not
// exist" and existence never leaks.
func mapRunError(err error) error {
	switch {
	case errors.Is(err, ErrTagNotFound):
		return apierr.NotFound("no run for this version")
	case errors.Is(err, ErrRunNotFound):
		return apierr.NotFound("run not found")
	case errors.Is(err, ErrCycleNotFound):
		return apierr.NotFound("cycle not found")
	case errors.Is(err, delivery.ErrTemporalUnavailable):
		// Nothing was cancelled and the caller may retry — a 503, not a 500.
		return apierr.ServiceUnavailable("the workflow engine is unavailable — nothing was cancelled")
	default:
		return apierr.Internal("internal error")
	}
}

// sseStream stamps the standard SSE response preamble — the four event-stream
// headers, an explicit 200, and an initial flush so the headers reach the client
// before the first frame — then hands the body writer + a per-chunk flush func
// to run. A slice-local copy, matching the other streaming slices: framing and
// loop logic stay per-feature, and this preamble is the only shared shape.
func sseStream(w http.ResponseWriter, run func(w io.Writer, flush func())) error {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	flush()
	run(w, flush)
	return nil
}
