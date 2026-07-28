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

package runread

import (
	"context"
	"fmt"

	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/gen"
)

// CycleBuilds answers "what did this cycle's merge build, and how did it go".
//
// It is a SEPARATE service from Reads on purpose. Reads is DB-only, which is
// what lets the console poll the run story at 5s; this read costs a cluster
// call, so it is its own endpoint the console fetches only for a cycle that has
// a merge SHA. Folding the two together would have made every run poll a
// cluster poll.
//
// Nothing here is stored. Per-component build state is derived from OpenChoreo
// on read, always — the same rule the event plane's re-trigger budget follows,
// and for the same reason: one source of truth that a redelivery or a crashed
// handler cannot desynchronise.
type CycleBuilds struct {
	runs   RunReader
	cycles CycleReader
	builds ProjectBuildLister
}

// NewCycleBuilds wires the read. A nil builds lister is a degraded boot without
// the OpenChoreo client; the read then answers empty rather than failing, which
// is the same answer a project with no builds yet gives.
func NewCycleBuilds(runs RunReader, cycles CycleReader, builds ProjectBuildLister) *CycleBuilds {
	return &CycleBuilds{runs: runs, cycles: cycles, builds: builds}
}

// ForCycle returns the builds one cycle's merge produced.
//
// The tag is resolved through the run rows exactly as the run read resolves it,
// and the cycle is then looked up WITHIN those rows rather than by id alone:
// that is the tenant fence. A cycle id belonging to another org, or to a
// different version of this project, is not found here — so a caller cannot
// read a cycle by guessing its id.
func (c *CycleBuilds) ForCycle(ctx context.Context, orgID, projectID, tag, cycleID string) (*gen.CycleBuildList, error) {
	if c == nil || c.runs == nil || c.cycles == nil {
		return nil, fmt.Errorf("runread: cycle builds not configured")
	}
	empty := &gen.CycleBuildList{Items: []gen.CycleBuild{}}

	number, found, err := c.runs.MilestoneNumberForTag(ctx, orgID, projectID, tag)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrTagNotFound
	}
	rows, err := c.runs.ListByMilestone(ctx, orgID, projectID, number)
	if err != nil {
		return nil, err
	}

	cycle, err := c.findCycle(ctx, orgID, rows, cycleID)
	if err != nil {
		return nil, err
	}
	if cycle == nil {
		return nil, ErrCycleNotFound
	}
	// A cycle whose pull request has not merged has nothing to have built. That
	// is the ordinary mid-cycle answer, not an error.
	if cycle.MergeSHA == "" || c.builds == nil {
		return empty, nil
	}

	runsAtCluster, err := c.builds.ListProjectBuildRuns(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	matched := delivery.BuildsAtMerge(runsAtCluster, projectID, cycle.MergeSHA)
	out := &gen.CycleBuildList{Items: make([]gen.CycleBuild, 0, len(matched))}
	for _, b := range matched {
		out.Items = append(out.Items, gen.CycleBuild{
			Component: b.Component,
			BuildName: b.RunName,
			Status:    b.Status,
			Completed: b.Completed,
			Attempt:   int64(b.Attempt),
			StartedAt: b.StartedAt,
		})
	}
	return out, nil
}

// findCycle locates the cycle among the version's runs. Scanning the version's
// own runs is what fences the read — see ForCycle.
func (c *CycleBuilds) findCycle(ctx context.Context, orgID string, rows []delivery.MilestoneRun, cycleID string) (*delivery.RunCycle, error) {
	for i := range rows {
		cycles, err := c.cycles.ListByRun(ctx, orgID, rows[i].ID)
		if err != nil {
			return nil, err
		}
		for j := range cycles {
			if cycles[j].ID == cycleID {
				return &cycles[j], nil
			}
		}
	}
	return nil, nil
}
