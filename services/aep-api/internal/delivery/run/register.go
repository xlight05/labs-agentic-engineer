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

package run

import (
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// Register puts the run supervisor on a Temporal worker.
//
// It is a function rather than a worker of its own because a task queue must be
// served by ONE worker that knows every workflow on it: a second worker polling
// the same queue with a disjoint registration would fail whichever tasks it
// picked up by accident. The composition root therefore hands this to the
// existing worker watcher, and it becomes the supervisor's own worker when that
// watcher's other registrations retire.
func Register(wk worker.Worker, acts *Activities) {
	wk.RegisterWorkflowWithOptions(MilestoneRunWorkflow, workflow.RegisterOptions{Name: delivery.MilestoneRunWorkflowName})
	wk.RegisterActivity(acts)
}
