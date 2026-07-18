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
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
)

// plannedTask is the fully-resolved facts of a Task the plan tap is about to
// create (from a planTask tool result). The idempotency key is derived from the
// project + lineage tag (the spec tag `v<N>`) + target + title so a crash
// re-run dedupes against issues created on an earlier run (§2, §6).
type plannedTask struct {
	Component string
	Title     string
	DependsOn []string
	Origin    taskmeta.Origin
	SpecTag   string
	DesignTag string
	Rationale string
}

// composePlannedIssue builds the machine block (with a computed idempotency key)
// and the full issue body for a newly planned Task. It absorbs what
// gitrepo/issue_body.go did, now expressed over the taskmeta encoding: the block
// is the authoritative structured facts, the rationale is the planner's one-line
// justification, and the body is written later by updateTask.
func composePlannedIssue(project string, p plannedTask) (block taskmeta.Block, body string) {
	block = taskmeta.Block{
		Component: p.Component,
		DependsOn: p.DependsOn,
		Origin:    p.Origin,
		SpecTag:   p.SpecTag,
		DesignTag: p.DesignTag,
	}
	block.Key = taskmeta.Key(project, p.DesignTag, block.Target(), p.Title)
	body = taskmeta.ComposeBody(block, taskmeta.Human{Rationale: p.Rationale})
	return block, body
}

// recomposeBody re-serializes a Task's issue body after an updateTask patch: the
// (possibly patched) block plus the patched human parts. The block stays the
// authoritative structured facts; only the fields the tap touched change. It is
// canonical output (block-first), so a subsequent block-repair is a no-op —
// which is what keeps repair convergent (§9.2).
func recomposeBody(block taskmeta.Block, rationale, markdown string) string {
	return taskmeta.ComposeBody(block, taskmeta.Human{Rationale: rationale, Body: markdown})
}
