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

// UNIT tier: the SkillImportService.Import ORCHESTRATION over the fake GitHub —
// the layer above the tarball extractor/validator, which skill_mutation_service_
// test.go already covers exhaustively (extractTarball shapes + parseAndValidate).
// Here: a happy import actually lands the files in the repo (round-trip through
// the fake), the tarball-name↔frontmatter-name mismatch, collision with an
// existing (built-in) name, warnings surfacing into the result, and the
// not-configured guard. Reuses makeTarGz/validSkillMD from the sibling test file.
package spec

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func skillMDNamed(name, extra string) string {
	return "---\nname: " + name + "\ndescription: An imported skill for testing.\n" + extra + "metadata:\n  aep.version: \"1\"\n---\n\n# " + name + "\n\nbody\n"
}

func TestImport_HappyLandsInRepo(t *testing.T) {
	t.Parallel()
	svc, _ := newTestStore(t)
	imp := NewSkillImportService(svc)
	ctx := context.Background()
	if _, err := svc.List(ctx, "org1"); err != nil { // seed built-ins first
		t.Fatalf("seed: %v", err)
	}

	// validSkillMD's frontmatter name is payments-pci-handling → topDir must match.
	tgz := makeTarGz(t, map[string]string{
		"payments-pci-handling/":                 "",
		"payments-pci-handling/SKILL.md":         validSkillMD,
		"payments-pci-handling/references/ex.md": "an example",
	})
	res, err := imp.Import(ctx, "org1", "tester", bytes.NewReader(tgz))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res == nil || res.Name != "payments-pci-handling" || res.Kind != "imported" {
		t.Fatalf("import result = %+v, want name=payments-pci-handling kind=imported", res)
	}
	if res.Warnings == nil {
		t.Fatal("Warnings must be non-nil ([] not null) so the console reads .length")
	}

	// The files actually landed: a subsequent resolve sees the imported skill
	// with its reference file, read back through the fake GitHub tree.
	got, err := svc.Resolve(ctx, "org1", "payments-pci-handling")
	if err != nil || got == nil {
		t.Fatalf("Resolve after import: %v / %v", got, err)
	}
	if got.Kind != "imported" {
		t.Fatalf("resolved kind = %q, want imported", got.Kind)
	}
	if got.References["references/ex.md"] != "an example" {
		t.Fatalf("reference did not land in the repo: %v", got.References)
	}
}

func TestImport_NameMismatch(t *testing.T) {
	t.Parallel()
	svc, _ := newTestStore(t)
	imp := NewSkillImportService(svc)
	ctx := context.Background()

	// topDir "wrong-dir" but validSkillMD's frontmatter name is different.
	tgz := makeTarGz(t, map[string]string{
		"wrong-dir/":         "",
		"wrong-dir/SKILL.md": validSkillMD,
	})
	_, err := imp.Import(ctx, "org1", "tester", bytes.NewReader(tgz))
	assertIssueCode(t, err, "NAME_MISMATCH")
}

func TestImport_CollisionWithBuiltin(t *testing.T) {
	t.Parallel()
	svc, _ := newTestStore(t)
	imp := NewSkillImportService(svc)
	ctx := context.Background()
	if _, err := svc.List(ctx, "org1"); err != nil { // seed so `go` builtin exists
		t.Fatalf("seed: %v", err)
	}

	tgz := makeTarGz(t, map[string]string{
		"go/":         "",
		"go/SKILL.md": skillMDNamed("go", ""),
	})
	_, err := imp.Import(ctx, "org1", "tester", bytes.NewReader(tgz))
	if !errors.Is(err, ErrSkillNameCollision) {
		t.Fatalf("import over builtin name: err = %v, want ErrSkillNameCollision", err)
	}
}

func TestImport_SurfacesWarnings(t *testing.T) {
	t.Parallel()
	svc, _ := newTestStore(t)
	imp := NewSkillImportService(svc)
	ctx := context.Background()

	md := skillMDNamed("warn-skill", "compatibility: python>=3.11\nallowed-tools:\n  - Bash\n")
	tgz := makeTarGz(t, map[string]string{
		"warn-skill/":         "",
		"warn-skill/SKILL.md": md,
	})
	res, err := imp.Import(ctx, "org1", "tester", bytes.NewReader(tgz))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Compatibility != "python>=3.11" {
		t.Fatalf("compatibility not preserved: %q", res.Compatibility)
	}
	var sawPython, sawTools bool
	for _, w := range res.Warnings {
		if contains(w, "python") {
			sawPython = true
		}
		if contains(w, "allowed_tools_ignored") {
			sawTools = true
		}
	}
	if !sawPython || !sawTools {
		t.Fatalf("expected python + allowed-tools warnings, got %v", res.Warnings)
	}
}

func TestImport_NotConfigured(t *testing.T) {
	t.Parallel()
	// A nil backing store makes the service unusable — Import must error, never
	// panic on the nil dereference.
	_, err := NewSkillImportService(nil).Import(context.Background(), "org1", "tester", bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected a not-configured error, got nil")
	}
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}
