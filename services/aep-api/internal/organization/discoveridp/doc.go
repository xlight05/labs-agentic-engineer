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

// Package discoveridp probes an OIDC issuer's discovery document.
//
// Trigger: GET /config/idp/discover (discover-idp).
// In→out:  issuer query param → the canonical issuer + JWKS URL.
// Ports:   organization.Service.
// Invariant: a missing issuer is a 400; an upstream failure is a 502.
package discoveridp
