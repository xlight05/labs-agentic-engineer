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

// Package devflow hosts the Temporal-backed development workflows: the
// per-version DevFlowWorkflow (tag → design → plan → task fan-out →
// validate), the per-task TaskFlowWorkflow (dispatch coding agent → PR →
// merge → build → deploy), and the validating phase's tree — the per-version
// ValidationFlowWorkflow orchestrator fanning out ValidationTaskWorkflow lane
// children (dispatch lanes → single PR → merge, no build/deploy).
// Activities are thin adapters over the existing
// feature services; existing webhook handlers and watchers feed the
// workflows via signals (see the delivery-root Signaler). The whole feature is
// additive: with Temporal unconfigured, nothing here runs and the rest of
// aep-api is untouched.
//
// As a delivery-domain sub-package it imports only the delivery root for the
// shared kernel it builds on — the Temporal Runtime, the Signaler, and the
// signal vocabulary (§10.3.1).
package devflow
