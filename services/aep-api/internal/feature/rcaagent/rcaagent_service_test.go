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
	"testing"

	"github.com/wso2/aep/aep-api/internal/gen"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/models"
)

// fakeExecReader is an in-memory ExecutionReader fake for correlation tests.
type fakeExecReader struct {
	perKind  map[string]*models.Execution
	gotRepo  string
	gotIssue int
}

func (f *fakeExecReader) LatestPerKindScoped(_ context.Context, _, repo string, issueNumber int) (map[string]*models.Execution, error) {
	f.gotRepo = repo
	f.gotIssue = issueNumber
	return f.perKind, nil
}

// fakeRepo is an in-memory Repository fake — enough to test the service's
// validation and default/cap logic without a database.
type fakeRepo struct {
	created []models.RcaAgentReport
	byID    map[string]*models.RcaAgentReport

	// listCalls records the (cursor, limit) the service actually passed
	// through, so tests can assert default/cap behavior.
	listLimit int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{byID: map[string]*models.RcaAgentReport{}}
}

func (f *fakeRepo) Create(_ context.Context, orgID string, in *gen.CreateRcaAgentReportRequest) (*models.RcaAgentReport, error) {
	report := &models.RcaAgentReport{
		ID:             "generated-id",
		OrgID:          orgID,
		Project:        in.Project,
		Title:          in.Title,
		Summary:        in.Summary,
		Classification: in.Classification,
		Diagnosis:      in.Diagnosis,
	}
	f.created = append(f.created, *report)
	f.byID[report.ID] = report
	return report, nil
}

func (f *fakeRepo) Get(_ context.Context, _, id string) (*models.RcaAgentReport, error) {
	return f.byID[id], nil
}

func (f *fakeRepo) List(_ context.Context, _ string, _ string, limit int) ([]models.RcaAgentReport, string, error) {
	f.listLimit = limit
	return nil, "", nil
}

func TestGetReport_correlatesExecutionState(t *testing.T) {
	repo := newFakeRepo()
	n := int64(8)
	repo.byID["r1"] = &models.RcaAgentReport{
		ID:          "r1",
		Project:     "hellonewservice",
		IssueNumber: &n,
		IssueURL:    "https://github.com/wso2-tharindu-SF/hellonewservice/issues/8",
		// Snapshot written stale (before dispatch/build).
		Dispatched: false,
		Deployed:   false,
	}
	exec := &fakeExecReader{perKind: map[string]*models.Execution{
		string(taskmeta.KindCoding): {Kind: string(taskmeta.KindCoding), Status: string(taskmeta.ExecRunning)},
		string(taskmeta.KindBuild):  {Kind: string(taskmeta.KindBuild), Status: string(taskmeta.ExecSucceeded)},
	}}
	svc := NewRcaAgentReportService(repo, exec)

	got, err := svc.GetReport(context.Background(), "acme", "r1")
	if err != nil {
		t.Fatalf("GetReport: %v", err)
	}
	if !got.Dispatched {
		t.Error("expected Dispatched=true from the coding execution")
	}
	if !got.Deployed {
		t.Error("expected Deployed=true from the succeeded build execution")
	}
	if exec.gotRepo != "wso2-tharindu-SF/hellonewservice" {
		t.Errorf("repo parsed from issue URL = %q, want wso2-tharindu-SF/hellonewservice", exec.gotRepo)
	}
	if exec.gotIssue != 8 {
		t.Errorf("issue number = %d, want 8", exec.gotIssue)
	}
}

func TestGetReport_correlationIsMonotonic(t *testing.T) {
	// A stored Dispatched=true must not be downgraded when no execution row
	// is found (e.g. dispatched via a path that left no execution).
	repo := newFakeRepo()
	n := int64(8)
	repo.byID["r1"] = &models.RcaAgentReport{
		ID:          "r1",
		IssueNumber: &n,
		IssueURL:    "https://github.com/o/r/issues/8",
		Dispatched:  true,
	}
	exec := &fakeExecReader{perKind: map[string]*models.Execution{}} // no executions
	svc := NewRcaAgentReportService(repo, exec)

	got, _ := svc.GetReport(context.Background(), "acme", "r1")
	if !got.Dispatched {
		t.Error("stored Dispatched=true must be preserved when no execution is found")
	}
}

func TestGetReport_noReader_servesStoredSnapshot(t *testing.T) {
	repo := newFakeRepo()
	n := int64(8)
	repo.byID["r1"] = &models.RcaAgentReport{ID: "r1", IssueNumber: &n, Dispatched: true}
	svc := NewRcaAgentReportService(repo, nil)

	got, _ := svc.GetReport(context.Background(), "acme", "r1")
	if !got.Dispatched {
		t.Error("stored snapshot should be served unchanged when correlation is disabled")
	}
}

