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
	"log/slog"
	"net/http"

	"github.com/wso2/aep/aep-api/internal/gen"
)

// The flat error envelope every non-2xx response carries (contract schema
// Error): {code, message, details?}. code is a stable machine-readable slug;
// details appears only on field-level validation errors. This replaced the
// RFC 9457 problem-details dialect at the contract-first cutover.
const (
	CodeValidationFailed   = "validation_failed"
	CodeBadRequest         = "bad_request"
	CodeUnauthorized       = "unauthorized"
	CodeForbidden          = "forbidden"
	CodeNotFound           = "not_found"
	CodeConflict           = "conflict"
	CodeInternal           = "internal_error"
	CodeBadGateway         = "bad_gateway"
	CodeServiceUnavailable = "service_unavailable"
)

// apiError is the transport error the strict handlers and middleware return;
// the central writers below turn it into the envelope. It replaces the Huma
// error constructors (humakit.ErrorFromStatus, huma.ErrorNNN*).
type apiError struct {
	Status  int
	Code    string
	Message string
	Details []gen.ErrorDetail
}

func (e *apiError) Error() string { return e.Message }

func errBadRequest(msg string) error {
	return &apiError{http.StatusBadRequest, CodeBadRequest, msg, nil}
}
func errUnauthorized(msg string) error {
	return &apiError{http.StatusUnauthorized, CodeUnauthorized, msg, nil}
}
func errForbidden(msg string) error { return &apiError{http.StatusForbidden, CodeForbidden, msg, nil} }
func errNotFound(msg string) error  { return &apiError{http.StatusNotFound, CodeNotFound, msg, nil} }
func errConflict(msg string) error  { return &apiError{http.StatusConflict, CodeConflict, msg, nil} }
func errInternal(msg string) error {
	return &apiError{http.StatusInternalServerError, CodeInternal, msg, nil}
}
func errBadGateway(msg string) error {
	return &apiError{http.StatusBadGateway, CodeBadGateway, msg, nil}
}
func errServiceUnavailable(msg string) error {
	return &apiError{http.StatusServiceUnavailable, CodeServiceUnavailable, msg, nil}
}

// errFromStatus maps a sentinel-classified HTTP status (e.g. an OpenChoreo
// pass-through classified by ocerr.Status) onto the envelope, mirroring the
// retired humakit.ErrorFromStatus ladder.
func errFromStatus(status int, msg string) error {
	switch status {
	case http.StatusBadRequest:
		return errBadRequest(msg)
	case http.StatusUnauthorized:
		return errUnauthorized(msg)
	case http.StatusForbidden:
		return errForbidden(msg)
	case http.StatusNotFound:
		return errNotFound(msg)
	case http.StatusConflict:
		return errConflict(msg)
	default:
		return errInternal(msg)
	}
}

// writeErrorEnvelope writes the flat envelope. It is the single place a
// non-2xx body is produced on the public edge.
func writeErrorEnvelope(w http.ResponseWriter, status int, code, msg string, details []gen.ErrorDetail) {
	body := gen.Error{Code: code, Message: msg, Details: details}
	writeJSON(w, status, body)
}

// writeResponseError is the strict handler's ResponseErrorHandlerFunc: a typed
// *apiError writes its own status/code; anything else is an unclassified
// failure and becomes an opaque 500 (never leaking the internal cause).
func writeResponseError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeErrorEnvelope(w, ae.Status, ae.Code, ae.Message, ae.Details)
		return
	}
	slog.ErrorContext(r.Context(), "unclassified handler error", "path", r.URL.Path, "err", err)
	writeErrorEnvelope(w, http.StatusInternalServerError, CodeInternal, "internal error", nil)
}

// writeRequestError is the strict handler's RequestErrorHandlerFunc and the
// generated router's ErrorHandlerFunc: request-shape problems the generated
// code detects before the handler runs (undecodable JSON body, unparsable
// path/query params) — always a 400 in the new dialect.
func writeRequestError(w http.ResponseWriter, _ *http.Request, err error) {
	writeErrorEnvelope(w, http.StatusBadRequest, CodeValidationFailed, err.Error(), nil)
}
