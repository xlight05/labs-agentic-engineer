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

// Package genaiturns serves the committed-truth turn edge for the bound org:
// start a turn, poll its status (by id or the active one), replay+tail its SSE
// event stream, and rehydrate a conversation.
//
// Trigger: create-turn / get-turn / get-active-turn / stream-turn / get-conversation.
// Ports:   spec.Service (the genai turn orchestrator).
package genaiturns
