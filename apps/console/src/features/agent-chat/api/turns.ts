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

import type { components } from "../../../generated/aep-api";
import { client } from "../../../api/client";
import { apiErrorMessage } from "../../../api/errors";

export type TurnStatus = components["schemas"]["TurnStatus"];

/**
 * Start a room-scoped agent turn (#86 phase 4 / #130): `collab: true` — the
 * agent joins the project's spec room as a live peer and edits the shared
 * doc; the panel only receives narration + tool results.
 */
export async function startCollabTurn(
  projectName: string,
  conversationId: string,
  instruction: string,
): Promise<string> {
  const { data, error, response } = await client.POST(
    "/projects/{projectName}/agents/{conversationId}/messages",
    {
      params: { path: { projectName, conversationId } },
      body: { instruction, collab: true },
    },
  );
  if (error || data === undefined) {
    if (response.status === 409) {
      throw new Error("An agent turn is already running for this project — wait for it to finish.");
    }
    throw new Error(apiErrorMessage(error, "Failed to start the agent turn"));
  }
  return data.turnId;
}

/** Text-only rehydrate of a conversation's server-side history. */
export async function getConversationMessages(
  projectName: string,
  conversationId: string,
): Promise<{ role: string; content: unknown }[] | null> {
  const { data, error } = await client.GET(
    "/projects/{projectName}/agents/{conversationId}/messages",
    { params: { path: { projectName, conversationId } } },
  );
  if (error || data === undefined) return null;
  const body = data as { messages?: { role: string; content: unknown }[] };
  return body.messages ?? null;
}

/** The project's running turn, or null (204 / none). */
export async function getActiveTurn(
  projectName: string,
): Promise<TurnStatus | null> {
  const { data, error, response } = await client.GET(
    "/projects/{projectName}/turns/active",
    { params: { path: { projectName } } },
  );
  if (response.status === 204 || error || data === undefined) return null;
  return data;
}

export async function getTurn(
  projectName: string,
  turnId: string,
): Promise<TurnStatus | null> {
  const { data, error } = await client.GET(
    "/projects/{projectName}/turns/{turnId}",
    { params: { path: { projectName, turnId } } },
  );
  if (error || data === undefined) return null;
  return data;
}

/**
 * Open the turn's SSE stream as a raw byte stream (replay from `from`, then
 * live tail). The caller iterates it with @aep/agent-stream's parseSseStream.
 */
export async function openTurnStream(
  projectName: string,
  turnId: string,
  from: number,
  signal: AbortSignal,
): Promise<ReadableStream<Uint8Array>> {
  const { data, error } = await client.GET(
    "/projects/{projectName}/turns/{turnId}/stream",
    {
      params: { path: { projectName, turnId }, query: { from } },
      parseAs: "stream",
      signal,
    },
  );
  if (error || !data) throw new Error("Failed to attach to the turn stream");
  return data as ReadableStream<Uint8Array>;
}
