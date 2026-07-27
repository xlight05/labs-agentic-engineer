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

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// The stream closes when the Task's issue is CLOSED, and only then: an open
// issue may still gain a pull request, a merge and a deployment, and closing
// the stream on it would freeze the detail page.
//
// The old rule waited for `deployed` and had to explicitly refuse to settle on
// the transient `abandoned` the merge→build handoff produced. Neither status is
// derivable any more — the vocabulary is open/closed — so the rule is now the
// fact itself.
func TestIsTaskSettled_ClosedIssueSettles(t *testing.T) {
	if !isTaskSettled(delivery.DerivedStatusMerged) {
		t.Error("a closed issue must settle the stream — nothing more will arrive on it")
	}
	if isTaskSettled(delivery.DerivedStatusPending) {
		t.Error("an open issue must keep the stream open")
	}
	// A status this build does not emit must never settle the stream: an
	// unrecognised value means the producer changed and the reader did not, and
	// holding a cheap stream open is the safe direction.
	for _, s := range []string{"", "deployed", "building", "abandoned", "on_hold"} {
		if isTaskSettled(s) {
			t.Errorf("%q is not an emitted status and must not settle the stream", s)
		}
	}
}
