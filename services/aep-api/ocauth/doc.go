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

// Package ocauth is the public OpenChoreo auth seam for aep-api.
//
// Overlay modules and OSS main implement RequestAuthStrategy and supply
// AuthProvider without importing internal/ clients or platform packages.
// Context helpers (IsServiceIdentity, GetAuthToken, ClaimsFromContext,
// ResolveOuHandle) wrap the inbound auth context for PAS strategies and
// impersonation resolvers.
//
// This package must not import openchoreo or app (no import cycle).
package ocauth
