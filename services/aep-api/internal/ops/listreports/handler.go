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

package listreports

import (
	"context"

	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/ops"
	"github.com/wso2/aep/aep-api/internal/platform/apierr"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// defaultListLimit matches the console bell's own default (issue #154: "last 50,
// no pagination") when the caller omits limit.
const defaultListLimit = 50

// maxListLimit caps a single page regardless of what the caller asks for.
const maxListLimit = 200

// Handler serves list-rca-agent-reports.
type Handler struct{ reports ops.Repository }

// New returns the slice's handler.
func New(reports ops.Repository) *Handler { return &Handler{reports: reports} }

// ListRcaAgentReports returns a page of reports, newest first.
func (h *Handler) ListRcaAgentReports(ctx context.Context, request gen.ListRcaAgentReportsRequestObject) (gen.ListRcaAgentReportsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)

	limit := request.Params.Limit
	switch {
	case limit <= 0:
		limit = defaultListLimit
	case limit > maxListLimit:
		limit = maxListLimit
	}

	reports, nextCursor, err := h.reports.List(ctx, org, request.Params.Cursor, limit)
	if err != nil {
		return nil, apierr.Internal("failed to list rca-agent reports")
	}
	return gen.ListRcaAgentReports200JSONResponse(gen.RcaAgentReportList{
		Items:      ops.ToWireList(reports),
		NextCursor: nextCursor,
	}), nil
}
