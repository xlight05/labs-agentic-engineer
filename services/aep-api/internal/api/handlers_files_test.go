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
	"net/http"
	"testing"

	"github.com/wso2/aep/aep-api/internal/feature/gitrepo"
)

// A wrapped Workspace.Mutate CAS-exhaustion (ErrRefNotFastForward) is a
// concurrent-write conflict, so mapFilesError must render it as a 409 — not
// the 500 the default arm would otherwise produce. (Ported from the files
// feature's Huma-era internal test at the contract-first cutover.)
func TestMapFilesError_CASExhaustionMapsTo409(t *testing.T) {
	err := mapFilesError(fmt.Errorf("apply: mutate: %w", gitrepo.ErrRefNotFastForward))
	var ae *apiError
	if !errors.As(err, &ae) {
		t.Fatalf("mapped error %T is not an *apiError", err)
	}
	if ae.Status != http.StatusConflict || ae.Code != CodeConflict {
		t.Fatalf("status/code = %d/%s, want 409/%s (CAS exhaustion is a retryable conflict)",
			ae.Status, ae.Code, CodeConflict)
	}
}
