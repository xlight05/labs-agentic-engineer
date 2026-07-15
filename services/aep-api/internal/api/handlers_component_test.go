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
	"net/http"
	"strings"
	"testing"

	"github.com/wso2/aep/aep-api/internal/clients/openchoreo"
)

// TestMapComponentError pins the mapper directly: every OpenChoreo sentinel
// that componentService passes through is translated to its HTTP status via
// the shared ocerr classifier, and anything that is not an OC sentinel
// collapses to a fixed-message 500 that never leaks the internal cause.
// (Moved from the component feature with the handler at the contract-first
// cutover — the mapper lives beside the strict handler now.)
func TestMapComponentError(t *testing.T) {
	t.Parallel()

	ocCases := []struct {
		err  error
		want int
	}{
		{openchoreo.ErrUnauthorized, http.StatusUnauthorized},
		{openchoreo.ErrForbidden, http.StatusForbidden},
		{openchoreo.ErrNotFound, http.StatusNotFound},
		{openchoreo.ErrConflict, http.StatusConflict},
		{openchoreo.ErrBadRequest, http.StatusBadRequest},
	}
	for _, tc := range ocCases {
		err := mapComponentError(tc.err, "failed to do thing")
		if got := statusOf(t, err); got != tc.want {
			t.Fatalf("mapComponentError(%v) → %v, want status %d", tc.err, err, tc.want)
		}
	}

	// Anything that is not an OC sentinel → opaque 500 carrying the supplied
	// internal message, never the raw error.
	err := mapComponentError(errors.New("pg: connection refused"), "failed to list components")
	if got := statusOf(t, err); got != http.StatusInternalServerError {
		t.Fatalf("opaque error must map to 500, got %v", err)
	}
	if strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("500 must not leak internals: %v", err)
	}
	if !strings.Contains(err.Error(), "failed to list components") {
		t.Fatalf("500 must carry the supplied internal message: %v", err)
	}
}
