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
	"fmt"
)

// criteriaDoc is the acceptance oracle authored by the validation-criteria
// skill at specs/validation/validation-criteria.json. The minter parses it only
// to render the human summary in the issue; the runner reads the file directly
// for the actual test partitioning.
type criteriaDoc struct {
	Requirements []requirement `json:"requirements"`
}

type requirement struct {
	ID        string      `json:"id"`
	Statement string      `json:"statement"`
	Criteria  []criterion `json:"criteria"`
}

type criterion struct {
	ID     string `json:"id"`
	Must   string `json:"must"`
	Method string `json:"method"` // e2e | scenario | manual
}

// criteriaSummary is the per-method tally rendered in the issue's acceptance
// oracle section.
type criteriaSummary struct {
	E2E      int
	Scenario int
	Manual   int
}

// parseCriteria decodes and minimally validates the oracle: it must have at
// least one requirement carrying at least one criterion. A malformed file is an
// error the caller treats as "skip minting" (never fail the design save).
func parseCriteria(raw []byte) (*criteriaDoc, error) {
	var doc criteriaDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse validation-criteria.json: %w", err)
	}
	total := 0
	for _, r := range doc.Requirements {
		total += len(r.Criteria)
	}
	if total == 0 {
		return nil, fmt.Errorf("validation-criteria.json has no criteria")
	}
	return &doc, nil
}

// summarize tallies criteria by method — mirrors
// scripts/create-validation-issue.mjs:summarize.
func (d *criteriaDoc) summarize() criteriaSummary {
	var s criteriaSummary
	for _, r := range d.Requirements {
		for _, c := range r.Criteria {
			switch c.Method {
			case "e2e":
				s.E2E++
			case "scenario":
				s.Scenario++
			case "manual":
				s.Manual++
			}
		}
	}
	return s
}
