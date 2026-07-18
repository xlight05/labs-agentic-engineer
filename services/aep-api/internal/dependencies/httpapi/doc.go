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

// Package httpapi embeds the dependencies slices into the one type the edge
// embeds, and assembles the domain from its Deps.
//
// It declares NO methods — a method here sits at depth-1 and silently shadows
// its slice (TestAggregatorsDeclareNoMethods). Deps lives HERE, not in the
// dependencies root, because the domain is kernel-root: its services live in
// sub-package slices and the root may not import its own sub-packages
// (root ⊥ slice); the httpapi aggregator is the one package allowed to import
// its sibling slices, which is its entire job.
// [Why →] docs/design/domain-oriented-architecture.md#8-structural-convention-the-composition-root
package httpapi
