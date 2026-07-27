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

package delivery_test

import (
	"reflect"
	"testing"

	"github.com/wso2/aep/aep-api/internal/delivery"
)

// TestDiffComponents pins the merged-PR path diff — the ONE rule the event
// plane's fan-out and the run supervisor's "have this cycle's builds all
// reported?" read must agree on, which is why it lives in the root rather than
// in either of them.
func TestDiffComponents(t *testing.T) {
	paths := map[string]string{
		"order-service": "services/order",
		"web":           "apps/web",
		"monolith":      "", // builds from the repo root
	}
	t.Run("prefix match, stable order, unmatched reported", func(t *testing.T) {
		got := delivery.DiffComponents([]string{"services/order/main.go", "docs/adr.md"}, map[string]string{
			"order-service": "services/order",
			"web":           "apps/web",
		})
		if !reflect.DeepEqual(got.Components, []string{"order-service"}) {
			t.Fatalf("components = %v, want just order-service", got.Components)
		}
		if !reflect.DeepEqual(got.Unmatched, []string{"docs/adr.md"}) {
			t.Fatalf("unmatched = %v, want the doc change", got.Unmatched)
		}
	})
	t.Run("an empty App Path claims everything", func(t *testing.T) {
		got := delivery.DiffComponents([]string{"docs/adr.md"}, paths)
		if !reflect.DeepEqual(got.Components, []string{"monolith"}) {
			t.Fatalf("components = %v, want monolith", got.Components)
		}
		if len(got.Unmatched) != 0 {
			t.Fatalf("a repo-root component leaves nothing unmatched, got %v", got.Unmatched)
		}
	})
	t.Run("a path prefix is not a directory prefix", func(t *testing.T) {
		got := delivery.DiffComponents([]string{"services/order-legacy/main.go"}, paths)
		if len(got.Components) != 1 || got.Components[0] != "monolith" {
			t.Fatalf("services/order must not match services/order-legacy, got %v", got.Components)
		}
	})
	t.Run("no files, nothing to build", func(t *testing.T) {
		if got := delivery.DiffComponents(nil, paths); len(got.Components) != 0 {
			t.Fatalf("components = %v, want none", got.Components)
		}
	})
}
