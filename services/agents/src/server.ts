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
 * The thin Express SSE layer. One turn = one HTTP request:
 *
 *   POST /conversations/:id/turns  → SSE stream of RAW StreamPart frames + [DONE]
 *   GET  /conversations/:id        → rehydrate { status, messages, ... } (404 if unknown)
 *
 * The route maps `runConversationTurn`'s `onEvent` straight to `data: <part>`
 * frames (no envelope — Decision 3). The connection lives only for the turn and
 * closes at `[DONE]`. The stream starts LAZILY (on the first event), so a
 * pre-stream failure (validation, a concurrent turn → 409) is still an HTTP
 * status; once streaming, every failure is an `error` frame then `[DONE]`.
 *
 * These routes are internal-only, behind the platform BFF (which authenticates
 * and owns conversation-id isolation); this service does not re-authenticate.
 */

import express from "express";
import type { Express, Request, Response, NextFunction } from "express";
import type { LanguageModel } from "ai";
import { SSE_DONE, type Skill } from "@aep/contracts";
import type { ConversationStore } from "./store/conversation-store.js";
import type { StreamPart } from "./agents/main/stream-types.js";
import { runConversationTurn, TurnGuard, ConcurrentTurnError } from "./conversation/run-conversation-turn.js";
import { config } from "./shared/config.js";

export interface CreateAppDeps {
  store: ConversationStore;
  /** Built ONCE at the composition root (not per turn). */
  model: LanguageModel;
  /** Max request body for the turn endpoint (default `config.bodyLimit`). */
  bodyLimit?: string;
}

function startSSE(res: Response): void {
  res.status(200);
  res.setHeader("Content-Type", "text/event-stream");
  res.setHeader("Cache-Control", "no-cache, no-transform");
  res.setHeader("Connection", "keep-alive");
  res.setHeader("X-Accel-Buffering", "no"); // defeat proxy buffering of the stream
  res.flushHeaders();
}

export function createApp(deps: CreateAppDeps): Express {
  const app = express();
  const guard = new TurnGuard(); // one in-flight guard per app (serializes turns per id)
  app.use(express.json({ limit: deps.bodyLimit ?? config.bodyLimit }));

  app.post("/conversations/:id/turns", async (req: Request, res: Response) => {
    const id = req.params.id as string;
    const body = (req.body ?? {}) as {
      instruction?: unknown;
      files?: unknown;
      filesChangedExternally?: unknown;
      skills?: unknown;
    };

    // Pre-stream validation → HTTP status (no SSE headers sent yet).
    if (typeof body.instruction !== "string" || body.instruction.trim() === "") {
      res.status(400).json({ error: "instruction is required" });
      return;
    }
    if (body.files === null || typeof body.files !== "object" || Array.isArray(body.files)) {
      res.status(400).json({ error: "files (a path→content map) is required" });
      return;
    }
    const fileEntries = Object.entries(body.files as Record<string, unknown>);
    const badEntry = fileEntries.find(([, v]) => typeof v !== "string");
    if (badEntry) {
      res.status(400).json({ error: `files["${badEntry[0]}"] must be a string` });
      return;
    }
    // Validated above (every value is a string) — no rebuild needed.
    const files = body.files as Record<string, string>;

    // skills (optional): the caller-resolved candidate set (ADR-0002). Each must
    // be { name, description, content } strings; reject a malformed payload
    // pre-stream so it's a clean 400, not an opaque mid-stream failure.
    let skills: Skill[] | undefined;
    if (body.skills !== undefined) {
      if (!Array.isArray(body.skills)) {
        res.status(400).json({ error: "skills must be an array" });
        return;
      }
      const isSkill = (s: unknown): s is Skill =>
        s !== null &&
        typeof s === "object" &&
        typeof (s as Record<string, unknown>).name === "string" &&
        typeof (s as Record<string, unknown>).description === "string" &&
        typeof (s as Record<string, unknown>).content === "string";
      if (!body.skills.every(isSkill)) {
        res.status(400).json({ error: "each skill must be { name, description, content } strings" });
        return;
      }
      skills = body.skills;
    }

    // Abort the turn if the client disconnects mid-stream.
    const ac = new AbortController();
    res.on("close", () => ac.abort());

    // Lazy stream start: headers go out on the FIRST event. A failure BEFORE any
    // event (e.g. a concurrent turn) is still a clean HTTP status.
    let started = false;
    const send = (part: StreamPart): void => {
      if (!started) {
        startSSE(res);
        started = true;
      }
      res.write(`data: ${JSON.stringify(part)}\n\n`);
    };

    try {
      await runConversationTurn({
        id,
        instruction: body.instruction,
        files,
        filesChangedExternally: body.filesChangedExternally === true,
        ...(skills ? { skills } : {}),
        model: deps.model,
        store: deps.store,
        guard,
        onEvent: send,
        abortSignal: ac.signal,
      });
      if (!started) startSSE(res); // no events emitted — still open + terminate the stream
      res.write(`data: ${SSE_DONE}\n\n`);
      res.end();
    } catch (err) {
      if (!started) {
        // Pre-stream failure → HTTP status + JSON.
        if (err instanceof ConcurrentTurnError) {
          res.status(409).json({ error: err.message });
        } else {
          res.status(500).json({ error: err instanceof Error ? err.message : "internal error" });
        }
      } else {
        // Already streaming → cannot change status; emit an error frame then [DONE].
        const message = err instanceof Error ? err.message : String(err);
        res.write(`data: ${JSON.stringify({ type: "error", error: message })}\n\n`);
        res.write(`data: ${SSE_DONE}\n\n`);
        res.end();
      }
    }
  });

  app.get("/conversations/:id", async (req: Request, res: Response) => {
    const conv = await deps.store.get(req.params.id as string);
    if (!conv) {
      res.status(404).json({ error: "conversation not found" });
      return;
    }
    // Raw ModelMessage[] — the UI projects tool parts to Changes client-side (§9).
    res.json({
      status: conv.status,
      messages: conv.messages,
      createdAt: conv.createdAt,
      updatedAt: conv.updatedAt,
    });
  });

  // Body-parser / payload errors → JSON (413 when the snapshot exceeds the limit).
  app.use((err: unknown, _req: Request, res: Response, next: NextFunction) => {
    if (res.headersSent) {
      next(err); // mid-stream — let the default handler tear down the connection
      return;
    }
    const e = err as { type?: string; status?: number } | null;
    if (e?.type === "entity.too.large" || e?.status === 413) {
      res.status(413).json({ error: "request body exceeds the configured limit" });
      return;
    }
    res.status(400).json({ error: "invalid request body" });
  });

  return app;
}
