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
	"strconv"
	"strings"

	"github.com/wso2/aep/aep-api/internal/gen"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
)

// defaultBuildAuthRetryBudget caps git-clone-auth build retries before the
// Execution is Finished failed with buildAuthRetryExceededReason. Ported from
// the legacy build watcher (phase2.md §9.3). A short-lived install token can
// expire between staging and the checkout step; a bounded re-mint absorbs that
// transient without a runaway retry loop.
const defaultBuildAuthRetryBudget = 3

// buildAuthRetryReasonPrefix + the attempt count is stamped on a still-running
// build row across git-auth retries (via ExecutionRepository.NoteBuildRetry), so
// the next sweep reads the attempt count back off the row's reason rather than a
// dedicated column — the "tracked on the execution row's reason" minimal
// equivalent of the legacy BuildAuthRetryCount column.
const buildAuthRetryReasonPrefix = "build_auth_retry:"

// buildAuthRetryExceededReason is the terminal reason once the budget is spent.
const buildAuthRetryExceededReason = "build.auth_retry_exceeded"

// authFailureMarkers are substring matches for the well-known git-clone-step
// failure outputs. A failed checkout-source task carrying any of these is
// classified as a transient auth failure and retried. Conservative — explicit
// substrings, not regex — so an unrelated failure never trips the retry budget.
// Ported verbatim from the legacy build watcher.
var authFailureMarkers = []string{
	"fatal: Authentication failed",
	"fatal: could not read Username",
	"could not read password",
	"unable to access ", // git's HTTP 403 prefix
	"the requested URL returned error: 401",
	"the requested URL returned error: 403",
}

// isGitCloneAuthFailure reports whether a completed-failed build WorkflowRun
// failed because its checkout-source step hit a git-clone auth marker. OC's CRD
// does not expose per-task outputs, so this matches Tasks[].Phase ∈
// {Failed,Error} on the checkout-source step AND a substring of Tasks[].Message.
// An empty Message returns false conservatively (a non-auth failure is safer
// than a runaway retry budget). Ported from the legacy build watcher.
func isGitCloneAuthFailure(run *gen.WorkflowRun) bool {
	if run == nil {
		return false
	}
	for _, task := range run.Tasks {
		if task.Name != "checkout-source" {
			continue
		}
		if task.Phase != "Failed" && task.Phase != "Error" {
			continue
		}
		for _, marker := range authFailureMarkers {
			if strings.Contains(task.Message, marker) {
				return true
			}
		}
		return false
	}
	return false
}

// classifyBuildRun maps a completed build WorkflowRun to (succeeded, authFailure).
// succeeded is true only for ReasonWorkflowSucceeded; authFailure is true when a
// non-succeeded run's checkout-source step matches a git-clone auth marker. Both
// false means a plain (non-auth) failure — the terminal-failed path.
func classifyBuildRun(run *gen.WorkflowRun) (succeeded, authFailure bool) {
	if run == nil || !run.Completed {
		return false, false
	}
	if run.Status == openchoreo.ReasonWorkflowSucceeded {
		return true, false
	}
	return false, isGitCloneAuthFailure(run)
}

// buildAuthRetryReason renders the retry-attempt reason stamped on the row.
func buildAuthRetryReason(attempt int) string {
	return buildAuthRetryReasonPrefix + strconv.Itoa(attempt)
}

// parseBuildAuthRetryAttempt reads the attempt count back off a row's reason
// (0 when the reason is empty or not a retry marker — the first auth failure of
// a build carries no marker yet).
func parseBuildAuthRetryAttempt(reason string) int {
	rest, ok := strings.CutPrefix(reason, buildAuthRetryReasonPrefix)
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		return 0
	}
	return n
}
