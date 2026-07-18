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
	"testing"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/delivery"
)

// The validation task must never enter the dev workflow's executing fan-out —
// it runs in the validating phase. implementationTasks filters it out.
func TestImplementationTasks_ExcludesValidation(t *testing.T) {
	views := []delivery.TaskView{
		{IssueNumber: 1, ExecutorClass: string(taskmeta.ClassCoding), Component: "hello-web"},
		{IssueNumber: 2, ExecutorClass: string(taskmeta.ClassCoding), Component: "hello-api", DependsOn: []string{"hello-web"}},
		{IssueNumber: 9, ExecutorClass: string(taskmeta.ClassValidation), Operation: "validate", DependsOn: []string{"hello-web", "hello-api"}},
	}
	got := implementationTasks(views)
	if len(got) != 2 {
		t.Fatalf("want 2 implementation tasks (validation excluded), got %d: %+v", len(got), got)
	}
	for _, pt := range got {
		if pt.Issue == 9 {
			t.Fatalf("validation task (issue 9) leaked into the fan-out: %+v", got)
		}
	}
	if got[0].Key != "hello-web" || got[1].Key != "hello-api" {
		t.Errorf("keys wrong: %+v", got)
	}
}
