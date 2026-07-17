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

package createreport

import (
	"context"
	"fmt"

	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/ops"
	"github.com/wso2/aep/aep-api/internal/platform/apierr"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// validClassifications is the closed set the handoff agent may send.
var validClassifications = map[string]bool{
	"code-level":   true,
	"config-level": true,
	"mixed":        true,
	"none":         true,
}

// Handler serves create-rca-agent-report.
type Handler struct{ reports ops.Repository }

// New returns the slice's handler.
func New(reports ops.Repository) *Handler { return &Handler{reports: reports} }

// CreateRcaAgentReport validates and persists a new report.
//
// The caller is any userJWT holder scoped to the org — including a
// widened-audience service-account token; there is no separate service-auth
// scheme (BE handshake #156). The deny-by-default tenant gate binds the active
// org from the verified token before this runs, so org is read from the context
// and never from the request.
func (h *Handler) CreateRcaAgentReport(ctx context.Context, request gen.CreateRcaAgentReportRequestObject) (gen.CreateRcaAgentReportResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)

	report, err := toDomain(org, request.Body)
	if err != nil {
		return nil, apierr.BadRequest(err.Error())
	}
	if err := h.reports.Create(ctx, report); err != nil {
		return nil, apierr.Internal("failed to create rca-agent report")
	}
	return gen.CreateRcaAgentReport201JSONResponse(ops.ToWire(*report)), nil
}

// toDomain validates the wire body and maps it onto the domain entity. Fields
// the contract marks required are enforced here rather than left to a DB NOT
// NULL error, so the caller gets a precise 400.
func toDomain(org string, in *gen.CreateRcaAgentReportRequest) (*ops.RcaAgentReport, error) {
	if in == nil {
		return nil, fmt.Errorf("%w: request body is required", ops.ErrInvalidReport)
	}
	var missing []string
	if in.Project == "" {
		missing = append(missing, "project")
	}
	if in.Title == "" {
		missing = append(missing, "title")
	}
	if in.Summary == "" {
		missing = append(missing, "summary")
	}
	if in.Diagnosis == "" {
		missing = append(missing, "diagnosis")
	}
	if in.Classification == "" {
		missing = append(missing, "classification")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: missing required field(s): %v", ops.ErrInvalidReport, missing)
	}
	if !validClassifications[in.Classification] {
		return nil, fmt.Errorf("%w: classification %q must be one of code-level, config-level, mixed, none",
			ops.ErrInvalidReport, in.Classification)
	}
	return &ops.RcaAgentReport{
		OrgID:          org,
		Project:        in.Project,
		Component:      in.Component,
		Title:          in.Title,
		Summary:        in.Summary,
		Classification: in.Classification,
		Diagnosis:      in.Diagnosis,
		IssueNumber:    in.IssueNumber,
		IssueURL:       in.IssueURL,
		IssueTitle:     in.IssueTitle,
		IssueExcerpt:   in.IssueExcerpt,
		Dispatched:     in.Dispatched,
		Deployed:       in.Deployed,
		DeployedAt:     in.DeployedAt,
	}, nil
}
