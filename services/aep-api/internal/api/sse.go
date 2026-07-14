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
	"errors"
	"io"
	"net/http"

	"github.com/wso2/aep/aep-api/internal/clients/agentsvc"
)

// sseStream stamps the standard SSE response preamble — the four event-stream
// headers, an explicit 200, and an initial flush so the headers reach the
// client before the first frame — then hands the body writer + a per-chunk
// flush func to run. It is the strict-server re-home of humakit.SSEBody:
// every streaming operation's response type wraps this in its
// VisitXxxResponse(w) method (frame framing and loop logic stay per-feature).
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

// mapAgentsUpstreamError applies the shared BFF edge policy for a pre-stream
// agents-service failure: if err is an *agentsvc.UpstreamError, the caller's
// per-status override (e.g. a typed 409 in-progress body) wins, and any other
// upstream status maps to 502. Returns (nil, false) when err is not an
// upstream error, so feature-specific sentinel mapping stays in the caller.
// (Strict-server re-home of humakit.MapAgentsUpstreamError.)
func mapAgentsUpstreamError(err error, overrides map[int]error) (error, bool) {
	var ue *agentsvc.UpstreamError
	if !errors.As(err, &ue) {
		return nil, false
	}
	if mapped, ok := overrides[ue.StatusCode]; ok {
		return mapped, true
	}
	return errBadGateway("agents service error"), true
}
