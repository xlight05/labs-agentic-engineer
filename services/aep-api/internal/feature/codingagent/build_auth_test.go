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

package codingagent

import (
	"testing"

	"github.com/wso2/aep/aep-api/internal/gen"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
)

func TestClassifyBuildRun(t *testing.T) {
	cases := []struct {
		name          string
		run           *gen.WorkflowRun
		wantSucceeded bool
		wantAuth      bool
	}{
		{"nil", nil, false, false},
		{"not completed", &gen.WorkflowRun{Status: openchoreo.ReasonWorkflowSucceeded}, false, false},
		{"succeeded", &gen.WorkflowRun{Completed: true, Status: openchoreo.ReasonWorkflowSucceeded}, true, false},
		{
			"git-auth failure on checkout-source",
			&gen.WorkflowRun{Completed: true, Status: openchoreo.ReasonWorkflowFailed,
				Tasks: []gen.WorkflowRunTask{{Name: "checkout-source", Phase: "Failed", Message: "fatal: could not read Username for 'https://github.com'"}}},
			false, true,
		},
		{
			"non-auth checkout failure",
			&gen.WorkflowRun{Completed: true, Status: openchoreo.ReasonWorkflowFailed,
				Tasks: []gen.WorkflowRunTask{{Name: "checkout-source", Phase: "Failed", Message: "disk full"}}},
			false, false,
		},
		{
			"failure in a non-checkout step is not auth",
			&gen.WorkflowRun{Completed: true, Status: openchoreo.ReasonWorkflowFailed,
				Tasks: []gen.WorkflowRunTask{{Name: "build", Phase: "Failed", Message: "the requested URL returned error: 403"}}},
			false, false,
		},
		{
			"empty checkout message is conservatively non-auth",
			&gen.WorkflowRun{Completed: true, Status: openchoreo.ReasonWorkflowFailed,
				Tasks: []gen.WorkflowRunTask{{Name: "checkout-source", Phase: "Failed", Message: ""}}},
			false, false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotSucc, gotAuth := classifyBuildRun(tc.run)
			if gotSucc != tc.wantSucceeded || gotAuth != tc.wantAuth {
				t.Errorf("classifyBuildRun = (succeeded=%v, auth=%v), want (%v, %v)", gotSucc, gotAuth, tc.wantSucceeded, tc.wantAuth)
			}
		})
	}
}

func TestParseBuildAuthRetryAttempt(t *testing.T) {
	cases := map[string]int{
		"":                    0,
		"on hold":             0,
		"build_auth_retry:0":  0,
		"build_auth_retry:2":  2,
		"build_auth_retry:10": 10,
		"build_auth_retry:x":  0,
	}
	for reason, want := range cases {
		if got := parseBuildAuthRetryAttempt(reason); got != want {
			t.Errorf("parseBuildAuthRetryAttempt(%q) = %d, want %d", reason, got, want)
		}
	}
	if got := buildAuthRetryReason(3); got != "build_auth_retry:3" {
		t.Errorf("buildAuthRetryReason(3) = %q", got)
	}
}
