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

package arch

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The migration-shim ledger (docs/design/domain-oriented-architecture.md §19,
// "re-export shim convention" + the "stale bridges outliving their peer" risk).
//
// Some phases cannot move a package and fix its consumers in one reviewable PR:
// sourcecontrol's relocation alone touches ~75 files across 18 packages. The
// convention is to leave a temporary shim at the OLD path — a type-alias
// re-export, or a depth-equalising wrapper like internal/api's legacyShim — so
// consumers migrate on their own schedule.
//
// The failure mode is not the shim; it is the shim NOBODY REMOVES. A temporary
// alias that outlives its phase is indistinguishable from architecture, and by
// P9 nobody remembers which paths were meant to die. So a shim is not allowed to
// be a comment nobody greps: every one carries a machine-readable pragma naming
// the phase that retires it, and this test is the ledger.
//
// Declare one with, on its own line, in the file's doc comment:
//
//	aep:migration-shim retires=P9 reason=<short why>
//
// P9's exit gate is the mirror of this test: zero files carry the pragma.

const shimPragma = "aep:migration-shim"

// A well-formed pragma names a retiring phase and a reason. Both are required:
// the phase makes removal schedulable, the reason makes it reviewable.
var shimPragmaRe = regexp.MustCompile(`aep:migration-shim\s+retires=(P[0-9])\s+reason=(\S.*)`)

// shimDecl is one declared shim.
type shimDecl struct {
	file    string
	retires string
	reason  string
}

// findShims walks root for files carrying the pragma. It reads raw bytes rather
// than parsing: the pragma lives in a comment, which is exactly what a Go parser
// throws away by default.
func findShims(t *testing.T, root string) (decls []shimDecl, malformed []string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if e.IsDir() {
			if n := e.Name(); n == ".git" || n == "node_modules" || n == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		body, rerr := os.ReadFile(path) // #nosec G304 — test-time walk of the repo
		if rerr != nil {
			return nil
		}
		src := string(body)
		if !strings.Contains(src, shimPragma) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == filepath.Join("internal", "arch", "migration_shim_test.go") {
			return nil // this file documents the pragma; it is not a shim
		}
		m := shimPragmaRe.FindStringSubmatch(src)
		if m == nil {
			malformed = append(malformed, rel)
			return nil
		}
		decls = append(decls, shimDecl{file: rel, retires: m[1], reason: strings.TrimSpace(m[2])})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Slice(decls, func(i, j int) bool { return decls[i].file < decls[j].file })
	sort.Strings(malformed)
	return decls, malformed
}

// TestMigrationShimsAreLabelled asserts every shim declares the phase that
// retires it, and prints the live ledger so a reviewer can see at a glance what
// is still owed. An unlabelled shim is the orphan this convention exists to
// prevent.
func TestMigrationShimsAreLabelled(t *testing.T) {
	decls, malformed := findShims(t, filepath.Join("..", ".."))

	for _, f := range malformed {
		t.Errorf("%s mentions %s but the pragma is malformed — write exactly:\n"+
			"\t%s retires=P<N> reason=<short why>\n"+
			"A shim without a retiring phase is indistinguishable from architecture by P9.",
			f, shimPragma, shimPragma)
	}

	for _, d := range decls {
		t.Logf("migration shim: %-52s retires=%s  (%s)", d.file, d.retires, d.reason)
	}
}

// TestNoShimsSurvivePastTheirPhase is P9's gate, staged now so it cannot be
// forgotten: once the migration completes, currentPhase moves to "P9" and every
// shim must be gone. Kept honest during the migration by asserting only that no
// shim claims a phase that has already passed.
//
// currentPhase is bumped by the phase that lands; it is the one line a phase
// must not forget, and forgetting it is caught by the shims it should have
// retired.
const currentPhase = "P3"

func TestNoShimsSurvivePastTheirPhase(t *testing.T) {
	decls, _ := findShims(t, filepath.Join("..", ".."))
	for _, d := range decls {
		// <= , not < : currentPhase names the phase that has LANDED, so a shim
		// retiring in that phase is already overdue. With strict <, the terminal
		// case could never fire — at currentPhase=P9 a retires=P9 shim (which is
		// every shim P9 exists to delete) would pass, and the pragma P[0-9] regex
		// admits no phase beyond P9 to bump to. The gate would be dead code
		// exactly when it matters.
		if d.retires <= currentPhase {
			t.Errorf("%s was due to retire in %s but %s has landed — delete the shim, or move its "+
				"retires= if the plan genuinely changed (and say why in the PR)", d.file, d.retires, currentPhase)
		}
	}
}
