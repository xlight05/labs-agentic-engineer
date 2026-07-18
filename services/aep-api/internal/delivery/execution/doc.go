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

// Package execution is the platform-owned half of the Task/Execution split
// (docs/design/tasks-github-native.md §1, §10.1). A Task is a GitHub issue,
// owned by GitHub (feature/task); an Execution is one platform attempt at one
// kind of work for that Task, owned by Postgres. This package holds THE single
// dispatch path (the funnel), the executor registry, the reconciliation sweep,
// the pull_request webhook handlers that end coding attempts and spawn builds,
// the unified progress endpoint, and the runner-scoped skills read.
//
// The §1 split is a package boundary: this package never imports feature/task
// and vice-versa (arch-locked). The two halves speak the pure taskmeta encoding
// and the executions rows (a shared kernel: models/ + repositories/), nothing
// else. There is exactly one door into dispatch — the funnel — so gates cannot
// be bypassed (§5).
//
// As a delivery-domain sub-package it imports only the delivery root for the
// executions write-API kernel it builds on — the Executor port, DispatchRequest,
// TaskFacts, the TaskStreamHub, and the Signaler/signal vocabulary (§10.3.1).
package execution
