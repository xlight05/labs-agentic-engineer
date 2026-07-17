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

package migrate

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// exemptFromRegistration are the exported Run* functions intentionally NOT
// wired into run_all.go's ordered step slice: RunAll is the driver itself, and
// RunBootstrapGrants is run by the caller (main / dbtest) before migrating, not
// as an ordered step — see run_all.go's package doc.
var exemptFromRegistration = map[string]bool{
	"RunAll":             true,
	"RunBootstrapGrants": true,
}

var runFuncRe = regexp.MustCompile(`(?m)^func (Run[A-Za-z0-9_]*)\(`)

// TestEveryStepIsRegistered pins the invariant that every exported Run*
// migration defined in this package is referenced by run_all.go. An
// unregistered step ships a migration a live boot silently skips — a failure
// mode that has shipped before. This is a deliberately cheap source-convention
// check (does run_all.go name the function?), not an AST census: it fails the
// moment a new Run* is added without wiring it into the ordered slice.
func TestEveryStepIsRegistered(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	registry, err := os.ReadFile("run_all.go")
	if err != nil {
		t.Fatalf("read run_all.go: %v", err)
	}
	registrySrc := string(registry)

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") || name == "run_all.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, m := range runFuncRe.FindAllStringSubmatch(string(src), -1) {
			fn := m[1]
			if exemptFromRegistration[fn] {
				continue
			}
			if !regexp.MustCompile(`\b` + regexp.QuoteMeta(fn) + `\b`).MatchString(registrySrc) {
				t.Errorf("migration %s (%s) is not registered in run_all.go — add it to the ordered step slice (dbStep/ctxStep), or add it to exemptFromRegistration with a reason", fn, name)
			}
		}
	}
}
