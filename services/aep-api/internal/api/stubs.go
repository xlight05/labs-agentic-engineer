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

	"github.com/wso2/aep/aep-api/internal/api/gen"
)

// stubServer answers 501 not_implemented for every contract operation. The
// real apiServer embeds it and shadows one method per migrated operation, so
// the strict interface stays fully satisfied throughout the feature-by-feature
// migration (issue 003) and an unmigrated route is a loud, typed 501 instead
// of a silent 404. Delete each stub as its feature moves onto the strict
// interface; this file is empty at the end of the migration.
type stubServer struct{}

func (stubServer) ValidateCollabAccess(_ context.Context, _ gen.ValidateCollabAccessRequestObject) (gen.ValidateCollabAccessResponseObject, error) {
	return nil, errNotImplemented("ValidateCollabAccess")
}

func (stubServer) GetConfig(_ context.Context, _ gen.GetConfigRequestObject) (gen.GetConfigResponseObject, error) {
	return nil, errNotImplemented("GetConfig")
}

func (stubServer) UpdateConfig(_ context.Context, _ gen.UpdateConfigRequestObject) (gen.UpdateConfigResponseObject, error) {
	return nil, errNotImplemented("UpdateConfig")
}

func (stubServer) StartGitProviderConnect(_ context.Context, _ gen.StartGitProviderConnectRequestObject) (gen.StartGitProviderConnectResponseObject, error) {
	return nil, errNotImplemented("StartGitProviderConnect")
}

func (stubServer) DisconnectGitProvider(_ context.Context, _ gen.DisconnectGitProviderRequestObject) (gen.DisconnectGitProviderResponseObject, error) {
	return nil, errNotImplemented("DisconnectGitProvider")
}

func (stubServer) RotateIdpClientSecret(_ context.Context, _ gen.RotateIdpClientSecretRequestObject) (gen.RotateIdpClientSecretResponseObject, error) {
	return nil, errNotImplemented("RotateIdpClientSecret")
}

func (stubServer) DiscoverIdp(_ context.Context, _ gen.DiscoverIdpRequestObject) (gen.DiscoverIdpResponseObject, error) {
	return nil, errNotImplemented("DiscoverIdp")
}

func (stubServer) ListExternalResources(_ context.Context, _ gen.ListExternalResourcesRequestObject) (gen.ListExternalResourcesResponseObject, error) {
	return nil, errNotImplemented("ListExternalResources")
}

func (stubServer) DeleteExternalResource(_ context.Context, _ gen.DeleteExternalResourceRequestObject) (gen.DeleteExternalResourceResponseObject, error) {
	return nil, errNotImplemented("DeleteExternalResource")
}

func (stubServer) ListPlatformResourceTypes(_ context.Context, _ gen.ListPlatformResourceTypesRequestObject) (gen.ListPlatformResourceTypesResponseObject, error) {
	return nil, errNotImplemented("ListPlatformResourceTypes")
}

func (stubServer) ListOrganizations(_ context.Context, _ gen.ListOrganizationsRequestObject) (gen.ListOrganizationsResponseObject, error) {
	return nil, errNotImplemented("ListOrganizations")
}

func (stubServer) ListProjects(_ context.Context, _ gen.ListProjectsRequestObject) (gen.ListProjectsResponseObject, error) {
	return nil, errNotImplemented("ListProjects")
}

func (stubServer) CreateProject(_ context.Context, _ gen.CreateProjectRequestObject) (gen.CreateProjectResponseObject, error) {
	return nil, errNotImplemented("CreateProject")
}

func (stubServer) DeleteProject(_ context.Context, _ gen.DeleteProjectRequestObject) (gen.DeleteProjectResponseObject, error) {
	return nil, errNotImplemented("DeleteProject")
}

func (stubServer) GetProject(_ context.Context, _ gen.GetProjectRequestObject) (gen.GetProjectResponseObject, error) {
	return nil, errNotImplemented("GetProject")
}

func (stubServer) GetConversation(_ context.Context, _ gen.GetConversationRequestObject) (gen.GetConversationResponseObject, error) {
	return nil, errNotImplemented("GetConversation")
}

func (stubServer) CreateTurn(_ context.Context, _ gen.CreateTurnRequestObject) (gen.CreateTurnResponseObject, error) {
	return nil, errNotImplemented("CreateTurn")
}

func (stubServer) BuildProject(_ context.Context, _ gen.BuildProjectRequestObject) (gen.BuildProjectResponseObject, error) {
	return nil, errNotImplemented("BuildProject")
}

