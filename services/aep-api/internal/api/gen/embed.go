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

package gen

import "embed"

// ContractFS embeds the vendored copy of the committed public contract
// (packages/contracts/api/v1 — the source of truth). `make gen-api` refreshes
// the vendor alongside the generated code; go:embed cannot reach across the
// module boundary, same posture as skills/embedded. Consumed by the request
// validator middleware and the contract arch-guard tests; deliberately NOT
// served over HTTP — the contract is a build-time artifact.
//
//go:embed contract/openapi.yaml contract/components.yaml
var ContractFS embed.FS
