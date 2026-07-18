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

package spec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestValidateOpenAPI_RejectsYAMLAliasBomb locks the transitive-dependency
// guarantee that yaml.v3 (>= v3.0.4) rejects alias-expansion ("billion laughs")
// bombs via its alias budget rather than OOM/hang — a platform-touching SSRF-
// adjacent guard (the fetched/pasted spec is attacker-controlled). The doc
// below has 8 levels each referencing the previous 10×, i.e. ~10^8 expansions;
// ValidateOpenAPI parses via yaml.Unmarshal first, so the budget guard must fire
// there. A future dependency downgrade that drops the budget would resurface
// the OOM and trip this test (via the hang timeout).
func TestValidateOpenAPI_RejectsYAMLAliasBomb(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("a: &a \"lol\"\n")
	prev := "a"
	for _, name := range []string{"b", "c", "d", "e", "f", "g", "h", "i"} {
		sb.WriteString(name + ": &" + name + " [")
		for j := 0; j < 10; j++ {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("*" + prev)
		}
		sb.WriteString("]\n")
		prev = name
	}
	bomb := []byte(sb.String())

	done := make(chan error, 1)
	go func() {
		_, err := ValidateOpenAPI(bomb)
		done <- err
	}()
	select {
	case err := <-done:
		// Either the alias budget rejects it, or it parses to a non-OpenAPI doc —
		// both are an error. The point is it returns quickly, not OOM/hang.
		if err == nil {
			t.Fatal("expected an error for a YAML alias bomb, got nil")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("ValidateOpenAPI did not return on a YAML alias bomb — alias budget not enforced (dep downgrade?)")
	}
}

const sampleSpec = `openapi: 3.0.3
info: { title: Weather, version: "1.0" }
paths:
  /weather:
    get: { responses: { "200": { description: ok } } }
  /forecast:
    get: { responses: { "200": { description: ok } } }
    post: { responses: { "201": { description: created } } }
`

// TestStoreConsumedSpec_ValidatesAndReturnsPath asserts the committed-truth
// StoreConsumedSpec: a valid spec validates + normalizes and returns the
// component-relative path plus the normalized blob the caller commits.
func TestStoreConsumedSpec_ValidatesAndReturnsPath(t *testing.T) {
	store := NewArtifactStore(nil) // no artifact service is touched — validate/normalize only
	got, normalized, err := store.StoreConsumedSpec(context.Background(), "acme", "web", "consumer", "stripe", []byte(sampleSpec))
	if err != nil {
		t.Fatalf("StoreConsumedSpec: unexpected error: %v", err)
	}
	if want := ConsumedSpecPath("stripe"); got != want {
		t.Fatalf("specPath = %q, want %q", got, want)
	}
	if normalized == "" {
		t.Fatal("normalized blob is empty — caller has nothing to commit")
	}
}

// TestStoreConsumedSpec_RejectsBadInput asserts the validation-class errors are
// %w-wrapped with ErrInvalidSpecContent (client/400): depName path traversal and
// a non-OpenAPI document.
func TestStoreConsumedSpec_RejectsBadInput(t *testing.T) {
	store := NewArtifactStore(nil)

	if _, _, err := store.StoreConsumedSpec(context.Background(), "acme", "web", "consumer", "../escape", []byte(sampleSpec)); !errors.Is(err, ErrInvalidSpecContent) {
		t.Fatalf("path-traversal depName: want ErrInvalidSpecContent, got %v", err)
	}
	if _, _, err := store.StoreConsumedSpec(context.Background(), "acme", "web", "consumer", "stripe", []byte("foo: bar")); !errors.Is(err, ErrInvalidSpecContent) {
		t.Fatalf("invalid OpenAPI: want ErrInvalidSpecContent, got %v", err)
	}
}

func TestValidateOpenAPICountsOperations(t *testing.T) {
	n, err := ValidateOpenAPI([]byte(sampleSpec))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if n != 3 {
		t.Fatalf("want 3 operations, got %d", n)
	}
}

func TestValidateOpenAPIRejectsNonOpenAPI(t *testing.T) {
	if _, err := ValidateOpenAPI([]byte("foo: bar")); err == nil {
		t.Fatal("expected error for non-openapi doc")
	}
}

func TestValidateOpenAPIRejectsSwagger2(t *testing.T) {
	swagger2 := `swagger: "2.0"
info: { title: Old, version: "1.0" }
paths:
  /ping:
    get: { responses: { "200": { description: ok } } }
`
	if _, err := ValidateOpenAPI([]byte(swagger2)); err == nil {
		t.Fatal("expected error for swagger 2.0 doc")
	}
}

func TestValidateOpenAPIRejectsNoPaths(t *testing.T) {
	noPaths := `openapi: "3.0.3"
info: { title: Empty, version: "1.0" }
`
	if _, err := ValidateOpenAPI([]byte(noPaths)); err == nil {
		t.Fatal("expected error for OpenAPI doc with no paths")
	}
}
