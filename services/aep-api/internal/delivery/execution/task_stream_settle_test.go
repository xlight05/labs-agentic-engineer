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

package execution

import (
	"testing"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
)

// The stream closes ONLY on a deployed Task. "abandoned" must NOT settle: during
// the merge→build handoff the issue auto-closes ~2s before the build Execution
// row is admitted, so the Task derives a transient "abandoned" in that window —
// closing on it froze the detail page and dropped the build/OC logs.
func TestIsTaskSettled_OnlyDeployedCloses(t *testing.T) {
	if !isTaskSettled(string(taskmeta.StatusDeployed)) {
		t.Error("deployed must settle the stream (nothing more will happen)")
	}
	if isTaskSettled(string(taskmeta.StatusAbandoned)) {
		t.Error("abandoned must NOT settle — it is transient during the merge→build handoff")
	}
	for _, s := range []taskmeta.DerivedStatus{
		taskmeta.StatusFailed, taskmeta.StatusRejected, taskmeta.StatusBuilding,
		taskmeta.StatusMerged, taskmeta.StatusInProgress, taskmeta.StatusReadyForReview,
		taskmeta.StatusPending, taskmeta.StatusOnHold,
	} {
		if isTaskSettled(string(s)) {
			t.Errorf("%q must keep the stream open, not settle", s)
		}
	}
}
