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

package sourcecontrol

// White-box tests for repoService's unexported helpers. These stay in
// package gitrepo (not the external gitrepo_test package the client-driving
// tests use) because they exercise unexported symbols — an external test
// package can't see them, and a white-box test can't import clients/github
// (that would cycle through gitrepo).

import (
	"strings"
	"testing"
)

func TestSlugifyProjectName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"My Project":      "my-project",
		"  weird!!name  ": "weird-name",
		"":                "project",
		"---":             "project",
		"UPPER_snake":     "upper-snake",
	}
	for in, want := range cases {
		if got := slugifyProjectName(in); got != want {
			t.Errorf("slugifyProjectName(%q) = %q, want %q", in, got, want)
		}
	}
	// Over-length names are truncated to 40 chars (trailing dash trimmed).
	long := strings.Repeat("a", 60)
	if got := slugifyProjectName(long); len(got) > 40 {
		t.Errorf("slugifyProjectName(len 60) = %d chars, want <= 40", len(got))
	}
}
