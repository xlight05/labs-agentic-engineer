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

package validation_test

import (
	"testing"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/delivery/validation"
)

// TestVerdictFromReport pins the rule that turns the runner's committed report
// into a run property. The cases that matter are the ones where a report is
// NOT a failure: a criterion the runner could not run, a manual checklist item,
// and a report that never arrived are all statements about the test harness,
// not about the deployed system.
func TestVerdictFromReport(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			"every criterion passed",
			`{"criteria":[{"id":"AC-1","method":"e2e","status":"pass"},{"id":"AC-2","method":"e2e","status":"pass"}]}`,
			delivery.ValidationVerdictPassed,
		},
		{
			"one real failure is the verdict",
			`{"criteria":[{"id":"AC-1","status":"pass"},{"id":"AC-2","status":"fail"}]}`,
			delivery.ValidationVerdictFailed,
		},
		{
			"un-runnable criteria alongside a pass do not fail the run",
			`{"criteria":[{"id":"AC-1","status":"pass"},{"id":"AC-2","status":"not_run"},
			  {"id":"AC-3","status":"not_validated"},{"id":"AC-4","status":"manual"}]}`,
			delivery.ValidationVerdictPassed,
		},
		{
			"nothing was actually run",
			`{"criteria":[{"id":"AC-1","status":"not_run"},{"id":"AC-2","status":"manual"}]}`,
			delivery.ValidationVerdictSkipped,
		},
		{"an empty report", `{"criteria":[]}`, delivery.ValidationVerdictSkipped},
		{"no report at all", "", delivery.ValidationVerdictSkipped},
		{"an unparseable report is a reporting gap, not a broken deployment", "{not json", delivery.ValidationVerdictSkipped},
	}
	for _, c := range cases {
		if got := validation.VerdictFromReport([]byte(c.raw)); got != c.want {
			t.Errorf("VerdictFromReport(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}
