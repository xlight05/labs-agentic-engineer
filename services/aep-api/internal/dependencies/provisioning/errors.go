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

package provisioning

import "errors"

// ErrExternalResourceInUse is returned by DeleteExternalResource when the
// resource still has consumers — the HTTP surface maps it to 409 Conflict.
// (The dependency-kind sentinels ErrDepNotFound / ErrDepWrongKind /
// ErrProvisionFailed / ErrNotRegistered live in dependencies/resources and are
// reused here.)
var ErrExternalResourceInUse = errors.New("provisioning: external resource is in use")

// ErrOrgServiceNotFound is returned by RequestAccess when the addressed
// org-service is not present in the org endpoint catalog (no provider component
// exports it under that name) — the HTTP surface maps it to 404.
var ErrOrgServiceNotFound = errors.New("provisioning: org-service provider not found")
