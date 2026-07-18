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

package app

import (
	"context"

	"go.temporal.io/sdk/activity"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/feature/devflow"
	"github.com/wso2/aep/aep-api/internal/feature/task"
	"github.com/wso2/aep/aep-api/internal/sourcecontrol"
	"github.com/wso2/aep/aep-api/internal/spec"
	"github.com/wso2/aep/aep-api/repositories"
)

// repoFullNameLookup resolves a project's "owner/name" from the repo row —
// the devflow API's RepoLookup port.
type repoFullNameLookup struct {
	repos repositories.RepoRepository
}

func (l repoFullNameLookup) RepoFullName(ctx context.Context, orgID, projectID string) (string, error) {
	row, err := l.repos.GetByOrgAndProjectID(ctx, orgID, projectID)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", sourcecontrol.ErrRepoNotFound
	}
	owner, name, err := sourcecontrol.ParseOwnerRepo(row.RepoURL)
	if err != nil {
		return "", err
	}
	return owner + "/" + name, nil
}

// This file wires the devflow dev-workflow activity ports onto the existing
// artifacts / genai / plan services. The adapters live at the composition root
// so the devflow package imports none of those features (§6.8).

// devflowSpecValidator re-runs the whole-spec hard gate at the build tag via
// the artifacts service — the dev workflow's pre-plan defensive check.
type devflowSpecValidator struct {
	art spec.ArtifactService
}

func (v devflowSpecValidator) ValidateSpecAtTag(ctx context.Context, orgID, projectID, tag string) error {
	return v.art.ValidateSpecAtTag(ctx, orgID, projectID, tag)
}

// devflowPlanner runs the plan turn and reads back the planned tasks.
type devflowPlanner struct {
	plan  *task.PlanService
	reads *task.Reads
}

func (p devflowPlanner) RunPlan(ctx context.Context, orgID, projectID string) ([]devflow.PlannedTask, error) {
	session, err := p.plan.StartPlan(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	// Drain the plan turn to completion (the tap creates the issues as the
	// stream flows), heartbeating so Temporal does not time the activity out.
	session.Stream(heartbeatWriter{ctx: ctx}, func() {})

	// Read the tasks the plan just wrote back as the planned-task graph.
	views, err := p.reads.List(ctx, orgID, projectID, "open")
	if err != nil {
		return nil, err
	}
	return implementationTasks(views), nil
}

// implementationTasks maps the open Task views into the dev workflow's
// planned-task graph, EXCLUDING the validation task. Validation is not an
// implementation task: it runs in the validating phase (after every component
// deploys), so keeping it out of the graph stops it blocking/hanging the
// executing fan-out.
func implementationTasks(views []task.TaskView) []devflow.PlannedTask {
	out := make([]devflow.PlannedTask, 0, len(views))
	for _, v := range views {
		if v.ExecutorClass == string(taskmeta.ClassValidation) {
			continue
		}
		key := v.Component
		if key == "" {
			key = v.Operation
		}
		out = append(out, devflow.PlannedTask{
			Issue:     v.IssueNumber,
			Key:       key,
			DependsOn: v.DependsOn,
		})
	}
	return out
}

// heartbeatWriter records a Temporal activity heartbeat on each write so a
// long plan turn does not exceed its heartbeat timeout. It discards the bytes
// (the plan tap does the real work; the workflow only needs the issue set).
type heartbeatWriter struct {
	ctx context.Context
}

func (w heartbeatWriter) Write(p []byte) (int, error) {
	activity.RecordHeartbeat(w.ctx, "plan streaming")
	return len(p), nil
}
