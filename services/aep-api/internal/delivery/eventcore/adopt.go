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

package eventcore

import (
	"context"
	"errors"
	"log/slog"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// ErrNoDeployedMilestone is adoption's honest refusal: a bare issue joins the
// DEPLOYED version's milestone, and a project that has never completed a build
// has no such version. The message is written for a human because the console
// dispatch path returns it to one verbatim.
var ErrNoDeployedMilestone = errors.New("no milestone for the deployed version — trigger a build")

// AdoptTarget is the issue being handed to the coding agent, plus the
// milestone it already belongs to (0 when it is a bare issue). The webhook
// path reads both out of the payload — every issues delivery embeds the full
// issue — so adoption costs no extra GitHub read.
type AdoptTarget struct {
	Number          int
	MilestoneNumber int
	MilestoneTitle  string
}

// AdoptIssue hands one issue to the coding agent, from either of the two
// adoption routes: the `aep:codingagent` label arriving by webhook, and the
// console's dispatch button (which calls this directly, because a label the
// platform stamps itself comes back as an echo and is dropped).
//
// The rules, in order:
//
//   - An issue that already has a milestone keeps it. The human put it there.
//   - A bare issue joins the deployed version's milestone — the version it is
//     an incident against. With no deployed version there is nothing to attach
//     it to, and the caller gets ErrNoDeployedMilestone rather than a guess.
//   - If a run is already live on that milestone, this is a no-op: the run
//     re-reads its milestone at the next cycle boundary and picks the issue up
//     there. Starting a second run on one milestone would put two agents on
//     one branch.
//   - Otherwise an incident run starts over that milestone.
//
// Adoption does NOT stamp the agent-work label. The working set is read from
// the milestone, and the labelling is the human's act of adoption — inventing
// a second, platform-authored path to the same state would make "who adopted
// this" unanswerable.
func (e *Events) AdoptIssue(ctx context.Context, orgID, projectID string, target AdoptTarget) error {
	if target.Number == 0 || e.p.Runs == nil {
		return nil
	}
	milestone := MilestoneRef{Number: target.MilestoneNumber, Title: target.MilestoneTitle}
	if milestone.Number == 0 {
		deployed, err := e.p.Runs.DeployedMilestoneRun(ctx, orgID, projectID)
		if err != nil {
			return err
		}
		if deployed == nil {
			return ErrNoDeployedMilestone
		}
		milestone = MilestoneRef{Number: deployed.MilestoneNumber, Title: deployed.MilestoneTitle}
		if e.p.Issues != nil {
			if err := e.p.Issues.SetIssueMilestone(ctx, orgID, projectID, target.Number, milestone.Number); err != nil {
				return err
			}
		}
		slog.InfoContext(ctx, "eventcore: adopted a bare issue into the deployed version's milestone",
			"issue", target.Number, "milestone", milestone.Number, "version", milestone.Title)
	}

	live, err := e.p.Runs.LiveRunForMilestone(ctx, orgID, projectID, milestone.Number)
	if err != nil {
		return err
	}
	if live != nil {
		slog.DebugContext(ctx, "eventcore: adoption into a milestone with a live run — the next cycle picks it up",
			"issue", target.Number, "milestone", milestone.Number, "run", live.ID)
		return nil
	}
	return e.startRun(ctx, orgID, projectID, milestone)
}

// startRun asks the supervisor for an incident run over a milestone. Every run
// this package starts is an incident adoption — the spec-build origin belongs
// to the plan path alone, where the version mutex lives.
func (e *Events) startRun(ctx context.Context, orgID, projectID string, milestone MilestoneRef) error {
	if e.p.Starter == nil {
		slog.DebugContext(ctx, "eventcore: no run starter wired — nothing to start",
			"project", projectID, "milestone", milestone.Number)
		return nil
	}
	return e.p.Starter.StartRun(ctx, delivery.StartRunRequest{
		OrgID:           orgID,
		ProjectID:       projectID,
		MilestoneNumber: milestone.Number,
		MilestoneTitle:  milestone.Title,
		Origin:          delivery.RunOriginIncidentAdoption,
	})
}
