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
	"fmt"
	"strings"
)

// OriginSpecPlan is the default `origin` a context file carries: the Task came
// from a spec planning turn. It is a cross-language wire value (the TS side's
// parseTaskContextFile reads it back), so it is a plain string here rather than
// a domain type — this package is a pure encoding and imports no domain.
const OriginSpecPlan = "spec-plan"

// TaskContextFile is the projection of an existing Task rendered into the
// plan-turn read-only context as tasks/<issueNumber>.md (§6). It is the Go
// counterpart of @aep/agent-stream's parseTaskContextFile: YAML frontmatter +
// markdown body. issueNumber/component/title are required; dependsOn defaults to
// [], origin to spec-plan; specTag/designTag are optional.
type TaskContextFile struct {
	IssueNumber int
	Component   string
	Title       string
	DependsOn   []string
	Origin      string
	SpecTag     string // optional lineage
	DesignTag   string // optional lineage
	Body        string // optional task body markdown (after the fence)
}

// Path returns the context-file path (tasks/<issueNumber>.md), matching the
// phase-1 TASK_CONTEXT_FILE_RE the agent side keys off.
func (f TaskContextFile) Path() string { return fmt.Sprintf("tasks/%d.md", f.IssueNumber) }

// Render produces the frontmatter + body content. Field order is fixed
// (issueNumber, component, title, dependsOn, origin, then the optionals) so the
// output is stable; string scalars are double-quoted so a human title with a
// colon or hash cannot break the frontmatter the agent-side YAML parser reads.
func (f TaskContextFile) Render() string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "issueNumber: %d\n", f.IssueNumber)
	fmt.Fprintf(&sb, "component: %s\n", yamlQuote(f.Component))
	fmt.Fprintf(&sb, "title: %s\n", yamlQuote(f.Title))
	fmt.Fprintf(&sb, "dependsOn: %s\n", yamlFlowList(f.DependsOn))
	origin := f.Origin
	if origin == "" {
		origin = OriginSpecPlan
	}
	fmt.Fprintf(&sb, "origin: %s\n", yamlQuote(origin))
	if f.SpecTag != "" {
		fmt.Fprintf(&sb, "specTag: %s\n", yamlQuote(f.SpecTag))
	}
	if f.DesignTag != "" {
		fmt.Fprintf(&sb, "designTag: %s\n", yamlQuote(f.DesignTag))
	}
	sb.WriteString("---\n")
	if body := strings.TrimSpace(f.Body); body != "" {
		sb.WriteByte('\n')
		sb.WriteString(body)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// RenderTaskContextFile renders the file and returns its path and content —
// convenience for the plan-turn context assembler (§6).
func RenderTaskContextFile(f TaskContextFile) (path, content string) {
	return f.Path(), f.Render()
}

// yamlQuote double-quotes a scalar and escapes backslash/quote; newlines are
// flattened to spaces (context-file scalars are single-line).
func yamlQuote(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// yamlFlowList renders a string slice as a YAML flow sequence of quoted scalars;
// nil/empty renders "[]".
func yamlFlowList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = yamlQuote(it)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
