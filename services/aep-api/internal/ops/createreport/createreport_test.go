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
	"errors"
	"strings"
	"testing"

	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/ops"
	"github.com/wso2/aep/aep-api/internal/platform/apierr"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// fakeRepo is an in-memory ops.Repository — enough to test this slice's
// validation without a database. The whole point of the repository port.
type fakeRepo struct{ created []ops.RcaAgentReport }

func (f *fakeRepo) Create(_ context.Context, r *ops.RcaAgentReport) error {
	r.ID = "generated-id"
	f.created = append(f.created, *r)
	return nil
}
func (f *fakeRepo) Get(context.Context, string, string) (*ops.RcaAgentReport, error) { return nil, nil }
func (f *fakeRepo) List(context.Context, string, string, int) ([]ops.RcaAgentReport, string, error) {
	return nil, "", nil
}

// ctxWithOrg is the only way org reaches a handler: the tenant gate binds it
// from the verified token. There is no org input to forge, which is what makes
// IDOR unrepresentable here.
func ctxWithOrg(org string) context.Context {
	return tenant.WithBoundOrg(context.Background(), org)
}

func validBody() *gen.CreateRcaAgentReportRequest {
	return &gen.CreateRcaAgentReportRequest{
		Project:        "proj",
		Title:          "Checkout 500s",
		Summary:        "Spike in 500s",
		Classification: "code-level",
		Diagnosis:      "npe in handler",
	}
}

func TestCreateReport_Valid(t *testing.T) {
	repo := &fakeRepo{}
	h := New(repo)

	resp, err := h.CreateRcaAgentReport(ctxWithOrg("acme"), gen.CreateRcaAgentReportRequestObject{Body: validBody()})
	if err != nil {
		t.Fatalf("CreateRcaAgentReport: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("persisted %d reports, want 1", len(repo.created))
	}
	// The org must come from the gate-bound context, never the body.
	if got := repo.created[0].OrgID; got != "acme" {
		t.Errorf("stored OrgID = %q, want the gate-bound %q", got, "acme")
	}
	out, ok := resp.(gen.CreateRcaAgentReport201JSONResponse)
	if !ok {
		t.Fatalf("response = %T, want 201", resp)
	}
	if out.ID != "generated-id" || out.Project != "proj" {
		t.Errorf("wire response = %+v, want the stored report projected", out)
	}
}

func TestCreateReport_MissingRequiredFields(t *testing.T) {
	cases := map[string]func(*gen.CreateRcaAgentReportRequest){
		"project":        func(b *gen.CreateRcaAgentReportRequest) { b.Project = "" },
		"title":          func(b *gen.CreateRcaAgentReportRequest) { b.Title = "" },
		"summary":        func(b *gen.CreateRcaAgentReportRequest) { b.Summary = "" },
		"diagnosis":      func(b *gen.CreateRcaAgentReportRequest) { b.Diagnosis = "" },
		"classification": func(b *gen.CreateRcaAgentReportRequest) { b.Classification = "" },
	}
	for field, blank := range cases {
		t.Run(field, func(t *testing.T) {
			repo := &fakeRepo{}
			body := validBody()
			blank(body)

			_, err := New(repo).CreateRcaAgentReport(ctxWithOrg("acme"),
				gen.CreateRcaAgentReportRequestObject{Body: body})

			assertStatus(t, err, 400)
			if !strings.Contains(err.Error(), field) {
				t.Errorf("error %q does not name the missing field %q", err, field)
			}
			if len(repo.created) != 0 {
				t.Error("an invalid report reached the repository — validation must precede persistence")
			}
		})
	}
}

func TestCreateReport_InvalidClassification(t *testing.T) {
	body := validBody()
	body.Classification = "vibes"

	_, err := New(&fakeRepo{}).CreateRcaAgentReport(ctxWithOrg("acme"),
		gen.CreateRcaAgentReportRequestObject{Body: body})

	assertStatus(t, err, 400)
	if !strings.Contains(err.Error(), "code-level") {
		t.Errorf("error %q does not name the allowed classifications", err)
	}
}

func TestCreateReport_NilRequest(t *testing.T) {
	_, err := New(&fakeRepo{}).CreateRcaAgentReport(ctxWithOrg("acme"),
		gen.CreateRcaAgentReportRequestObject{Body: nil})
	assertStatus(t, err, 400)
}

// assertStatus pins the TRANSPORT status a slice returns. Slices build their own
// apierr, so this is where a "400 became a 500" regression surfaces.
func assertStatus(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want a %d", want)
	}
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("error %v is not an *apierr.Error — the edge would write it as an opaque 500", err)
	}
	if ae.Status != want {
		t.Fatalf("status = %d, want %d", ae.Status, want)
	}
}
