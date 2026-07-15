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

// UNIT tier: the pure SKILL.md helpers in skill_service.go and the RFC-9457
// no git, no HTTP, no cache. These pin the
// parse/hash primitives every higher tier composes with, and the
// mapSkillError status table exhaustively (the component tier only reaches the
// reachable subset). Complements skill_mutation_service_test.go, which covers
// parseAndValidateSkillMD (the validating wrapper); this file pins parseSkillMD
// (the raw splitter) and contentSHA directly.
package skills

import (
	"strings"
	"testing"
)

func TestParseSkillMD_Table(t *testing.T) {
	t.Parallel()
	const valid = "---\nname: go\ndescription: A description.\nmetadata:\n  aep.version: \"2\"\n---\n\n# Body\n\ncontent\n"

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		fm, body, err := parseSkillMD(valid)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fm.Name != "go" || strings.TrimSpace(fm.Description) != "A description." {
			t.Fatalf("frontmatter = %+v", fm)
		}
		if !strings.Contains(body, "content") {
			t.Fatalf("body missing content: %q", body)
		}
	})

	// Every malformed case must surface a DISTINCT parse error — the validating
	// wrapper flattens them all to FRONTMATTER_INVALID, so this is the only place
	// the individual guards are pinned.
	cases := []struct {
		name    string
		in      string
		wantSub string
	}{
		{"no frontmatter fence", "# Just a heading\n\nprose\n", "missing frontmatter"},
		{"unterminated fence", "---\nname: go\ndescription: d\n", "split frontmatter"},
		{"invalid yaml", "---\nname: go\ndescription: d\nbad: [unclosed\n---\n\nbody\n", "decode frontmatter"},
		{"missing name", "---\ndescription: only a description\n---\n\nbody\n", "missing name"},
		{"missing description", "---\nname: go\n---\n\nbody\n", "missing description"},
		{"blank name", "---\nname: \"   \"\ndescription: d\n---\n\nbody\n", "missing name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := parseSkillMD(tc.in)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestContentSHA(t *testing.T) {
	t.Parallel()
	const body = "# Skill body\n\nsome content\n"

	t.Run("deterministic for identical input", func(t *testing.T) {
		t.Parallel()
		refs := map[string]string{"references/a.md": "A", "references/b.md": "B"}
		if contentSHA(body, refs) != contentSHA(body, refs) {
			t.Fatal("contentSHA is not stable across calls")
		}
	})

	t.Run("independent of reference map iteration order", func(t *testing.T) {
		t.Parallel()
		// Two maps with the same entries inserted in different order must hash
		// identically — the function sorts keys before hashing.
		one := map[string]string{"references/a.md": "A", "references/b.md": "B", "references/c.md": "C"}
		two := map[string]string{"references/c.md": "C", "references/b.md": "B", "references/a.md": "A"}
		if contentSHA(body, one) != contentSHA(body, two) {
			t.Fatal("contentSHA depends on map order — sort is broken")
		}
	})

	t.Run("nil and empty reference maps hash the same", func(t *testing.T) {
		t.Parallel()
		if contentSHA(body, nil) != contentSHA(body, map[string]string{}) {
			t.Fatal("nil vs empty refs must hash identically")
		}
	})

	t.Run("body change flips the hash", func(t *testing.T) {
		t.Parallel()
		if contentSHA(body, nil) == contentSHA(body+"x", nil) {
			t.Fatal("different body produced the same hash")
		}
	})

	t.Run("reference content change flips the hash", func(t *testing.T) {
		t.Parallel()
		a := contentSHA(body, map[string]string{"references/a.md": "A"})
		b := contentSHA(body, map[string]string{"references/a.md": "B"})
		if a == b {
			t.Fatal("different reference content produced the same hash")
		}
	})
}

// frontmatterKind derivation: metadata.aep.kind names the skill's kind; absent,
// empty, or unparseable metadata defaults to "org" (the platform-shipped,
// page-visible kind). docs/design/skills-unified-library-migration.md §3.2.
func TestFrontmatterKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		fm   string
		want string
	}{
		{"platform", "---\nname: s\ndescription: d.\nmetadata:\n  aep:\n    kind: platform\n---\nbody", "platform"},
		{"org explicit", "---\nname: s\ndescription: d.\nmetadata:\n  aep:\n    kind: org\n---\nbody", "org"},
		{"custom stamped", "---\nname: s\ndescription: d.\nmetadata:\n  aep:\n    kind: custom\n---\nbody", "custom"},
		{"imported stamped", "---\nname: s\ndescription: d.\nmetadata:\n  aep:\n    kind: imported\n---\nbody", "imported"},
		{"absent metadata", "---\nname: s\ndescription: d.\n---\nbody", "org"},
		{"empty kind", "---\nname: s\ndescription: d.\nmetadata:\n  aep:\n    kind: \"\"\n---\nbody", "org"},
		{"unknown kind", "---\nname: s\ndescription: d.\nmetadata:\n  aep:\n    kind: wat\n---\nbody", "org"},
		{"whitespace kind", "---\nname: s\ndescription: d.\nmetadata:\n  aep:\n    kind: '  platform  '\n---\nbody", "platform"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fm, _, err := parseSkillMD(tc.fm)
			if err != nil {
				t.Fatalf("parseSkillMD: %v", err)
			}
			if got := frontmatterKind(fm); got != tc.want {
				t.Fatalf("frontmatterKind = %q, want %q", got, tc.want)
			}
		})
	}
}
