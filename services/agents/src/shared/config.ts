/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import { loadDotenv, intEnv } from "./env.js";

// Load the nearest .env BEFORE reading process.env, so AGENT_MODEL /
// AGENT_MAX_STEPS set only in .env are honored (not silently dropped).
loadDotenv();

export const config = {
  model: process.env.AGENT_MODEL || "claude-sonnet-4-6",
  // A fresh "write an app" generation needs 10–15 file calls; steps batch
  // parallel tool calls, so the loop budget is higher than the call count.
  // intEnv guards a non-numeric value (which would NaN out the step cap).
  maxSteps: intEnv(process.env.AGENT_MAX_STEPS, 20),
  logLevel: process.env.LOG_LEVEL || "info",
  // Max request body for the SSE turn endpoint (the inlined files snapshot). The
  // express default 100kb silently rejects a real spec repo, so set it explicitly.
  bodyLimit: process.env.AGENT_BODY_LIMIT || "10mb",
} as const;
