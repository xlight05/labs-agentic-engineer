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

// Package getreport reads one RCA report and reconciles it against live executions.
//
// Trigger: GET /rca-agent/reports/{reportId} (get-rca-agent-report).
// In→out:  bound org + report id → the report, Dispatched/Deployed refreshed; 404 when absent.
// Ports:   ops.Repository, ops.ExecutionReader (optional — nil serves the stored snapshot).
// Invariant: correlation only promotes false→true, and is best-effort — a lookup
// failure serves the stored snapshot rather than failing the read.
package getreport
