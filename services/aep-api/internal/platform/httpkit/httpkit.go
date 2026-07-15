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

// Package httpkit holds the shared HTTP response writers (§4.0): the
// WriteSuccessResponse/WriteErrorResponse base writers plus the Write40x
// helpers the central tenant gate uses, and the UUID path-param validator.
package httpkit

import (
	"net/http"

	"github.com/wso2/aep/aep-api/internal/platform/validate"
)

// APIV1 is the client-facing edge's version prefix, declared ONCE. The
// contract-first router mounts every public operation under it (it is the
// committed contract's `servers` base URL), and raw routes that need the
// absolute path (e.g. the GitHub OAuth redirect_uri) build on it directly.
// A v2 is a one-edit change here. The internal S2S surface uses a separate
// /internal/v1 root (api.internalV1).
const APIV1 = "/api/v1"

// Write400 writes a 400 Bad Request with the given client-facing message.
func Write400(w http.ResponseWriter, msg string) {
	WriteErrorResponse(w, http.StatusBadRequest, msg)
}

// Write401 writes a 401 Unauthorized.
func Write401(w http.ResponseWriter) {
	WriteErrorResponse(w, http.StatusUnauthorized, "authentication required")
}

// Write404 writes a 404 Not Found. The gate uses the SAME body for wrong-org
// and no-such-org so cross-org existence is never leaked (§6.1a).
func Write404(w http.ResponseWriter, msg string) {
	WriteErrorResponse(w, http.StatusNotFound, msg)
}

// Write500 writes a 500 Internal Server Error with a generic message.
func Write500(w http.ResponseWriter) {
	WriteErrorResponse(w, http.StatusInternalServerError, "internal error")
}

// RequireUUID validates a canonical UUID path param. On failure it writes a
// 400 to w (with paramName surfaced in the message) and returns false;
// otherwise returns true.
func RequireUUID(w http.ResponseWriter, paramName, v string) bool {
	if err := validate.UUID(v); err != nil {
		Write400(w, paramName+": "+err.Error())
		return false
	}
	return true
}
