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

package tenant

import (
	"regexp"
	"strings"
	"testing"
)

// dns1123Label is the K8s DNS-1123 label grammar (max 63 chars, lower
// alphanumeric + interior hyphens). WorkflowPlaneNamespace output must always
// satisfy it, including on the truncation branch.
var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func TestWorkflowPlaneNamespace_ShortInputPassesThrough(t *testing.T) {
	got := WorkflowPlaneNamespace("acme")
	if got != "workflows-acme" {
		t.Fatalf("WorkflowPlaneNamespace(acme) = %q; want workflows-acme", got)
	}
	if len(got) > 63 || !dns1123Label.MatchString(got) {
		t.Fatalf("%q is not a valid DNS-1123 label", got)
	}
}

func TestWorkflowPlaneNamespace_LowercasesAndTrims(t *testing.T) {
	// The slug is lower-cased + trimmed before the length check, so mixed-case
	// / whitespace-padded org ids normalise rather than producing invalid names.
	if got := WorkflowPlaneNamespace("  Acme  "); got != "workflows-acme" {
		t.Errorf("WorkflowPlaneNamespace(padded/mixed) = %q; want workflows-acme", got)
	}
}

func TestWorkflowPlaneNamespace_TableCases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"default", "workflows-default"},
		{"Acme-Co", "workflows-acme-co"}, // case-normalised, interior hyphen kept
		{"  trimmed  ", "workflows-trimmed"},
	}
	for _, c := range cases {
		if got := WorkflowPlaneNamespace(c.in); got != c.want {
			t.Errorf("WorkflowPlaneNamespace(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestWorkflowPlaneNamespace_TruncatesLongInputWithHashSuffix(t *testing.T) {
	// prefix "workflows-" is 10 chars; anything pushing prefix+slug past 63
	// takes the truncation branch: slug is cut and a stable 8-hex SHA-256
	// suffix is appended so the result still fits and stays unique.
	longSlug := strings.Repeat("a", 80)
	got := WorkflowPlaneNamespace(longSlug)

	if len(got) > 63 {
		t.Fatalf("truncated name length = %d; must be <= 63 (%q)", len(got), got)
	}
	if len(got) != 63 {
		// With a 80-char slug the branch fills the budget exactly:
		// 10 (prefix) + 44 (slug) + 1 ("-") + 8 (hex) = 63.
		t.Errorf("truncated name length = %d; want 63 (full DNS-1123 budget)", len(got))
	}
	if !dns1123Label.MatchString(got) {
		t.Fatalf("truncated name %q is not a valid DNS-1123 label", got)
	}
	if !strings.HasPrefix(got, "workflows-") {
		t.Errorf("truncated name lost its prefix: %q", got)
	}
	// The suffix is a hex hash: after the last '-', 8 hex chars.
	suffix := got[len(got)-8:]
	if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(suffix) {
		t.Errorf("suffix %q is not an 8-char hex hash", suffix)
	}
}

func TestWorkflowPlaneNamespace_TruncationIsDeterministic(t *testing.T) {
	longSlug := strings.Repeat("z", 90)
	if a, b := WorkflowPlaneNamespace(longSlug), WorkflowPlaneNamespace(longSlug); a != b {
		t.Errorf("same long input must map to the same namespace: %q != %q", a, b)
	}
}

func TestWorkflowPlaneNamespace_DistinctLongInputsStayUnique(t *testing.T) {
	// Two long inputs that share the first 60 chars but differ afterward would
	// collide on the truncated prefix alone — the SHA-256 suffix (hashed over
	// the FULL slug) is what keeps them distinct. This is the whole point of
	// the truncation branch.
	base := strings.Repeat("q", 60)
	a := WorkflowPlaneNamespace(base + "-alpha-tenant")
	b := WorkflowPlaneNamespace(base + "-beta-tenant")
	if a == b {
		t.Fatalf("distinct long inputs collided: both mapped to %q", a)
	}
	// Sanity: their truncated slug prefixes are in fact identical, so only the
	// hash disambiguates them.
	if a[:len(a)-8] != b[:len(b)-8] {
		t.Errorf("expected identical truncated prefixes with differing hashes; got %q vs %q", a, b)
	}
}
