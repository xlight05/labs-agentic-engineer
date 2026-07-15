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
	"github.com/wso2/aep/aep-api/internal/platform/auth"

	"github.com/wso2/aep/aep-api/internal/feature/artifacts"
	"github.com/wso2/aep/aep-api/internal/feature/build"
	"github.com/wso2/aep/aep-api/internal/feature/component"
	"github.com/wso2/aep/aep-api/internal/feature/dependencies"
	"github.com/wso2/aep/aep-api/internal/feature/execution"
	"github.com/wso2/aep/aep-api/internal/feature/files"
	"github.com/wso2/aep/aep-api/internal/feature/genai"
	"github.com/wso2/aep/aep-api/internal/feature/gitrepo"
	"github.com/wso2/aep/aep-api/internal/feature/organization"
	"github.com/wso2/aep/aep-api/internal/feature/orgconfig"
	"github.com/wso2/aep/aep-api/internal/feature/project"
	"github.com/wso2/aep/aep-api/internal/feature/provisioning"
	"github.com/wso2/aep/aep-api/internal/feature/rcaagent"
	"github.com/wso2/aep/aep-api/internal/feature/skills"
	"github.com/wso2/aep/aep-api/internal/feature/task"
)

// Deps carries every feature service the strict handlers (handlers_*.go)
// call — nothing more: a field here means at least one handler reads it
// (fields whose only consumers were the retired Huma registrations were
// dropped at the extraction). main.go (internal/app) fills it with the real
// services; component tests fill only what the feature under test needs
// (untouched fields nil-guard or 503 in their handlers).
type Deps struct {
	ProjectSvc          project.ProjectService
	OrgSvc              organization.OrganizationService
	ComponentSvc        component.ComponentService
	ConfigSvc           component.ConfigService
	CollabRepo          gitrepo.RepoService
	IssueSvc            gitrepo.IssueService
	ProvisioningSvc     *provisioning.Service
	ResourceTypeCatalog dependencies.ResourceTypeLister
	TaskReads           *task.Reads
	TaskCommands        *task.Commands
	TaskStream          *execution.TaskStreamService
	OrgConfigSvc        *orgconfig.Service
	TaskTokens          *auth.TaskTokenManager
	SkillSvc            *skills.SkillService
	SkillMutationSvc    *skills.SkillMutationService
	SkillImportSvc      *skills.SkillImportService
	FilesSvc            files.FilesService
	ArtifactSvc         artifacts.ArtifactService
	GenAISvc            genai.GenAIService
	BuildSvc            *build.Service
	RcaAgentReportSvc   rcaagent.RcaAgentReportService
	PreflightSvc        *build.PreflightService
}
