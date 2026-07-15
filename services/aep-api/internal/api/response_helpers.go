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
	"bytes"
	"encoding/json"
	"net/http"
)

// ----------------------------------------------------------------------------
// The package's ONE JSON response writer. Buffered: an encode failure
// surfaces as an error BEFORE headers commit (the strict wrapper then serves
// its 500 envelope) instead of a half-written body.
// ----------------------------------------------------------------------------

func writeJSONBody(w http.ResponseWriter, status int, body any) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}

// writeJSON is writeJSONBody for raw-route callers with nowhere to send an
// error (dev routes, the envelope writers).
func writeJSON(w http.ResponseWriter, status int, body any) {
	_ = writeJSONBody(w, status, body)
}
