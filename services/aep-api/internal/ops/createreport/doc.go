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

// Package createreport records an RCA-agent report for the bound org.
//
// Trigger: POST /rca-agent/reports (create-rca-agent-report).
// In→out:  bound org (tenant.BoundOrgFromContext) + report body → the stored report.
// Ports:   ops.Repository.
// Invariant: fields the contract marks required are validated HERE, so a caller
// gets a precise 400 instead of a DB NOT NULL 500.
package createreport
