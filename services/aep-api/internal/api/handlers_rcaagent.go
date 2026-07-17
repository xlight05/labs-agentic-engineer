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

	"github.com/wso2/aep/aep-api/internal/feature/rcaagent"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// RCA-agent reports on the strict interface (issues #154, #155, BE handshake
// #156). Per the handshake, the caller is any userJWT holder scoped to the
// org — including a widened-audience service-account token — there is no
// separate service-auth scheme; the deny-by-default tenant gate binds the
// active org from the verified token before these run.

func (s *legacyHandlers) ListRcaAgentReports(ctx context.Context, request gen.ListRcaAgentReportsRequestObject) (gen.ListRcaAgentReportsResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	cursor, limit := "", 0
	if request.Params.Cursor != "" {
		cursor = request.Params.Cursor
	}
	if request.Params.Limit != 0 {
		limit = request.Params.Limit
	}
	out, err := s.deps.RcaAgentReportSvc.ListReports(ctx, org, cursor, limit)
	if err != nil {
		return nil, errInternal("failed to list rca-agent reports")
	}
	return gen.ListRcaAgentReports200JSONResponse(*out), nil
}

func (s *legacyHandlers) CreateRcaAgentReport(ctx context.Context, request gen.CreateRcaAgentReportRequestObject) (gen.CreateRcaAgentReportResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	out, err := s.deps.RcaAgentReportSvc.CreateReport(ctx, org, request.Body)
	if err != nil {
		if errors.Is(err, rcaagent.ErrInvalidReport) {
			return nil, errBadRequest(err.Error())
		}
		return nil, errInternal("failed to create rca-agent report")
	}
	return gen.CreateRcaAgentReport201JSONResponse(*out), nil
}

func (s *legacyHandlers) GetRcaAgentReport(ctx context.Context, request gen.GetRcaAgentReportRequestObject) (gen.GetRcaAgentReportResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)
	out, err := s.deps.RcaAgentReportSvc.GetReport(ctx, org, request.ReportID)
	if err != nil {
		if errors.Is(err, rcaagent.ErrReportNotFound) {
			return nil, errNotFound("rca-agent report not found")
		}
		return nil, errInternal("failed to get rca-agent report")
	}
	return gen.GetRcaAgentReport200JSONResponse(*out), nil
}