func (stubServer) GetBuildPreflight(_ context.Context, _ gen.GetBuildPreflightRequestObject) (gen.GetBuildPreflightResponseObject, error) {
	return nil, errNotImplemented("GetBuildPreflight")
}

func (stubServer) GetProjectBuild(_ context.Context, _ gen.GetProjectBuildRequestObject) (gen.GetProjectBuildResponseObject, error) {
	return nil, errNotImplemented("GetProjectBuild")
}

func (stubServer) ListProjectBuilds(_ context.Context, _ gen.ListProjectBuildsRequestObject) (gen.ListProjectBuildsResponseObject, error) {
	return nil, errNotImplemented("ListProjectBuilds")
}

func (stubServer) ListComponents(_ context.Context, _ gen.ListComponentsRequestObject) (gen.ListComponentsResponseObject, error) {
	return nil, errNotImplemented("ListComponents")
}

func (stubServer) GetComponent(_ context.Context, _ gen.GetComponentRequestObject) (gen.GetComponentResponseObject, error) {
	return nil, errNotImplemented("GetComponent")
}

func (stubServer) ListBuilds(_ context.Context, _ gen.ListBuildsRequestObject) (gen.ListBuildsResponseObject, error) {
	return nil, errNotImplemented("ListBuilds")
}

func (stubServer) TriggerBuild(_ context.Context, _ gen.TriggerBuildRequestObject) (gen.TriggerBuildResponseObject, error) {
	return nil, errNotImplemented("TriggerBuild")
}

func (stubServer) GetBuildLogs(_ context.Context, _ gen.GetBuildLogsRequestObject) (gen.GetBuildLogsResponseObject, error) {
	return nil, errNotImplemented("GetBuildLogs")
}

func (stubServer) GetComponentConfig(_ context.Context, _ gen.GetComponentConfigRequestObject) (gen.GetComponentConfigResponseObject, error) {
	return nil, errNotImplemented("GetComponentConfig")
}

func (stubServer) UpdateComponentConfig(_ context.Context, _ gen.UpdateComponentConfigRequestObject) (gen.UpdateComponentConfigResponseObject, error) {
	return nil, errNotImplemented("UpdateComponentConfig")
}

func (stubServer) RequestOrgServiceAccess(_ context.Context, _ gen.RequestOrgServiceAccessRequestObject) (gen.RequestOrgServiceAccessResponseObject, error) {
	return nil, errNotImplemented("RequestOrgServiceAccess")
}

func (stubServer) ProvisionPlatformResource(_ context.Context, _ gen.ProvisionPlatformResourceRequestObject) (gen.ProvisionPlatformResourceResponseObject, error) {
	return nil, errNotImplemented("ProvisionPlatformResource")
}

func (stubServer) GetDependencyStatus(_ context.Context, _ gen.GetDependencyStatusRequestObject) (gen.GetDependencyStatusResponseObject, error) {
	return nil, errNotImplemented("GetDependencyStatus")
}

func (stubServer) ListDeployments(_ context.Context, _ gen.ListDeploymentsRequestObject) (gen.ListDeploymentsResponseObject, error) {
	return nil, errNotImplemented("ListDeployments")
}

func (stubServer) GetComponentOpenapi(_ context.Context, _ gen.GetComponentOpenapiRequestObject) (gen.GetComponentOpenapiResponseObject, error) {
	return nil, errNotImplemented("GetComponentOpenapi")
}

func (stubServer) ListAccessRequests(_ context.Context, _ gen.ListAccessRequestsRequestObject) (gen.ListAccessRequestsResponseObject, error) {
	return nil, errNotImplemented("ListAccessRequests")
}

func (stubServer) CollectExternalResourceValues(_ context.Context, _ gen.CollectExternalResourceValuesRequestObject) (gen.CollectExternalResourceValuesResponseObject, error) {
	return nil, errNotImplemented("CollectExternalResourceValues")
}

func (stubServer) ListFiles(_ context.Context, _ gen.ListFilesRequestObject) (gen.ListFilesResponseObject, error) {
	return nil, errNotImplemented("ListFiles")
}

func (stubServer) ApplyFiles(_ context.Context, _ gen.ApplyFilesRequestObject) (gen.ApplyFilesResponseObject, error) {
	return nil, errNotImplemented("ApplyFiles")
}

