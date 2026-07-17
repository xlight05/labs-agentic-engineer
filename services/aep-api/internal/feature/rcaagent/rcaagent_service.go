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

package rcaagent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/wso2/aep/aep-api/internal/gen"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/models"
)

// ErrReportNotFound is returned by GetReport when no report exists for the
// given (org, id) — the huma handler maps it to a 404.
var ErrReportNotFound = errors.New("rca agent report not found")

// ErrInvalidReport is returned by CreateReport when the request fails
// validation — the huma handler maps it to a 400.
var ErrInvalidReport = errors.New("invalid rca agent report")

// defaultListLimit matches the console bell's own default (issue #154:
// "last 50, no pagination") when the caller omits limit.
const defaultListLimit = 50

// maxListLimit caps a single page regardless of what the caller asks for.
const maxListLimit = 200

var validClassifications = map[string]bool{
	"code-level":   true,
	"config-level": true,
	"mixed":        true,
	"none":         true,
}

// RcaAgentReportService is the read + write surface backing the console's
// Alerts notification bell and Alerts list/stepper (issues #154, #155, BE
// handshake #156).
type RcaAgentReportService interface {
	CreateReport(ctx context.Context, orgID string, in *gen.CreateRcaAgentReportRequest) (*models.RcaAgentReport, error)
	GetReport(ctx context.Context, orgID, id string) (*models.RcaAgentReport, error)
	ListReports(ctx context.Context, orgID, cursor string, limit int) (*gen.RcaAgentReportList, error)
}

type rcaAgentReportService struct {
	repo  Repository
	execs ExecutionReader
}

// NewRcaAgentReportService returns a service backed by repo. execs is optional
// (may be nil): when supplied, GetReport reconciles a report's
// Dispatched/Deployed snapshot against live Task executions.
func NewRcaAgentReportService(repo Repository, execs ExecutionReader) RcaAgentReportService {
	return &rcaAgentReportService{repo: repo, execs: execs}
}

// CreateReport validates and persists a new report. Fields the contract
// marks required are enforced here (not left to a DB NOT NULL 500) so the
// caller gets a precise 400.
func (s *rcaAgentReportService) CreateReport(ctx context.Context, orgID string, in *gen.CreateRcaAgentReportRequest) (*models.RcaAgentReport, error) {
	if err := validateCreate(in); err != nil {
		return nil, err
	}
	report, err := s.repo.Create(ctx, orgID, in)
	if err != nil {
		return nil, fmt.Errorf("create report: %w", err)
	}
	return report, nil
}

func validateCreate(in *gen.CreateRcaAgentReportRequest) error {
	if in == nil {
		return fmt.Errorf("%w: request body is required", ErrInvalidReport)
	}
	missing := []string{}
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
		return fmt.Errorf("%w: missing required field(s): %v", ErrInvalidReport, missing)
	}
	if !validClassifications[in.Classification] {
		return fmt.Errorf("%w: classification %q must be one of code-level, config-level, mixed, none", ErrInvalidReport, in.Classification)
	}
	return nil
}

// GetReport returns a single report, or ErrReportNotFound when absent. The
// report's Dispatched/Deployed fields are reconciled against live Task
// executions so the console's Coding Handover / Verify Fix stepper reflects
// the current state rather than the write-time snapshot.
func (s *rcaAgentReportService) GetReport(ctx context.Context, orgID, id string) (*models.RcaAgentReport, error) {
	report, err := s.repo.Get(ctx, orgID, id)
	if err != nil {
		return nil, fmt.Errorf("get report %q: %w", id, err)
	}
	if report == nil {
		return nil, ErrReportNotFound
	}
	s.correlateExecutionState(ctx, orgID, report)
	return report, nil
}

// correlateExecutionState upgrades a report's Dispatched/Deployed flags from
// the live executions of the linked Task (repo + issue number). It only ever
// promotes false→true (never the reverse), so a writer that already knew the
// agent was dispatched is never contradicted by a missing execution row. A
// no-op when correlation is disabled (nil reader), the report has no linked
// issue, or the issue URL can't be resolved to a repo.
func (s *rcaAgentReportService) correlateExecutionState(ctx context.Context, orgID string, report *models.RcaAgentReport) {
	if s.execs == nil || report == nil || report.IssueNumber == nil {
		return
	}
	repo := repoFromIssueURL(report.IssueURL)
	if repo == "" {
		return
	}
	execs, err := s.execs.LatestPerKindScoped(ctx, orgID, repo, int(*report.IssueNumber))
	if err != nil {
		// Correlation is best-effort: serve the stored snapshot on lookup error.
		slog.WarnContext(ctx, "rcaagent: execution correlation failed",
			"issue", *report.IssueNumber, "repo", repo, "error", err)
		return
	}
	if _, ok := execs[string(taskmeta.KindCoding)]; ok {
		report.Dispatched = true
	}
	// Deployed = a build execution for this Task has succeeded (the fix built
	// and rolled), matching taskmeta.Derive's StatusDeployed threshold — i.e.
	// beyond merely PR-merged, per issue #156's "Verify Fix" requirement.
	if b := execs[string(taskmeta.KindBuild)]; b != nil && b.Status == string(taskmeta.ExecSucceeded) {
		report.Deployed = true
		if report.DeployedAt == nil {
			report.DeployedAt = b.EndedAt
		}
	}
}

// repoFromIssueURL extracts the "<owner>/<name>" GitHub slug (the key the
// executions table stores) from a full issue URL like
// https://github.com/<owner>/<name>/issues/<n>. Returns "" if it can't.
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

// ListReports returns a page of reports, newest first.
func (s *rcaAgentReportService) ListReports(ctx context.Context, orgID, cursor string, limit int) (*gen.RcaAgentReportList, error) {
	switch {
	case limit <= 0:
		limit = defaultListLimit
	case limit > maxListLimit:
		limit = maxListLimit
	}
	reports, nextCursor, err := s.repo.List(ctx, orgID, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	return &gen.RcaAgentReportList{Items: reports, NextCursor: nextCursor}, nil
}
