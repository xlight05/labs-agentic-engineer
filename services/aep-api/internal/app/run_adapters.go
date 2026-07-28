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
	"context"
	"errors"
	"log/slog"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
	"github.com/wso2/aep/aep-api/internal/delivery"
	"github.com/wso2/aep/aep-api/internal/delivery/run"
	"github.com/wso2/aep/aep-api/internal/delivery/validation"
	"github.com/wso2/aep/aep-api/internal/spec"
)

// The composition-root adapters behind the run supervisor's consumer ports.
// Three of its eight ports are satisfied by the issue service and the design
// reader with no adapter at all; these are the four that need one.

// runRuns projects the milestone-run repository onto the supervisor's RunStore.
// The repository's guarded mutators return the row they changed (or nil when a
// terminal run made them a no-op); the supervisor only needs to know the write
// was attempted, so the row is dropped here.
type runRuns struct {
	runs delivery.MilestoneRunRepository
}

func (a runRuns) TryAdmit(ctx context.Context, row *delivery.MilestoneRun) (bool, *delivery.MilestoneRun, error) {
	return a.runs.TryAdmit(ctx, row)
}

// LiveRunForMilestone is the same "is anybody on this milestone?" read the
// event plane makes, restated here because the supervisor's start path must
// answer it without importing a peer.
func (a runRuns) LiveRunForMilestone(ctx context.Context, orgID, projectID string, milestoneNumber int) (*delivery.MilestoneRun, error) {
	rows, err := a.runs.ListByMilestone(ctx, orgID, projectID, milestoneNumber)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if !delivery.IsTerminalRunState(rows[i].State) {
			return &rows[i], nil
		}
	}
	return nil, nil
}

func (a runRuns) SetState(ctx context.Context, id, state string) error {
	_, err := a.runs.SetState(ctx, id, state)
	return err
}

func (a runRuns) Settle(ctx context.Context, id, state, reason string) error {
	_, err := a.runs.Settle(ctx, id, state, reason)
	return err
}

func (a runRuns) BumpBudget(ctx context.Context, id string, counter delivery.RunBudget) error {
	_, err := a.runs.BumpBudget(ctx, id, counter)
	return err
}

func (a runRuns) SetValidationVerdict(ctx context.Context, id, verdict string) error {
	_, err := a.runs.SetValidationVerdict(ctx, id, verdict)
	return err
}

// runCycles projects the cycle repository onto the supervisor's CycleStore.
type runCycles struct{ cycles delivery.RunCycleRepository }

func (a runCycles) Append(ctx context.Context, cycle *delivery.RunCycle) (string, error) {
	if err := a.cycles.Append(ctx, cycle); err != nil {
		return "", err
	}
	return cycle.ID, nil
}

func (a runCycles) NoteDispatch(ctx context.Context, cycleID, jobRef string) error {
	_, err := a.cycles.NoteDispatch(ctx, cycleID, jobRef)
	return err
}

func (a runCycles) Finish(ctx context.Context, cycleID, mergeSHA string) error {
	_, err := a.cycles.Finish(ctx, cycleID, mergeSHA)
	return err
}

func (a runCycles) Latest(ctx context.Context, orgID, runID string) (*delivery.RunCycle, error) {
	return a.cycles.Latest(ctx, orgID, runID)
}

// runBuilds reads a component's OpenChoreo WorkflowRuns back for the
// supervisor, mapping OpenChoreo's condition vocabulary onto the two facts the
// loop reasons about. The supervisor never triggers a build — the event plane
// owns that, and its automatic re-trigger budget is derived from these same
// runs, so both halves count one source.
type runBuilds struct{ oc openchoreo.ComponentClient }

