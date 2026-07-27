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
	"fmt"
	"strings"
)

// A planned Task's issue body is PROSE, for the coding agent to read. Nothing
// parses it platform-side: the milestone is the version pin, the `aep` label is
// the working-set marker, and ordering is the "Depends on #N" lines the AGENT
// honours. That is the whole structure — the body is free to read like a brief
// because no code depends on its shape.
//
// The one structured thing a body still carries is a REFERENCE: the issue
// numbers of the Tasks this one depends on, so the agent can follow them.

// plannedTask is one Task's facts as the plan tap tracks them: what the planner
// said at creation, plus whatever a later updateTask patched. The tap
// re-renders the whole body from this state on every patch, so a body is always
// the current facts rather than an accumulation of edits.
type plannedTask struct {
	// Component is the design component this Task builds.
	Component string
	// AppPath is the component's source directory relative to the repo root,
	// resolved from the design. Empty when the design does not pin one (the
	// component builds from the repo root) or no design reader is wired.
	AppPath string
	// DependsOn holds design COMPONENT names, as the planner emits them. They
	// are resolved to issue numbers at render time, because the issue a
	// dependency will get may not exist yet when this Task is planned.
	DependsOn []string
	// Rationale is the planner's one-line justification; Body is the fuller
	// brief a later updateTask writes.
	Rationale string
	Body      string
}

// composeTaskBody renders a Task's issue body. issueFor resolves a design
// component name to the issue number planned for it this run, reporting false
// when the platform has no issue for it (a forward reference the planner emits
// before the dependency's own Task, or a component outside this plan). An
// unresolved dependency is still named, by component — losing the ordering hint
// entirely would be worse than a name the agent can search for.
func composeTaskBody(p plannedTask, issueFor func(component string) (int, bool)) string {
	var sb strings.Builder
	if r := strings.TrimSpace(p.Rationale); r != "" {
		sb.WriteString(r)
		sb.WriteString("\n\n")
	}
	if c := strings.TrimSpace(p.Component); c != "" {
		fmt.Fprintf(&sb, "**Component:** `%s`\n", c)
	}
	if ap := strings.TrimSpace(p.AppPath); ap != "" {
		fmt.Fprintf(&sb, "**App Path:** `%s`\n", ap)
	}
	for _, dep := range p.DependsOn {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		if n, ok := issueFor(dep); ok && n > 0 {
			fmt.Fprintf(&sb, "Depends on #%d\n", n)
			continue
		}
		fmt.Fprintf(&sb, "Depends on the `%s` task\n", dep)
	}
	if b := strings.TrimSpace(p.Body); b != "" {
		sb.WriteString("\n")
		sb.WriteString(b)
		sb.WriteString("\n")
	}
	return sb.String()
}
