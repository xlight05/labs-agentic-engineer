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
	"context"
	"errors"
	"net/http"

	"github.com/wso2/aep/aep-api/internal/api/igen"
	"github.com/wso2/aep/aep-api/internal/feature/orgcreds"
	"github.com/wso2/aep/aep-api/internal/feature/validation"
	"github.com/wso2/aep/aep-api/internal/platform/auth"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// The internal service-to-service surface (/internal/v1), served CONTRACT-FIRST
// from packages/contracts/api/internal/v1 (generated strict server in
// internal/api/igen). It is NOT wrapped by the user-JWT middleware: every
// operation passes runnerAuthGate, which verifies the caller's BFF Task-JWT or
// publisher-cc bearer against the execution named in the path (the INT-6
// fence) and binds the verified org into the context. The spec is non-public —
// never gateway-advertised.
//
// RUNNER LOCKSTEP: the credentials-refresh response body is the orgcreds
// service's own struct (igen.RefreshResponse is an alias via x-go-type), so
// the wire bytes cannot drift from what the runner expects.

// InternalDeps carries the services + authorizer the internal S2S operations
// need. main.go (internal/app) fills it with real instances.
type InternalDeps struct {
	CredsRefresh orgcreds.CredentialsRefreshService
	// RunnerAuth verifies runner bearers (Task-JWT / publisher-cc) against the
	// path execution id. nil fails closed: every internal op answers 503.
	RunnerAuth *auth.RunnerAuthorizer
	// ValidationContext + ValidationCredentials back the two validation runner
	// callbacks (validation-context GET, test-credentials POST); a nil provider
	// answers 503 for its op.
	ValidationContext     validation.ContextProvider
	ValidationCredentials validation.CredentialRequester
}

// internalServer implements igen.StrictServerInterface.
type internalServer struct {
	deps InternalDeps
}

var _ igen.StrictServerInterface = (*internalServer)(nil)

// newInternalV1Handler assembles the internal edge: runner-auth gate → strict
// wrapper (envelope error writers) → generated router.
func newInternalV1Handler(deps InternalDeps) http.Handler {
	strict := igen.NewStrictHandlerWithOptions(
		&internalServer{deps: deps},
		[]igen.StrictMiddlewareFunc{runnerAuthGate(deps.RunnerAuth)},
		igen.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  writeRequestError,
			ResponseErrorHandlerFunc: writeResponseError,
		},
	)
	mux := http.NewServeMux()
	igen.HandlerWithOptions(strict, igen.StdHTTPServerOptions{
		BaseURL:          internalV1,
		BaseRouter:       mux,
		ErrorHandlerFunc: writeRequestError,
	})
	return mux
}

// runnerAuthGate is the internal surface's deny-by-default gate: every
// operation must present a bearer the authorizer accepts for the execution id
// named in the request, and the verified org is bound into the context. There
// are deliberately NO carve-outs here. An operation whose request shape the
// gate does not know is denied outright — adding an internal op means teaching
// this gate its execution key first.
func runnerAuthGate(authorizer *auth.RunnerAuthorizer) igen.StrictMiddlewareFunc {
	return func(f igen.StrictHandlerFunc, operationID string) igen.StrictHandlerFunc {
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			if authorizer == nil {
				return nil, errServiceUnavailable("runner auth not configured")
			}
			var executionID string
			switch req := request.(type) {
			case igen.RunnerRefreshCredentialsRequestObject:
				executionID = req.ExecutionID
			case igen.RunnerValidationContextRequestObject:
				executionID = req.ExecutionID
			case igen.RunnerValidationCredentialsRequestObject:
				executionID = req.ExecutionID
			default:
				return nil, errUnauthorized("unauthenticated internal operation: " + operationID)
			}
			caller, err := authorizer.Authorize(ctx, r.Header.Get("Authorization"), executionID)
			if err != nil {
				return nil, mapRunnerAuthError(err)
			}
			return f(tenant.WithBoundOrg(ctx, string(caller.Org)), w, r, request)
		}
	}
}

// mapRunnerAuthError translates the authorizer's neutral auth.HTTPError onto
// the envelope; anything unrecognized fails closed as a 401.
func mapRunnerAuthError(err error) error {
	var ae *auth.HTTPError
	if errors.As(err, &ae) {
		switch ae.Status {
		case http.StatusForbidden:
			return errForbidden(ae.Message)
		default:
			return errUnauthorized(ae.Message)
		}
	}
	return errUnauthorized("invalid bearer")
}

func (s *internalServer) RunnerRefreshCredentials(ctx context.Context, request igen.RunnerRefreshCredentialsRequestObject) (igen.RunnerRefreshCredentialsResponseObject, error) {
	if s.deps.CredsRefresh == nil {
		return nil, errServiceUnavailable("credentials refresh not configured")
	}
	org := tenant.BoundOrgFromContext(ctx)
	resp, err := s.deps.CredsRefresh.Refresh(ctx, request.ExecutionID, org)
	if err != nil {
		return nil, errInternal("failed to refresh credentials")
	}
	return igen.RunnerRefreshCredentials200JSONResponse(*resp), nil
}

func (s *internalServer) RunnerValidationContext(ctx context.Context, request igen.RunnerValidationContextRequestObject) (igen.RunnerValidationContextResponseObject, error) {
	if s.deps.ValidationContext == nil {
		return nil, errServiceUnavailable("validation context not configured")
	}
	org := tenant.BoundOrgFromContext(ctx)
	resp, err := s.deps.ValidationContext.ValidationContext(ctx, request.ExecutionID, org)
	if err != nil {
		if errors.Is(err, validation.ErrExecutionNotFound) {
			return nil, errNotFound("no validation task for this execution")
		}
		return nil, errInternal("failed to resolve validation context")
	}
	return igen.RunnerValidationContext200JSONResponse(*resp), nil
}

func (s *internalServer) RunnerValidationCredentials(ctx context.Context, request igen.RunnerValidationCredentialsRequestObject) (igen.RunnerValidationCredentialsResponseObject, error) {
	if s.deps.ValidationCredentials == nil {
		return nil, errServiceUnavailable("validation credentials not configured")
	}
	org := tenant.BoundOrgFromContext(ctx)
	var req validation.CredentialRequest
	if request.Body != nil {
		req = validation.CredentialRequest{
			Role:     request.Body.Role,
			Purpose:  request.Body.Purpose,
			Username: request.Body.Username,
		}
	}
	resp, err := s.deps.ValidationCredentials.RequestCredentials(ctx, request.ExecutionID, org, req)
	if err != nil {
		if errors.Is(err, validation.ErrExecutionNotFound) {
			return nil, errNotFound("no validation task for this execution")
		}
		return nil, errInternal("failed to request test credentials")
	}
	return igen.RunnerValidationCredentials200JSONResponse(*resp), nil
}
