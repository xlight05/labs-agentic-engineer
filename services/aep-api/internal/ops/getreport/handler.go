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

package getreport

import (
	"context"
	"log/slog"
	"strings"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/ops"
	"github.com/wso2/aep/aep-api/internal/platform/apierr"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// Handler serves get-rca-agent-report.
type Handler struct {
	reports ops.Repository
	execs   ops.ExecutionReader
}

// New returns the slice's handler. execs may be nil, which disables correlation.
func New(reports ops.Repository, execs ops.ExecutionReader) *Handler {
	return &Handler{reports: reports, execs: execs}
}

// GetRcaAgentReport returns a single report, 404 when absent.
//
// The report's Dispatched/Deployed fields are reconciled against live Task
// executions so the console's Coding Handover / Verify Fix stepper reflects the
// current state rather than the write-time snapshot.
func (h *Handler) GetRcaAgentReport(ctx context.Context, request gen.GetRcaAgentReportRequestObject) (gen.GetRcaAgentReportResponseObject, error) {
	org := tenant.BoundOrgFromContext(ctx)

	report, err := h.reports.Get(ctx, org, request.ReportID)
	if err != nil {
		return nil, apierr.Internal("failed to get rca-agent report")
	}
	if report == nil {
		return nil, apierr.NotFound("rca-agent report not found")
	}
	h.correlate(ctx, org, report)
	return gen.GetRcaAgentReport200JSONResponse(ops.ToWire(*report)), nil
}

// correlate upgrades a report's Dispatched/Deployed flags from the live
// executions of the linked Task (repo + issue number).
//
// It only ever promotes false→true, never the reverse: a writer that already
// knew the agent was dispatched must not be contradicted by a missing execution
// row. A no-op when correlation is disabled (nil reader), the report has no
// linked issue, or the issue URL cannot be resolved to a repo.
func (h *Handler) correlate(ctx context.Context, orgID string, report *ops.RcaAgentReport) {
	if h.execs == nil || report == nil || report.IssueNumber == nil {
		return
	}
	repo := repoFromIssueURL(report.IssueURL)
	if repo == "" {
		return
	}
	execs, err := h.execs.LatestExecutionPerKind(ctx, orgID, repo, int(*report.IssueNumber))
	if err != nil {
		// Correlation is best-effort: serve the stored snapshot on lookup error.
		slog.WarnContext(ctx, "ops: execution correlation failed",
			"issue", *report.IssueNumber, "repo", repo, "error", err)
		return
	}
	if _, ok := execs[string(taskmeta.KindCoding)]; ok {
		report.Dispatched = true
	}
	// Deployed = a build execution for this Task has SUCCEEDED (the fix built and
	// rolled) — deliberately beyond merely PR-merged, per issue #156's
	// "Verify Fix" requirement.
	if b, ok := execs[string(taskmeta.KindBuild)]; ok && b.Status == string(taskmeta.ExecSucceeded) {
		report.Deployed = true
		if report.DeployedAt == nil {
			report.DeployedAt = b.EndedAt
		}
	}
}

// repoFromIssueURL extracts the "<owner>/<name>" GitHub slug (the key the
// executions store is scoped by) from a full issue URL like
// https://github.com/<owner>/<name>/issues/<n>. Returns "" if it cannot.
func repoFromIssueURL(issueURL string) string {
	const marker = "github.com/"
	i := strings.Index(issueURL, marker)
	if i < 0 {
		return ""
	}
	parts := strings.Split(issueURL[i+len(marker):], "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}
