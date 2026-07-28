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

package delivery_test

import (
	"reflect"
	"testing"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// TestDiffComponents pins the merged-PR path diff — the ONE rule the event
// plane's fan-out and the run supervisor's "have this cycle's builds all
// reported?" read must agree on, which is why it lives in the root rather than
// in either of them.
func TestDiffComponents(t *testing.T) {
	paths := map[string]string{
		"order-service": "services/order",
		"web":           "apps/web",
		"monolith":      "", // builds from the repo root
	}
	t.Run("prefix match, stable order, unmatched reported", func(t *testing.T) {
		got := delivery.DiffComponents([]string{"services/order/main.go", "docs/adr.md"}, map[string]string{
			"order-service": "services/order",
			"web":           "apps/web",
		})
		if !reflect.DeepEqual(got.Components, []string{"order-service"}) {
			t.Fatalf("components = %v, want just order-service", got.Components)
		}
		if !reflect.DeepEqual(got.Unmatched, []string{"docs/adr.md"}) {
			t.Fatalf("unmatched = %v, want the doc change", got.Unmatched)
		}
	})
	t.Run("an empty App Path claims everything", func(t *testing.T) {
		got := delivery.DiffComponents([]string{"docs/adr.md"}, paths)
		if !reflect.DeepEqual(got.Components, []string{"monolith"}) {
			t.Fatalf("components = %v, want monolith", got.Components)
		}
		if len(got.Unmatched) != 0 {
			t.Fatalf("a repo-root component leaves nothing unmatched, got %v", got.Unmatched)
		}
	})
	t.Run("a path prefix is not a directory prefix", func(t *testing.T) {
		got := delivery.DiffComponents([]string{"services/order-legacy/main.go"}, paths)
		if len(got.Components) != 1 || got.Components[0] != "monolith" {
			t.Fatalf("services/order must not match services/order-legacy, got %v", got.Components)
		}
	})
	t.Run("no files, nothing to build", func(t *testing.T) {
		if got := delivery.DiffComponents(nil, paths); len(got.Components) != 0 {
			t.Fatalf("components = %v, want none", got.Components)
		}
	})
}

// TestBuildsAtMerge pins the READ-side inverse of BuildRunName: recovering one
// merge's fan-out from the project's WorkflowRuns, without anything having been
// stored when the fan-out was triggered.
func TestBuildsAtMerge(t *testing.T) {
	const project = "shop"
	const sha = "4a91c2f8ab3199ff"

	run := func(component string, attempt int, status string, completed bool) delivery.MergeBuild {
		return delivery.MergeBuild{
			Component: component,
			RunName:   delivery.BuildRunName(project, component, sha, attempt),
			Status:    status,
			Completed: completed,
		}
	}

	t.Run("keeps only this merge's runs, ordered by component", func(t *testing.T) {
		got := delivery.BuildsAtMerge([]delivery.MergeBuild{
			run("webapp", 1, "Succeeded", true),
			run("api", 1, "Running", false),
			// Another commit entirely — same components, must not appear.
			{Component: "api", RunName: delivery.BuildRunName(project, "api", "ffffffffffff", 1)},
		}, project, sha)

		if len(got) != 2 {
			t.Fatalf("builds = %+v, want exactly this merge's two", got)
		}
		if got[0].Component != "api" || got[1].Component != "webapp" {
			t.Errorf("order = %q,%q, want api,webapp", got[0].Component, got[1].Component)
		}
		if got[0].Status != "Running" || got[0].Completed {
			t.Errorf("api build = %+v, want the running one carried verbatim", got[0])
		}
	})

	t.Run("a re-triggered component reports its LATEST attempt", func(t *testing.T) {
		got := delivery.BuildsAtMerge([]delivery.MergeBuild{
			run("api", 1, "Failed", true),
			run("api", 2, "Running", false),
		}, project, sha)

		if len(got) != 1 {
			t.Fatalf("builds = %+v, want one row per component", got)
		}
		if got[0].Attempt != 2 || got[0].Status != "Running" {
			t.Errorf("build = %+v, want attempt 2 (the re-trigger), not the first red", got[0])
		}
	})

	t.Run("a component whose name contains the SHA prefix is not mis-split", func(t *testing.T) {
		// The component is read off the run's own label, never parsed back out of
		// the name, so a dashed component name cannot confuse the match.
		got := delivery.BuildsAtMerge([]delivery.MergeBuild{
			run("workout-tracker-webapp", 3, "Succeeded", true),
		}, project, sha)

		if len(got) != 1 || got[0].Component != "workout-tracker-webapp" || got[0].Attempt != 3 {
			t.Fatalf("builds = %+v, want the dashed component at attempt 3", got)
		}
	})

	t.Run("no merge SHA means nothing to report", func(t *testing.T) {
		if got := delivery.BuildsAtMerge([]delivery.MergeBuild{run("api", 1, "Running", false)}, project, ""); got != nil {
			t.Fatalf("builds = %+v, want nil for a cycle that has not merged", got)
		}
	})

	t.Run("a prefixed name with no attempt ordinal is not one of ours", func(t *testing.T) {
		got := delivery.BuildsAtMerge([]delivery.MergeBuild{
			{Component: "api", RunName: delivery.BuildRunNamePrefix(project, "api", sha) + "manual"},
		}, project, sha)
		if len(got) != 0 {
			t.Fatalf("builds = %+v, want none", got)
		}
	})
}
