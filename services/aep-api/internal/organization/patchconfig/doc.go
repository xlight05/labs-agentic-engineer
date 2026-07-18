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

// Package patchconfig applies a partial update to the org settings singleton.
//
// Trigger: PATCH /config (update-config).
// In→out:  bound org + actor + three-state ConfigPatch → the updated projection.
// Ports:   organization.Service.
// Invariant: a SectionError carries the offending section, so the 4xx echoes a
// body.<section> pointer the console highlights; anything else collapses to 500.
package patchconfig
