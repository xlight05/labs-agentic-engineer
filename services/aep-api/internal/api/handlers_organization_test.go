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
	"errors"
	"fmt"
	"testing"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
)

// statusOf casts a transport error to its wire status, failing the test if the
// mapper returned something that is not an *apiError.
func statusOf(t *testing.T, err error) int {
	t.Helper()
	var ae *apiError
	if !errors.As(err, &ae) {
		t.Fatalf("expected an *apiError, got %T (%v)", err, err)
	}
	return ae.Status
}

// TestMapOrganizationError pins the handler-facing status mapping: only an OC
// 401 becomes a 401 (so an upstream auth failure isn't masked); every other OC
// sentinel and any opaque error are an opaque 500 whose body carries a fixed
// message (never the underlying error, so internals can't leak). The 401
// distinction is the deliberate coarseness of the read-only List contract.
// (Moved from the organization feature with the handler at the contract-first
// cutover — the mapper lives beside the strict handler now.)
func TestMapOrganizationError(t *testing.T) {
	t.Parallel()

	if got := statusOf(t, mapOrganizationError(openchoreo.ErrUnauthorized)); got != 401 {
		t.Fatalf("oc unauthorized → %d, want 401", got)
	}
	// Wrapped unauthorized still maps to 401 (errors.Is chain).
	if got := statusOf(t, mapOrganizationError(fmt.Errorf("list: %w", openchoreo.ErrUnauthorized))); got != 401 {
		t.Fatalf("wrapped oc unauthorized → %d, want 401", got)
	}
	// Every other OC sentinel and any opaque error collapse to an opaque 500.
	for _, err := range []error{openchoreo.ErrForbidden, openchoreo.ErrNotFound, errors.New("pg down")} {
		e := mapOrganizationError(err)
		if got := statusOf(t, e); got != 500 {
			t.Fatalf("%v → %d, want 500", err, got)
		}
		if e.Error() != "failed to list organizations" {
			t.Fatalf("500 must carry the fixed message, got %q", e.Error())
		}
	}
}
