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

// Package listorgs enumerates the caller's OC organizations.
//
// Trigger: GET /organizations (list-organizations) — the tenant-gate carve-out,
// carrying NO bound org: the console renders the org switcher from it before an
// org claim exists. The carve-out registration stays at the edge.
// In→out:  the verified user JWT → the OrganizationList.
// Ports:   organization.OrganizationService.
// Invariant: an OC 401 surfaces as 401; every other error collapses to 500.
package listorgs
