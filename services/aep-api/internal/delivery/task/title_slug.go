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
	"regexp"
	"strings"
)

// slugNonAlnumRE collapses every run of non-[a-z0-9] into a single hyphen.
var slugNonAlnumRE = regexp.MustCompile(`[^a-z0-9]+`)

// titleSlug lowercases a title and collapses non-alphanumeric runs to single
// hyphens, trimming the leading and trailing ones.
//
// It is the plan's WHOLE dedupe primitive. A milestone's membership is the
// version's task list, and a re-plan (or a crash re-run) is additive-only, so
// "have I already minted this Task?" is answered by comparing slugs against the
// milestone's existing titles — there is no machine-block key to compare any
// more. Case and punctuation are absorbed deliberately: two titles that differ
// only in them are the same Task.
//
// A title that slugifies to "" (all non-ASCII, emoji or punctuation) is not
// deduped at all — the caller skips the empty slug rather than collapsing every
// such title onto one key.
func titleSlug(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = slugNonAlnumRE.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
