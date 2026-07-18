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

package devflow

import (
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// Registered workflow type names. Kept as constants because clients start
// workflows by name and tests assert on them.
const (
	DevFlowWorkflowName        = "DevFlowWorkflow"
	TaskFlowWorkflowName       = "TaskFlowWorkflow"
	ValidationFlowWorkflowName = "ValidationFlowWorkflow"
	ValidationTaskWorkflowName = "ValidationTaskWorkflow"
)

// registerAll registers every devflow workflow and activity on the worker.
// The single registration point keeps the worker, the test environments and
// the docs in agreement about what runs on the aep-devflow task queue.
func registerAll(wk worker.Worker, acts *Activities) {
	wk.RegisterWorkflowWithOptions(DevFlowWorkflow, workflow.RegisterOptions{Name: DevFlowWorkflowName})
	wk.RegisterWorkflowWithOptions(TaskFlowWorkflow, workflow.RegisterOptions{Name: TaskFlowWorkflowName})
	wk.RegisterWorkflowWithOptions(ValidationFlowWorkflow, workflow.RegisterOptions{Name: ValidationFlowWorkflowName})
	wk.RegisterWorkflowWithOptions(ValidationTaskWorkflow, workflow.RegisterOptions{Name: ValidationTaskWorkflowName})
	wk.RegisterActivity(acts)
}
