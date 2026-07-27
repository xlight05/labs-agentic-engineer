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
)

// This file is the strict-server (contract-first) entry surface of the build
// service: exported, org-explicit methods the internal/api handlers call
// (handlers_build.go). Run IS the build sequence (build_service.go), shared
// with the non-HTTP StartProjectBuild trigger; its edge-mapped failures are
// *EdgeError values the api-layer mapper copies onto the flat envelope.

// List is the VERSION LEDGER: one entry per spec version the platform has
// built, newest first, each carrying the state of the newest milestone run that
// worked it.
//
// It is a single DB read of the run rows — no GitHub, no cluster, no workflow
// query — which is the whole point: the console's overview version dropdown
// fetches it on demand and the Builds page may poll it at 5s while a run is
// live without spending GitHub rate.
//
// Rows arrive newest-first, so the FIRST row seen per milestone title is that
// version's newest run. Keying on the title is not a GitHub title match: it is
// the `v<N>` tag THIS platform recorded when it cut the version, and the
// milestone NUMBER travels with it for anything that needs a lookup key.
//
// Builds is always non-nil so the JSON body is [] rather than null.
func (s *Service) List(ctx context.Context, orgID, projectID string) (BuildList, error) {
	if s.plan == nil || s.plan.runs == nil {
		return BuildList{Builds: []BuildSummary{}}, nil
	}
	rows, err := s.plan.runs.ListByProject(ctx, orgID, projectID)
	if err != nil {
		return BuildList{}, fmt.Errorf("list builds: %w", err)
	}
	seen := make(map[string]bool, len(rows))
	builds := make([]BuildSummary, 0, len(rows))
	for i := range rows {
		row := rows[i]
		if row.MilestoneTitle == "" || seen[row.MilestoneTitle] {
			continue
		}
		seen[row.MilestoneTitle] = true
		builds = append(builds, BuildSummary{
			Tag:             row.MilestoneTitle,
			MilestoneNumber: row.MilestoneNumber,
			Status:          statusFromRunState(row.State),
			Reason:          row.TerminalReason,
			StartedAt:       row.CreatedAt,
			CompletedAt:     row.EndedAt,
		})
	}
	return BuildList{Builds: builds}, nil
}
