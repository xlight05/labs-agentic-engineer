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

package taskplan

import (
	"strings"
	"testing"
)

func TestRenderTaskContextFilePath(t *testing.T) {
	path, _ := RenderTaskContextFile(TaskContextFile{IssueNumber: 42, Component: "svc", Title: "t"})
	if path != "tasks/42.md" {
		t.Fatalf("path = %q; want tasks/42.md", path)
	}
}

// TestRenderFull pins the frontmatter field order and content for a fully
// populated context file.
func TestRenderFull(t *testing.T) {
	f := TaskContextFile{
		IssueNumber: 7,
		Component:   "order-service",
		Title:       "Implement order-service",
		DependsOn:   []string{"user-service", "catalog"},
		Origin:      OriginSpecPlan,
		SpecTag:     "requirements-v3",
		DesignTag:   "design-v5",
		Body:        "## Scope\ndo the thing",
	}
	got := f.Render()
	want := "---\n" +
		"issueNumber: 7\n" +
		`component: "order-service"` + "\n" +
		`title: "Implement order-service"` + "\n" +
		`dependsOn: ["user-service", "catalog"]` + "\n" +
		`origin: "spec-plan"` + "\n" +
		`specTag: "requirements-v3"` + "\n" +
		`designTag: "design-v5"` + "\n" +
		"---\n\n## Scope\ndo the thing\n"
	if got != want {
		t.Fatalf("render drift:\n got: %q\nwant: %q", got, want)
	}
}

// TestRenderMinimalDefaults confirms required fields render, dependsOn defaults
// to [], origin defaults to spec-plan, optionals are omitted, and no body means
// no trailing content.
func TestRenderMinimalDefaults(t *testing.T) {
	got := TaskContextFile{IssueNumber: 1, Component: "svc", Title: "t"}.Render()
	want := "---\n" +
		"issueNumber: 1\n" +
		`component: "svc"` + "\n" +
		`title: "t"` + "\n" +
		"dependsOn: []\n" +
		`origin: "spec-plan"` + "\n" +
		"---\n"
	if got != want {
		t.Fatalf("minimal render drift:\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(got, "derivedStatus") || strings.Contains(got, "specTag") || strings.Contains(got, "designTag") {
		t.Fatalf("optional fields should be omitted: %q", got)
	}
}

// TestRenderQuotesDangerousTitle confirms a colon/hash in the title can't break
// the frontmatter the agent-side YAML parser reads.
func TestRenderQuotesDangerousTitle(t *testing.T) {
	got := TaskContextFile{IssueNumber: 1, Component: "svc", Title: `fix: cache #3 "hot" path`}.Render()
	if !strings.Contains(got, `title: "fix: cache #3 \"hot\" path"`) {
		t.Fatalf("title not safely quoted/escaped: %q", got)
	}
}
