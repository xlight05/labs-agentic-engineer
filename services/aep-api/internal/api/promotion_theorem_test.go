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

package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The first of the two nets guarding the edge: the legacyShim
// (docs/design/domain-oriented-architecture.md §19.1).
//
// The migration's headline risk is NOT a broken build — it is a GREEN one. Go
// promotes the method at the SHALLOWEST depth and says nothing about the loser,
// so an op that was added to its domain but never cut from legacy can compile
// green and keep serving the stale legacy body while the new slice sits dead.
// componenttest cannot see it: both bodies return the same shape.
//
// The shim removes the depth asymmetry that makes that possible. These two tests
// hold it in place from opposite sides:
//
//   - TestLegacyIsShimmed pins the SHAPE of the real apiServer (someone
//     "simplifying" the shim away is the way this protection dies);
//   - TestPromotionTheorem pins the LANGUAGE RULE the shape relies on, by
//     compiling all four states and asserting which build and what they serve.

// TestLegacyIsShimmed asserts legacy is embedded THROUGH the shim, never
// directly. Embedding *legacyHandlers straight into apiServer would put its
// methods at depth-1 — beating every domain's depth-2 method, silently.
func TestLegacyIsShimmed(t *testing.T) {
	srv := reflect.TypeOf(apiServer{})

	f, ok := srv.FieldByName("legacyShim")
	if !ok || !f.Anonymous {
		t.Fatal("apiServer must embed legacyShim — it is what puts legacy's methods at the " +
			"same depth as a domain slice's, making a forgotten cut a compile error")
	}
	if f.Type != reflect.TypeOf(legacyShim{}) {
		t.Fatalf("apiServer.legacyShim is %v, want the legacyShim wrapper", f.Type)
	}

	// The shim must actually wrap legacy (an empty shim would silently defeat it).
	shim := reflect.TypeOf(legacyShim{})
	if shim.NumField() != 1 || shim.Field(0).Type != reflect.TypeOf(&legacyHandlers{}) {
		t.Fatalf("legacyShim must embed exactly *legacyHandlers, got %v fields", shim.NumField())
	}

	// And legacy must not ALSO appear at depth-1, which would win outright.
	for i := 0; i < srv.NumField(); i++ {
		ft := srv.Field(i).Type
		if ft == reflect.TypeOf(&legacyHandlers{}) || ft == reflect.TypeOf(legacyHandlers{}) {
			t.Fatal("apiServer embeds *legacyHandlers DIRECTLY — that restores the depth-1 " +
				"shadowing hazard the shim exists to remove; embed it via legacyShim")
		}
	}
}

// promotionFixture is one state of the §19.1 matrix, expressed as a whole
// program so the COMPILER is the thing under test.
type promotionFixture struct {
	name         string
	legacyHasOp  bool // legacy still declares Op (the cut was forgotten)
	domainHasOp  bool // the domain slice implements Op (the migration happened)
	shimmed      bool // legacy is wrapped, equalising depth
	wantBuildOK  bool
	wantBuildErr string // substring the compile error must contain
	wantServes   string // what the binary prints when it builds
}

