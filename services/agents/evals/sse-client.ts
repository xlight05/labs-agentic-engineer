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
 * The reference SSE consumer — exactly what the browser is. POSTs one turn and
 * yields each raw `StreamPart` frame until `[DONE]`, buffered across chunk
 * boundaries. Each frame is a single physical line (`data: <json>`), since the
 * SDK `JSON.stringify`s each part (embedded newlines are escaped), so the only
 * real hazard is the cross-chunk buffering handled here.
 */

import { SSE_DONE, type TurnRequest } from "@aep/contracts";
import type { StreamPart } from "../src/agents/main/stream-types.js";

// The turn-request body is the shared contract type (one definition, no drift).
export type { TurnRequest };

export async function* streamTurn(
  baseUrl: string,
  id: string,
  body: TurnRequest,
): AsyncIterable<StreamPart> {
  const res = await fetch(`${baseUrl}/conversations/${encodeURIComponent(id)}/turns`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok || !res.body) {
    const text = await res.text().catch(() => "");
    throw new Error(`turn failed: HTTP ${res.status} ${text}`);
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });

    // Frames are delimited by a blank line.
    let sep: number;
    while ((sep = buffer.indexOf("\n\n")) !== -1) {
      const frame = buffer.slice(0, sep).trim();
      buffer = buffer.slice(sep + 2);
      if (!frame.startsWith("data:")) continue;
      const data = frame.slice("data:".length).trim();
      if (data === SSE_DONE) return;
      yield JSON.parse(data) as StreamPart;
    }
  }
}
