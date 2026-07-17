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
	"strings"

	"github.com/wso2/aep/aep-api/internal/feature/gitrepo"
	"github.com/wso2/aep/aep-api/internal/gen"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// Issue create/search on the strict interface. These back external handoffs —
// the OpenChoreo SRE/RCA agent files an issue here (and searches for related
// ones first) via aep-mcp-server, ahead of dispatching the coding agent with
// promote-task-from-issue. See AE-HANDOFF-DESIGN.md (openchoreo/agents/
// sre-agent). NOTE the wire quirk the contract documents: IssueInfo's keys
// are CAPITALIZED (historical shape the deployed MCP server parses).

func (s *legacyHandlers) CreateIssue(ctx context.Context, request gen.CreateIssueRequestObject) (gen.CreateIssueResponseObject, error) {
	if s.deps.IssueSvc == nil {
		return nil, errServiceUnavailable("issue service not configured")
	}
	org := tenant.BoundOrgFromContext(ctx)
	issue, err := s.deps.IssueSvc.CreateIssue(ctx, org, request.ProjectName, gitrepo.CreateIssueRequest{
		Title:     request.Body.Title,
		Body:      request.Body.Body,
		Labels:    request.Body.Labels,
		DedupeKey: request.Body.DedupeKey,
	})
	if err != nil {
		if errors.Is(err, gitrepo.ErrRepoNotFound) {
			return nil, errNotFound("project repo not found")
		}
		return nil, errInternal("failed to create issue")
	}
	return gen.CreateIssue200JSONResponse(gen.IssueResult{
		Number:  int64(issue.Number),
		URL:     issue.URL,
		NodeID:  issue.NodeID,
		Deduped: issue.Deduped,
	}), nil
}

func (s *legacyHandlers) ListIssues(ctx context.Context, request gen.ListIssuesRequestObject) (gen.ListIssuesResponseObject, error) {
	if s.deps.IssueSvc == nil {
		return nil, errServiceUnavailable("issue service not configured")
	}
	org := tenant.BoundOrgFromContext(ctx)
	var labels []string
	if request.Params.Labels != "" {
		for _, l := range strings.Split(request.Params.Labels, ",") {
			if l = strings.TrimSpace(l); l != "" {
				labels = append(labels, l)
			}
		}
	}
	issues, err := s.deps.IssueSvc.ListIssues(ctx, org, request.ProjectName, labels)
	if err != nil {
		if errors.Is(err, gitrepo.ErrRepoNotFound) {
			return nil, errNotFound("project repo not found")
		}
		return nil, errInternal("failed to list issues")
	}
	query := ""
	if request.Params.Q != "" {
		query = request.Params.Q
	}
	ranked := gitrepo.RankIssuesByQuery(issues, query)
	out := make([]gen.IssueInfo, 0, len(ranked))
	for _, iss := range ranked {
		out = append(out, gen.IssueInfo{
			Number: int64(iss.Number),
			Title:  iss.Title,
			Body:   iss.Body,
			URL:    iss.URL,
			State:  iss.State,
			Labels: iss.Labels,
		})
	}
	return gen.ListIssues200JSONResponse(out), nil
}
