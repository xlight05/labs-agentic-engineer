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

// labelUnsafeRE collapses every run of characters GitHub label names handle
// poorly into a single hyphen. Dependency names come from the design and are
// already tame, but a label key must be total.
var labelUnsafeRE = regexp.MustCompile(`[^a-z0-9._-]+`)

// gateDepLabel is the dependency label for a gate issue. An empty or
// unslugifiable name yields "" so callers can append it unconditionally.
func gateDepLabel(depName string) string {
	slug := labelUnsafeRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(depName)), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return ""
	}
	return gateDepLabelPrefix + slug
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
