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

package provisioning

import (
	"regexp"
	"strings"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// A gate issue is PROSE plus LABELS. Its body is written for a human to read
// and nothing platform-side parses it, so the one structured fact the platform
// still needs from a gate — WHICH DEPENDENCY it holds — rides a label:
//
//	aep:provision      the gate marker (delivery.LabelProvisionGate)
//	aep:dep/<slug>     the dependency this gate is for
//
// That pair is the whole index. Both the mint-time dedupe ("does this dep
// already have an open gate?") and the drawer's resolve ("which issue do I
// close for this dep?") are label queries — never a body read, and never a
// title match, because a human may rewrite a title.
const gateDepLabelPrefix = "aep:dep/"

// aep:wired/<slug> is the same trick applied to the OTHER side of a dependency:
// stamped on a WORKING-SET issue, it records that the platform has already
// posted dependency <slug>'s resolved-wiring comment there (wiring.go). It is
// the comment's idempotency key — GitHub has no dedupe on comments the way
// CreateIssue has DedupeKey, and the resolve path re-runs (a re-build re-mints
// and re-settles a gate for a dependency that is already Ready).
//
// It is deliberately a DIFFERENT prefix from aep:dep/: on a gate the label means
// "this issue IS the gate for X", and overloading it on a coding issue would
// make gateDepFromLabels answer for issues that are not gates.
const wiredDepLabelPrefix = "aep:wired/"

// labelUnsafeRE collapses every run of characters GitHub label names handle
// poorly into a single hyphen. Dependency names come from the design and are
// already tame, but a label key must be total.
var labelUnsafeRE = regexp.MustCompile(`[^a-z0-9._-]+`)

// depSlug is the label-safe form of a dependency name, or "" when the name
// slugifies to nothing (the caller then emits no label at all).
func depSlug(depName string) string {
	slug := labelUnsafeRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(depName)), "-")
	return strings.Trim(slug, "-")
}

// gateDepLabel is the dependency label for a gate issue. An empty or
// unslugifiable name yields "" so callers can append it unconditionally.
func gateDepLabel(depName string) string {
	slug := depSlug(depName)
	if slug == "" {
		return ""
	}
	return gateDepLabelPrefix + slug
}

// wiredDepLabel is the "resolved wiring for this dependency has been posted
// here" marker for a working-set issue. Empty for an unslugifiable name.
func wiredDepLabel(depName string) string {
	slug := depSlug(depName)
	if slug == "" {
		return ""
	}
	return wiredDepLabelPrefix + slug
}

// gateLabels is the full label set a gate issue is minted with. A gate
// deliberately does NOT carry the `aep` working-set label: it is never agent
// work, only a hold on the next dispatch.
func gateLabels(depName string) []string {
	labels := []string{delivery.LabelProvisionGate}
	if l := gateDepLabel(depName); l != "" {
		labels = append(labels, l)
	}
	return labels
}

// gateDepFromLabels reads a gate issue's dependency slug back out of its
// labels, or "" when it carries none (a hand-filed gate).
func gateDepFromLabels(labels []string) string {
	for _, l := range labels {
		if rest, ok := strings.CutPrefix(strings.ToLower(l), gateDepLabelPrefix); ok {
			return rest
		}
	}
	return ""
}
