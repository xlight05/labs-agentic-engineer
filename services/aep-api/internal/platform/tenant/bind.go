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

package tenant

import "context"

// boundOrgCtxKey carries the org handle the deny-by-default tenant gate bound
// for the current request. Only the gate writes it (api.tenantGate), and it
// derives the value SOLELY from the verified JWT claims — never client input —
// so a handler reading it holds the authorized tenant key by construction.
type boundOrgCtxKey struct{}

// WithBoundOrg returns a copy of ctx carrying the gate-bound org handle.
func WithBoundOrg(ctx context.Context, org string) context.Context {
	return context.WithValue(ctx, boundOrgCtxKey{}, org)
}

// BoundOrgFromContext returns the org handle the tenant gate bound for this
// request, or "" when the gate did not bind one (a carve-out operation, or
// LOG mode passing through a claimless request).
func BoundOrgFromContext(ctx context.Context) string {
	if org, ok := ctx.Value(boundOrgCtxKey{}).(string); ok {
		return org
	}
	return ""
}
