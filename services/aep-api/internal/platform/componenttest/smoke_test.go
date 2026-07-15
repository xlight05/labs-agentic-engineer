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

package componenttest

import (
	"context"
	"strings"
	"testing"

	"github.com/wso2/aep/aep-api/internal/api"
	"github.com/wso2/aep/aep-api/internal/feature/project"
	"github.com/wso2/aep/aep-api/models"
)

// fakeProjectService is a minimal project.ProjectService stand-in for the
// harness smoke test. (The real per-feature component tests will run the REAL
// project service with a mocked OC client; this only proves the harness wires
// the real chain + ENFORCE gate.)
type fakeProjectService struct{}

func (fakeProjectService) ListProjects(_ context.Context, _ string, _ int, _, _ string) (*models.ProjectList, error) {
	return &models.ProjectList{Items: []models.Project{{Name: "demo"}}}, nil
}
func (fakeProjectService) GetProject(_ context.Context, _, name string) (*models.Project, error) {
	return &models.Project{Name: name}, nil
}
func (fakeProjectService) CreateProject(_ context.Context, _ string, req *models.CreateProjectRequest) (*models.Project, error) {
	return &models.Project{Name: req.Name}, nil
}
func (fakeProjectService) DeleteProject(_ context.Context, _, _ string) error { return nil }
func (fakeProjectService) GetProjectStatus(_ context.Context, _, _ string) (*models.ProjectStatus, error) {
	return &models.ProjectStatus{Phase: "prompt"}, nil
}

var _ project.ProjectService = fakeProjectService{}

// TestHarness_Smoke proves the componenttest harness assembles the REAL /api
// handler with the tenant gate in ENFORCE and faked auth at the claims seam:
//   - an authed request reaches the real handler (200 with the service's data);
//   - a no-claims request is denied 401 by the gate's no-claims branch — the
//     ENFORCE proof that the global SetGateMode(GateModeLog) flip used to hide.
//
// This is the Phase-0c exit gate ("a throwaway _component_test compiles and
// passes"). The real project_component_test.go (Pilot A) supersedes it.
func TestHarness_Smoke(t *testing.T) {
	t.Parallel()
	h := New(t, Options{Deps: api.Deps{ProjectSvc: fakeProjectService{}}})

	// Authed → real handler runs, real service data returned.
	if resp := h.AsOrg("acme").Get("/api/v1/projects"); resp.Code != 200 {
		t.Fatalf("AsOrg list-projects: got %d, body=%s", resp.Code, resp.Body.String())
	} else if !strings.Contains(resp.Body.String(), "demo") {
		t.Fatalf("AsOrg list-projects body missing item: %s", resp.Body.String())
	}

	// No claims → gate denies in ENFORCE (401). This is the whole point of the tier.
	if resp := h.NoAuth().Get("/api/v1/projects"); resp.Code != 401 {
		t.Fatalf("NoAuth list-projects: expected 401 (gate ENFORCE), got %d, body=%s", resp.Code, resp.Body.String())
	}
}
