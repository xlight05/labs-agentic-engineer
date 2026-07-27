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

// Package validation owns the VALIDATION phase's platform side: it mints the
// single project-scoped validation issue (aep:validation) that the milestone
// run's validation cycle is dispatched at.
//
// The run supervisor mints it at DEPLOYED-GREEN, not at plan time: an issue
// that nothing can work until every component is deployed would otherwise sit
// in the run's working set and hold every cycle boundary open. It is prose
// carrying one label, and it deliberately does NOT carry the `aep` working-set
// label for the same reason — the cycle is dispatched at it by number.
//
// This feature does the ISSUE side only. Its runtime inputs — deployed endpoint
// URLs and test credentials — are never written into the (public) issue: the
// runner fetches endpoints from the secure validation-context endpoint and
// requests test credentials on demand (only when a criterion needs a login)
// from the sibling test-credentials endpoint (credentials.go). Test credentials
// are a v1 mock (admin/admin) until real user provisioning exists.
//
// Feature-edge allowlist: {gitrepo}. Design + criteria reads are consumer ports
// (ports.go) satisfied by adapters at the composition root, so this package
// imports neither the artifacts nor the files feature.
package validation
