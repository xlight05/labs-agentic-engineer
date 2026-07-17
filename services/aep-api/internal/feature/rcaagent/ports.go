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

	"github.com/wso2/aep/aep-api/internal/gen"

	"github.com/wso2/aep/aep-api/models"
)

// Repository is the storage port RcaAgentService depends on — narrow enough
// to fake in unit tests without a database.
type Repository interface {
	Create(ctx context.Context, orgID string, in *gen.CreateRcaAgentReportRequest) (*models.RcaAgentReport, error)
	Get(ctx context.Context, orgID, id string) (*models.RcaAgentReport, error)
	List(ctx context.Context, orgID string, cursor string, limit int) ([]models.RcaAgentReport, string, error)
}

// ExecutionReader lets GetReport reconcile a report's Dispatched/Deployed
// snapshot against live Task executions (the same source the Build/Task view
// reads). A report is written once at RCA time, so its snapshot can go stale
// as a coding agent is later dispatched and the fix is built/deployed; this
// keeps the Coding Handover / Verify Fix stepper stages honest at read time.
// Optional — nil disables correlation (the stored snapshot is served as-is).
type ExecutionReader interface {
	LatestPerKindScoped(ctx context.Context, orgID, repo string, issueNumber int) (map[string]*models.Execution, error)
}