func (stubServer) ReadFile(_ context.Context, _ gen.ReadFileRequestObject) (gen.ReadFileResponseObject, error) {
	return nil, errNotImplemented("ReadFile")
}

func (stubServer) GetSpecCollabSession(_ context.Context, _ gen.GetSpecCollabSessionRequestObject) (gen.GetSpecCollabSessionResponseObject, error) {
	return nil, errNotImplemented("GetSpecCollabSession")
}

func (stubServer) GetProjectStatus(_ context.Context, _ gen.GetProjectStatusRequestObject) (gen.GetProjectStatusResponseObject, error) {
	return nil, errNotImplemented("GetProjectStatus")
}

func (stubServer) ListProjectTags(_ context.Context, _ gen.ListProjectTagsRequestObject) (gen.ListProjectTagsResponseObject, error) {
	return nil, errNotImplemented("ListProjectTags")
}

func (stubServer) ListTasks(_ context.Context, _ gen.ListTasksRequestObject) (gen.ListTasksResponseObject, error) {
	return nil, errNotImplemented("ListTasks")
}

func (stubServer) GetTask(_ context.Context, _ gen.GetTaskRequestObject) (gen.GetTaskResponseObject, error) {
	return nil, errNotImplemented("GetTask")
}

func (stubServer) StreamTaskLog(_ context.Context, _ gen.StreamTaskLogRequestObject) (gen.StreamTaskLogResponseObject, error) {
	return nil, errNotImplemented("StreamTaskLog")
}

func (stubServer) GetActiveTurn(_ context.Context, _ gen.GetActiveTurnRequestObject) (gen.GetActiveTurnResponseObject, error) {
	return nil, errNotImplemented("GetActiveTurn")
}

func (stubServer) GetTurn(_ context.Context, _ gen.GetTurnRequestObject) (gen.GetTurnResponseObject, error) {
	return nil, errNotImplemented("GetTurn")
}

func (stubServer) StreamTurn(_ context.Context, _ gen.StreamTurnRequestObject) (gen.StreamTurnResponseObject, error) {
	return nil, errNotImplemented("StreamTurn")
}

func (stubServer) ListRcaAgentReports(_ context.Context, _ gen.ListRcaAgentReportsRequestObject) (gen.ListRcaAgentReportsResponseObject, error) {
	return nil, errNotImplemented("ListRcaAgentReports")
}

func (stubServer) CreateRcaAgentReport(_ context.Context, _ gen.CreateRcaAgentReportRequestObject) (gen.CreateRcaAgentReportResponseObject, error) {
	return nil, errNotImplemented("CreateRcaAgentReport")
}

func (stubServer) GetRcaAgentReport(_ context.Context, _ gen.GetRcaAgentReportRequestObject) (gen.GetRcaAgentReportResponseObject, error) {
	return nil, errNotImplemented("GetRcaAgentReport")
}

func (stubServer) ListSkills(_ context.Context, _ gen.ListSkillsRequestObject) (gen.ListSkillsResponseObject, error) {
	return nil, errNotImplemented("ListSkills")
}

func (stubServer) CreateSkill(_ context.Context, _ gen.CreateSkillRequestObject) (gen.CreateSkillResponseObject, error) {
	return nil, errNotImplemented("CreateSkill")
}

func (stubServer) ImportSkill(_ context.Context, _ gen.ImportSkillRequestObject) (gen.ImportSkillResponseObject, error) {
	return nil, errNotImplemented("ImportSkill")
}

func (stubServer) SyncSkills(_ context.Context, _ gen.SyncSkillsRequestObject) (gen.SyncSkillsResponseObject, error) {
	return nil, errNotImplemented("SyncSkills")
}

func (stubServer) ListSkillUpdates(_ context.Context, _ gen.ListSkillUpdatesRequestObject) (gen.ListSkillUpdatesResponseObject, error) {
	return nil, errNotImplemented("ListSkillUpdates")
}

func (stubServer) DeleteSkill(_ context.Context, _ gen.DeleteSkillRequestObject) (gen.DeleteSkillResponseObject, error) {
	return nil, errNotImplemented("DeleteSkill")
}

func (stubServer) GetSkill(_ context.Context, _ gen.GetSkillRequestObject) (gen.GetSkillResponseObject, error) {
	return nil, errNotImplemented("GetSkill")
}

func (stubServer) UpdateSkill(_ context.Context, _ gen.UpdateSkillRequestObject) (gen.UpdateSkillResponseObject, error) {
	return nil, errNotImplemented("UpdateSkill")
}
