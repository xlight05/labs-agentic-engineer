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

import { useEffect, useMemo, useRef, useState } from "react";
import * as Y from "yjs";
import { HocuspocusProvider } from "@hocuspocus/provider";
import { listDocPaths } from "@aep/collab-doc";
import { getAccessToken } from "../../../auth/token";

// Console side of #86 phase 5: connect the spec view to the collab service.
// One room + one Y.Doc per project (`spec-<org>-<project>`), Y.Map('files')
// of path → Y.Text. If no collab server is reachable the view degrades to
// solo (#86 decision 10) — callers keep their non-collaborative fallback.
//
// The connection authenticates with the session's access token (#91): a real
// Thunder JWT in thunder mode (verified by the BFF oracle), the unsigned
// mock token in mock mode (decoded by the collab mock BFF). The token is a
// getter so reconnects pick up silently-renewed tokens.

export interface CollabPeer {
  clientId: number;
  name: string;
  color: string;
  kind: "user" | "agent";
}

export type CollabStatus = "connecting" | "connected" | "offline";

export interface CollabSpec {
  status: CollabStatus;
  peers: CollabPeer[];
  /** Y.Text for a non-md path, once synced; null → REST-content fallback. */
  getFileText: (path: string) => Y.Text | null;
  /** Y.XmlFragment for an md path, once synced (#86 phase 6 doc model). */
  getFileFragment: (path: string) => Y.XmlFragment | null;
  /** Live file paths in the doc (Y.Map entries + md fragments) — the source
   *  for the reactive spec list; empty until connected. */
  docPaths: string[];
  /** The live provider (for CollaborationCaret); null until connected. */
  provider: HocuspocusProvider | null;
  /** The room Y.Doc — exists from mount regardless of connection (so an
   *  offline/solo client still has a local doc to work on). Null before the
   *  first effect run. */
  doc: Y.Doc | null;
  /** This client's presence identity (name/color) for caret labels. */
  self: { name: string; color: string };
  /** True while a transaction originates from this client (binding helper). */
  isLocalTransaction: (transaction: Y.Transaction) => boolean;
  /** Bumped on any files-map change so selections can re-resolve. */
  version: number;
  /** Force the room's pending doc edits to commit to git NOW and resolve once
   *  done (#162) — the console awaits this before triggering a build, which
   *  tags HEAD. Resolves immediately when offline (nothing shared to commit);
   *  rejects on a flush error or timeout. */
  flush: () => Promise<void>;
}

// A forced flush is one files/apply commit — quick, but allow slack for the
// git op before the build is blocked on a hung reply.
const FLUSH_TIMEOUT_MS = 30_000;

const PEER_COLORS = [
  "#e57373", "#64b5f6", "#81c784", "#ffb74d",
  "#ba68c8", "#4dd0e1", "#f06292", "#aed581",
];

