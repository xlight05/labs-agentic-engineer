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

package build

import (
	"context"
	"fmt"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// TemporalRunner is the production WorkflowRunner over the devflow Temporal
// runtime. ExecuteWorkflow is start-and-return, so a build POST answers as
// soon as the workflow is accepted — never when it completes.
type TemporalRunner struct {
	rt *delivery.Runtime
}

// NewTemporalRunner wraps the devflow runtime.
func NewTemporalRunner(rt *delivery.Runtime) *TemporalRunner {
	return &TemporalRunner{rt: rt}
}

func (r *TemporalRunner) Ready() error {
	if _, err := r.rt.Client(); err != nil {
		return ErrTemporalUnavailable
	}
	return nil
}

func (r *TemporalRunner) StartBuild(ctx context.Context, workflowID string, in delivery.DevFlowInput) (string, error) {
	c, err := r.rt.Client()
	if err != nil {
		return "", ErrTemporalUnavailable
	}
	// ALLOW_DUPLICATE: re-building an unchanged spec re-runs the same
	// deterministic id after the previous run closed; a concurrently-running
	// duplicate is excluded by the API's one-dev-run-per-project 409 guard.
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                    workflowID,
		TaskQueue:             r.rt.TaskQueue(),
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	}, delivery.DevFlowWorkflowName, in)
	if err != nil {
		return "", fmt.Errorf("start workflow: %w", err)
	}
	return run.GetRunID(), nil
}

func (r *TemporalRunner) BuildStatus(ctx context.Context, workflowID string) (delivery.DevFlowStatus, error) {
	var st delivery.DevFlowStatus
	c, err := r.rt.Client()
	if err != nil {
		return st, ErrTemporalUnavailable
	}
	resp, err := c.QueryWorkflow(ctx, workflowID, "", delivery.QueryStatus)
	if err != nil {
		return st, fmt.Errorf("query workflow: %w", err)
	}
	if err := resp.Get(&st); err != nil {
		return st, fmt.Errorf("decode status: %w", err)
	}
	return st, nil
}
