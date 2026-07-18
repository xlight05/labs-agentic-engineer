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

package execution

import (
	"github.com/wso2/aep/aep-api/internal/contracts/taskmeta"
	"github.com/wso2/aep/aep-api/internal/delivery"
)

// Registry maps an executor-class label to its Executor (§3, §11). Extending
// the platform to a new class is a registry entry plus a runner — no schema or
// API change. In v1 only the coding class is wired; the ops class is present as
// a slot so an aep:execute on an ops Task produces a clear "no executor for
// class ops" attention flag rather than a silent drop (§11).
type Registry struct {
	byClass map[taskmeta.ExecutorClass]delivery.Executor
}

// NewRegistry builds an empty registry. The composition root registers the
// coding executor (feature/codingagent) into it.
func NewRegistry() *Registry {
	return &Registry{byClass: map[taskmeta.ExecutorClass]delivery.Executor{}}
}

// Register wires an executor for a class. A later call for the same class
// replaces the earlier one (last wins) — the composition root registers each
// class exactly once.
func (r *Registry) Register(class taskmeta.ExecutorClass, e delivery.Executor) {
	r.byClass[class] = e
}

// Lookup returns the executor for a class and whether one is registered. An
// unregistered class (e.g. ops before its executor ships) returns (nil, false),
// which the funnel surfaces as aep:attention (§11).
func (r *Registry) Lookup(class taskmeta.ExecutorClass) (delivery.Executor, bool) {
	e, ok := r.byClass[class]
	return e, ok
}
