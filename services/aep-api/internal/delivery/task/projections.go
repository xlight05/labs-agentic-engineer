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

package task

import (
	"context"
	"log/slog"

	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
)

// Projections writes the aep:status/* projection label onto a Task — the ONLY
// GitHub-side status mirror (§4: Projects v2 is gone). It is write-only and
// never read back as truth (reads.go derives status live); it exists for humans
// and cheap GitHub-side filtering. Best-effort by design — a projection failure
// never affects correctness.
type Projections struct {
	issues IssueClient
}

// NewProjections wires the projection writer.
func NewProjections(issues IssueClient) *Projections {
	return &Projections{issues: issues}
}

// Project sets the aep:status/<derived> label on a Task, removing any other
// aep:status/* label (the projection is single-valued). currentLabels is the
// issue's live label set; passing it lets Project remove stale status labels in
// place without a read. Best-effort — errors are logged, not returned.
func (p *Projections) Project(ctx context.Context, orgID, projectID string, number int, currentLabels []string, status taskmeta.DerivedStatus) {
	desired := taskmeta.StatusLabel(status)
	for _, l := range currentLabels {
		if taskmeta.Classify(l) == taskmeta.KindStatus && l != desired {
			if err := p.issues.RemoveLabel(ctx, orgID, projectID, number, l); err != nil {
				slog.WarnContext(ctx, "projection: remove stale status label failed", "issue", number, "label", l, "error", err)
			}
		}
	}
	if err := p.issues.AddLabels(ctx, orgID, projectID, number, []string{desired}); err != nil {
		slog.WarnContext(ctx, "projection: add status label failed", "issue", number, "status", status, "error", err)
	}
}