function collabWsUrl(): string {
  const env = (window as { _env_?: { collabWsUrl?: string } })._env_;
  if (env?.collabWsUrl) return env.collabWsUrl;
  if (import.meta.env.DEV) return "ws://localhost:8091";
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/collab`;
}

export function useCollabSpec(
  projectName: string,
  user: { name: string; email: string },
  orgHandle: string,
): CollabSpec {
  const [status, setStatus] = useState<CollabStatus>("connecting");
  // Flips true once the local Y.Doc is created (mount), so the memoized return
  // re-exposes `doc` even when the room never connects (offline/solo).
  const [docReady, setDocReady] = useState(false);
  const [peers, setPeers] = useState<CollabPeer[]>([]);
  const [version, setVersion] = useState(0);
  const docRef = useRef<Y.Doc | null>(null);
  const providerRef = useRef<HocuspocusProvider | null>(null);
  // Last-seen file-path set (serialized) so we re-render the list only when a
  // file is added/removed/created — not on every keystroke within a file.
  const pathKeyRef = useRef("");
  // In-flight flush requests keyed by correlation id (#162): the server acks
  // via a stateless message that resolves/rejects the matching promise.
  const pendingFlushes = useRef(
    new Map<string, { resolve: () => void; reject: (e: Error) => void }>(),
  );

  useEffect(() => {
    const doc = new Y.Doc();
    docRef.current = doc;
    setDocReady(true);
    // Stable across this effect — captured so the cleanup doesn't read a ref
    // that could have moved (react-hooks/exhaustive-deps).
    const flushes = pendingFlushes.current;
    const provider = new HocuspocusProvider({
      url: collabWsUrl(),
      name: `spec-${orgHandle}-${projectName}`,
      document: doc,
      token: async () => (await getAccessToken()) ?? "",
      onSynced: () => setStatus("connected"),
      onStatus: ({ status: s }) => {
        if (s === "disconnected") setStatus("offline");
      },
      onAuthenticationFailed: () => setStatus("offline"),
    });
    providerRef.current = provider;

    provider.setAwarenessField("user", {
      name: user.name,
      color: PEER_COLORS[doc.clientID % PEER_COLORS.length],
      kind: "user",
    });
    const awareness = provider.awareness;
    const onAwareness = () => {
      if (!awareness) return;
      const list: CollabPeer[] = [];
      awareness.getStates().forEach((state, clientId) => {
        if (clientId === doc.clientID) return;
        const u = (state as { user?: Partial<CollabPeer> }).user;
        if (!u?.name) return;
        list.push({
          clientId,
          name: u.name,
          color: u.color ?? PEER_COLORS[clientId % PEER_COLORS.length] ?? "#888",
          kind: u.kind === "agent" ? "agent" : "user",
        });
      });
      setPeers(list);
    };
    awareness?.on("change", onAwareness);

    // Re-render the list when the FILE SET changes. Watching the whole doc
    // (not just Y.Map('files')) is load-bearing: markdown files are top-level
    // Y.XmlFragments, so a new agent-created .md file appears as a new share,
    // which a Y.Map observer never sees. afterAllTransactions fires on local
    // and synced remote changes alike; we diff the path set so content edits
    // (which bind to the editor directly) don't thrash the list.
    const onDocChange = () => {
      const key = listDocPaths(doc).join("\n");
      if (key !== pathKeyRef.current) {
        pathKeyRef.current = key;
        setVersion((v) => v + 1);
      }
    };
    doc.on("afterAllTransactions", onDocChange);

    // Flush acks (#162): the server replies to a flush request with a stateless
    // {type:"flushed"|"flush-error", id} — resolve/reject the matching promise.
    const onStateless = ({ payload }: { payload: string }) => {
      let msg: { type?: string; id?: string; message?: string };
      try {
        msg = JSON.parse(payload) as typeof msg;
      } catch {
        return;
      }
      if (!msg.id) return;
      const pending = pendingFlushes.current.get(msg.id);
      if (!pending) return;
      pendingFlushes.current.delete(msg.id);
      if (msg.type === "flushed") pending.resolve();
      else if (msg.type === "flush-error")
        pending.reject(new Error(msg.message ?? "Failed to commit the workspace."));
    };
    provider.on("stateless", onStateless);

    provider.attach();
    return () => {
      doc.off("afterAllTransactions", onDocChange);
      awareness?.off("change", onAwareness);
      provider.off("stateless", onStateless);
      // Fail any in-flight flush so a caller (Build) never hangs on teardown.
      for (const p of flushes.values())
        p.reject(new Error("Collaboration session ended before the commit finished."));
      flushes.clear();
      provider.destroy();
      doc.destroy();
      docRef.current = null;
      providerRef.current = null;
    };
  }, [projectName, user.name, user.email, orgHandle]);

  return useMemo(
    () => ({
      status,
      peers,
      version,
      getFileText: (path: string) =>
        status === "connected"
          ? (docRef.current?.getMap<Y.Text>("files").get(path) ?? null)
          : null,
      getFileFragment: (path: string) =>
        status === "connected"
          ? (docRef.current?.getXmlFragment(path) ?? null)
          : null,
      docPaths:
        status === "connected" && docRef.current
          ? listDocPaths(docRef.current)
          : [],
      provider: status === "connected" ? providerRef.current : null,
      doc: docReady ? docRef.current : null,
      self: {
        name: user.name,
        color:
          PEER_COLORS[(docRef.current?.clientID ?? 0) % PEER_COLORS.length] ??
          "#888",
      },
      isLocalTransaction: (transaction: Y.Transaction) => transaction.local,
      flush: () =>
        new Promise<void>((resolve, reject) => {
          const provider = providerRef.current;
          if (status !== "connected" || !provider) {
            resolve(); // offline / solo — nothing shared to commit
            return;
          }
          const id = crypto.randomUUID();
          const timer = setTimeout(() => {
            pendingFlushes.current.delete(id);
            reject(new Error("Timed out waiting for the workspace to commit."));
          }, FLUSH_TIMEOUT_MS);
          pendingFlushes.current.set(id, {
            resolve: () => {
              clearTimeout(timer);
              resolve();
            },
            reject: (e) => {
              clearTimeout(timer);
              reject(e);
            },
          });
          provider.sendStateless(JSON.stringify({ type: "flush", id }));
        }),
    }),
    [status, peers, version, user.name, docReady],
  );
}
