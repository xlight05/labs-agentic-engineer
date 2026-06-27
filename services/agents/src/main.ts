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

/**
 * The composition root: build the model ONCE, pick a store, mount the SSE app,
 * listen. The only place that wires concrete dependencies together.
 *
 *   pnpm --filter @aep/agents dev     # watch + reload
 *   pnpm --filter @aep/agents start   # run once
 */

import { createApp } from "./server.js";
import { createModel } from "./shared/model.js";
import { loadAnthropicKey, intEnv } from "./shared/env.js";
import { InMemoryConversationStore } from "./store/memory-store.js";

const port = intEnv(process.env.PORT, 4000);

const app = createApp({
  store: new InMemoryConversationStore(), // Postgres later, same interface
  model: createModel({ apiKey: loadAnthropicKey() }), // built once, injected per turn
});

app.listen(port, () => {
  process.stdout.write(`@aep/agents SSE server listening on :${port}\n`);
});
