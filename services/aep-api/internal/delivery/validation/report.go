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

package validation

import (
	"encoding/json"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// ReportFilePath is the run report the validation runner commits, and the
// console renders. It is the counterpart of criteriaFilePath: the oracle says
// what must hold, the report says what did.
const ReportFilePath = "tests/validation/report.json"

// reportDoc is the slice of the runner's report a VERDICT is derived from. The
// console reads the whole document per criterion; the run only needs to know
// whether anything the runner could decide came out negative.
type reportDoc struct {
	Criteria []reportCriterion `json:"criteria"`
}

type reportCriterion struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	// Status is the runner's per-criterion outcome: pass | fail | not_run |
	// not_validated | manual.
	Status string `json:"status"`
}

// VerdictFromReport derives a run's validation verdict from the committed
// report, returning one of the delivery.ValidationVerdict* values.
//
// The rule is deliberately narrow: a verdict is FAILED if and only if the
// runner actually ran a criterion and it failed. Everything else — a criterion
// it could not run, a manual checklist item, a scenario it declared out of
// scope — is not a statement about the deployed system, and treating an
// un-runnable criterion as a failure would make the run's outcome depend on the
// test harness's coverage rather than on the software.
//
// An empty or unparseable report is SKIPPED rather than failed for the same
// reason: the run landed its work either way, and a missing report is a gap in
// reporting, not evidence of a broken deployment.
func VerdictFromReport(raw []byte) string {
	if len(raw) == 0 {
		return delivery.ValidationVerdictSkipped
	}
	var doc reportDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return delivery.ValidationVerdictSkipped
	}
	ran := false
	for _, c := range doc.Criteria {
		switch c.Status {
		case "fail":
			return delivery.ValidationVerdictFailed
		case "pass":
			ran = true
		}
	}
	if !ran {
		return delivery.ValidationVerdictSkipped
	}
	return delivery.ValidationVerdictPassed
}