func validCreateRequest() *gen.CreateRcaAgentReportRequest {
	return &gen.CreateRcaAgentReportRequest{
		Project:        "demo-shop",
		Title:          "Checkout requests failing",
		Summary:        "summary",
		Classification: "code-level",
		Diagnosis:      "diagnosis",
	}
}

func TestCreateReport_Valid(t *testing.T) {
	repo := newFakeRepo()
	svc := NewRcaAgentReportService(repo, nil)

	report, err := svc.CreateReport(context.Background(), "acme", validCreateRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.OrgID != "acme" {
		t.Errorf("orgID = %q, want acme", report.OrgID)
	}
	if len(repo.created) != 1 {
		t.Errorf("expected 1 created report, got %d", len(repo.created))
	}
}

func TestCreateReport_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*gen.CreateRcaAgentReportRequest)
	}{
		{"missing project", func(r *gen.CreateRcaAgentReportRequest) { r.Project = "" }},
		{"missing title", func(r *gen.CreateRcaAgentReportRequest) { r.Title = "" }},
		{"missing summary", func(r *gen.CreateRcaAgentReportRequest) { r.Summary = "" }},
		{"missing diagnosis", func(r *gen.CreateRcaAgentReportRequest) { r.Diagnosis = "" }},
		{"missing classification", func(r *gen.CreateRcaAgentReportRequest) { r.Classification = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo()
			svc := NewRcaAgentReportService(repo, nil)
			in := validCreateRequest()
			tt.mutate(in)

			_, err := svc.CreateReport(context.Background(), "acme", in)
			if !errors.Is(err, ErrInvalidReport) {
				t.Fatalf("error = %v, want ErrInvalidReport", err)
			}
			if len(repo.created) != 0 {
				t.Errorf("expected no report to be created, got %d", len(repo.created))
			}
		})
	}
}

func TestCreateReport_InvalidClassification(t *testing.T) {
	repo := newFakeRepo()
	svc := NewRcaAgentReportService(repo, nil)
	in := validCreateRequest()
	in.Classification = "urgent"

	_, err := svc.CreateReport(context.Background(), "acme", in)
	if !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("error = %v, want ErrInvalidReport", err)
	}
}

func TestCreateReport_NilRequest(t *testing.T) {
	repo := newFakeRepo()
	svc := NewRcaAgentReportService(repo, nil)

	_, err := svc.CreateReport(context.Background(), "acme", nil)
	if !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("error = %v, want ErrInvalidReport", err)
	}
}

func TestGetReport_Found(t *testing.T) {
	repo := newFakeRepo()
	svc := NewRcaAgentReportService(repo, nil)
	created, _ := svc.CreateReport(context.Background(), "acme", validCreateRequest())

	got, err := svc.GetReport(context.Background(), "acme", created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("got ID %q, want %q", got.ID, created.ID)
	}
}

func TestGetReport_NotFound(t *testing.T) {
	repo := newFakeRepo()
	svc := NewRcaAgentReportService(repo, nil)

	_, err := svc.GetReport(context.Background(), "acme", "does-not-exist")
	if !errors.Is(err, ErrReportNotFound) {
		t.Fatalf("error = %v, want ErrReportNotFound", err)
	}
}

func TestListReports_DefaultsLimitWhenAbsentOrZero(t *testing.T) {
	repo := newFakeRepo()
	svc := NewRcaAgentReportService(repo, nil)

	if _, err := svc.ListReports(context.Background(), "acme", "", 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.listLimit != defaultListLimit {
		t.Errorf("limit passed to repo = %d, want default %d", repo.listLimit, defaultListLimit)
	}
}

func TestListReports_CapsExcessiveLimit(t *testing.T) {
	repo := newFakeRepo()
	svc := NewRcaAgentReportService(repo, nil)

	if _, err := svc.ListReports(context.Background(), "acme", "", 10_000); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.listLimit != maxListLimit {
		t.Errorf("limit passed to repo = %d, want cap %d", repo.listLimit, maxListLimit)
	}
}

func TestListReports_PassesThroughReasonableLimit(t *testing.T) {
	repo := newFakeRepo()
	svc := NewRcaAgentReportService(repo, nil)

	if _, err := svc.ListReports(context.Background(), "acme", "", 20); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.listLimit != 20 {
		t.Errorf("limit passed to repo = %d, want 20", repo.listLimit)
	}
}