func (a runBuilds) ListBuildRuns(ctx context.Context, orgID, projectID, component string) ([]run.BuildRunInfo, error) {
	list, err := a.oc.ListWorkflowRuns(ctx, orgID, projectID, component, 0, "")
	if err != nil {
		return nil, err
	}
	if list == nil {
		return nil, nil
	}
	out := make([]run.BuildRunInfo, 0, len(list.Items))
	for _, item := range list.Items {
		out = append(out, run.BuildRunInfo{
			Name:      item.Name,
			Terminal:  item.Completed,
			Succeeded: item.Status == openchoreo.ReasonWorkflowSucceeded,
		})
	}
	return out, nil
}

// runValidation adapts the validation feature onto the supervisor's
// ValidationCoordinator port: mint the run's validation issue into the
// milestone at deployed-green, and read the runner's verdict back afterwards.
//
// The milestone assignment happens HERE rather than inside the minter because
// the minter is shared with the pre-milestone path; a freshly minted validation
// issue is moved into the run's milestone so it joins the version's ledger.
// It deliberately does not gain the `aep` working-set label — the validation
// cycle is dispatched at it by number, and a working-set membership would only
// hold settle open.
type runValidation struct {
	svc       *validation.Service
	art       spec.ArtifactService
	files     spec.FilesService
	milestone milestoneAssigner
}

// milestoneAssigner is the one issue-service verb this adapter needs.
type milestoneAssigner interface {
	SetIssueMilestone(ctx context.Context, orgID, projectID string, number, milestoneNumber int) error
}

func (a runValidation) EnsureValidationIssue(ctx context.Context, orgID, projectID string, milestoneNumber int) (int, error) {
	designTag := a.art.LatestDesignTag(ctx, orgID, projectID)
	number, err := a.svc.ResolveValidationTask(ctx, orgID, projectID, designTag)
	if err != nil || number == 0 {
		return 0, err
	}
	if a.milestone != nil && milestoneNumber > 0 {
		if merr := a.milestone.SetIssueMilestone(ctx, orgID, projectID, number, milestoneNumber); merr != nil {
			// The cycle can still be dispatched at the issue by number; only the
			// version's ledger loses an entry.
			slog.WarnContext(ctx, "run: could not file the validation issue under the milestone",
				"project", projectID, "issue", number, "milestone", milestoneNumber, "error", merr)
		}
	}
	return number, nil
}

func (a runValidation) Verdict(ctx context.Context, orgID, projectID string) (string, error) {
	fc, err := a.files.Read(ctx, orgID, projectID, validation.ReportFilePath)
	if err != nil {
		if errors.Is(err, spec.ErrFileNotFound) {
			// The validation cycle merged but committed no report. Skipped, not
			// failed: an absent report is a gap in reporting, not evidence that the
			// deployment is broken.
			return delivery.ValidationVerdictSkipped, nil
		}
		return "", err
	}
	return validation.VerdictFromReport([]byte(fc.Content)), nil
}

// runreadProjectBuilds reads every build WorkflowRun in a project so the run
// read can derive one cycle's builds from its merge SHA. One call rather than
// one per component: the read side does not know which components a merge
// touched, and it does not need to — the run names carry the (component,
// commit, attempt) triple, so delivery.BuildsAtMerge recovers the fan-out by
// filtering. Nothing is stored; this is the same cluster-is-the-truth rule the
// re-trigger budget follows.
type runreadProjectBuilds struct{ oc openchoreo.ComponentClient }

func (a runreadProjectBuilds) ListProjectBuildRuns(ctx context.Context, orgID, projectID string) ([]delivery.MergeBuild, error) {
	list, err := a.oc.ListProjectWorkflowRuns(ctx, orgID, projectID, 0, "")
	if err != nil {
		return nil, err
	}
	if list == nil {
		return nil, nil
	}
	out := make([]delivery.MergeBuild, 0, len(list.Items))
	for _, item := range list.Items {
		out = append(out, delivery.MergeBuild{
			Component: item.ComponentName,
			RunName:   item.Name,
			Status:    item.Status,
			Completed: item.Completed,
			StartedAt: item.StartedAt,
		})
	}
	return out, nil
}