// TestPromotionTheorem compiles the four states of §19.1's table and asserts the
// outcome of each. This is the proof that the shim's protection is real and that
// the hazard it prevents is real — the row that matters is
// "double coverage, unshimmed": it BUILDS, and it serves the stale body.
//
// Pinning the language rule matters because the entire migration leans on it: if
// a future toolchain changed promotion or ambiguity, this fails loudly here
// rather than silently serving stale handlers in production.
func TestPromotionTheorem(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles four fixture programs; skipped in the fast lane")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	fixtures := []promotionFixture{
		{
			// THE HAZARD, and the whole reason the shim exists. Legacy at depth-1
			// shadows the domain's depth-2 method: green build, stale body served.
			name:        "double_coverage_unshimmed",
			legacyHasOp: true, domainHasOp: true, shimmed: false,
			wantBuildOK: true, wantServes: "STALE legacy body",
		},
		{
			// THE FIX. Same mistake, but the depths now tie -> the build fails.
			name:        "double_coverage_shimmed",
			legacyHasOp: true, domainHasOp: true, shimmed: true,
			wantBuildOK: false, wantBuildErr: "ambiguous",
		},
		{
			// The correct cut serves the domain through the shim.
			name:        "correct_cut_shimmed",
			legacyHasOp: false, domainHasOp: true, shimmed: true,
			wantBuildOK: true, wantServes: "fresh domain body",
		},
		{
			// Under-coverage: cut from legacy, never implemented. Nothing supplies
			// Op, so the interface assertion fails. Caught with or without the shim
			// — this is the case the compiler was ALREADY good at.
			name:        "under_coverage_shimmed",
			legacyHasOp: false, domainHasOp: false, shimmed: true,
			wantBuildOK: false, wantBuildErr: "missing method Op",
		},
		{
			name:        "under_coverage_unshimmed",
			legacyHasOp: false, domainHasOp: false, shimmed: false,
			wantBuildOK: false, wantBuildErr: "missing method Op",
		},
	}

	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, "go.mod", "module promotionproof\n\ngo 1.24\n")
			write(t, dir, "main.go", fixtureProgram(f))

			out, err := runGo(dir, "build", "./...")
			if !f.wantBuildOK {
				if err == nil {
					t.Fatalf("fixture BUILT, want a compile error containing %q — if this state "+
						"compiles, the net it stands for protects nothing", f.wantBuildErr)
				}
				if !strings.Contains(out, f.wantBuildErr) {
					t.Fatalf("build failed, but not for the expected reason (want %q): %s", f.wantBuildErr, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("fixture failed to build: %s", out)
			}
			got, err := runGo(dir, "run", ".")
			if err != nil {
				t.Fatalf("fixture failed to run: %s", got)
			}
			if !strings.Contains(got, f.wantServes) {
				t.Fatalf("fixture served %q, want %q", strings.TrimSpace(got), f.wantServes)
			}
		})
	}
}

// fixtureProgram renders one state of the matrix. The shape mirrors the real
// edge exactly: the op is declared on a SLICE handler, promoted through the
// domain's aggregator (depth-2), and legacy is embedded either directly
// (depth-1) or through the shim (depth-2).
func fixtureProgram(f promotionFixture) string {
	var b strings.Builder
	b.WriteString(`package main

import "fmt"

type Iface interface{ Op() string }

type legacyHandlers struct{}
`)
	if f.legacyHasOp {
		b.WriteString("\nfunc (l *legacyHandlers) Op() string { return \"STALE legacy body\" }\n")
	}
	b.WriteString(`
type legacyShim struct{ *legacyHandlers }

type sliceHandler struct{}
`)
	if f.domainHasOp {
		// Declared on the SLICE and promoted through the aggregator — depth-2,
		// exactly like a real migrated op.
		b.WriteString("\nfunc (s *sliceHandler) Op() string { return \"fresh domain body\" }\n")
	}
	b.WriteString(`
type domainHandlers struct{ *sliceHandler }

type apiServer struct {
`)
	if f.shimmed {
		b.WriteString("\tlegacyShim\n")
	} else {
		b.WriteString("\t*legacyHandlers\n")
	}
	b.WriteString(`	*domainHandlers
}

var _ Iface = (*apiServer)(nil)

func main() {
	s := &apiServer{`)
	if f.shimmed {
		b.WriteString("legacyShim{&legacyHandlers{}}")
	} else {
		b.WriteString("&legacyHandlers{}")
	}
	b.WriteString(`, &domainHandlers{&sliceHandler{}}}
	fmt.Println(s.Op())
}
`)
	return b.String()
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// runGo runs the toolchain in dir, isolated from the repo's module so the
// fixture compiles as its own tiny program.
func runGo(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	return string(out), err
}
