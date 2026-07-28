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

import { useEffect, useRef, useState } from "react";
import { client } from "../../../api/client";
import { apiErrorMessage } from "../../../api/errors";
import type { components } from "../../../generated/aep-api";

type BuildLogEntry = components["schemas"]["BuildLogEntry"];

/** How often a still-running build's log is re-read from its cursor. */
const TAIL_POLL_MS = 2_000;

export interface BuildLogState {
  entries: BuildLogEntry[];
  /** True once the build is terminal and the log will not grow again. */
  complete: boolean;
  /** True while a read is in flight and nothing has arrived yet. */
  loading: boolean;
  error: string | undefined;
}

/**
 * One build's log, read through the server's cursor.
 *
 * The same code path serves a live build and a finished one — that is the whole
 * point of a cursor rather than a stream. A terminal build answers complete on
 * the first read and is never asked again; a running build answers incomplete,
 * and each subsequent read starts from the previous response's `nextCursor` and
 * appends. A complete response carrying nothing is the honest "no log retained"
 * answer, and the caller renders it as such rather than as an error.
 *
 * Reads only while `open`, because a log is opened on demand: a page with four
 * collapsed builds must cost nothing.
 */
export function useBuildLog(
  projectName: string,
  componentName: string,
  buildName: string,
  open: boolean,
): BuildLogState {
  const [state, setState] = useState<BuildLogState>({
    entries: [],
    complete: false,
    loading: false,
    error: undefined,
  });
  // The cursor lives in a ref, not state: advancing it must not itself trigger
  // a render, and the poll effect must never re-run just because it moved.
  const cursor = useRef<number | undefined>(undefined);

  useEffect(() => {
    if (!open) return;

    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    // A different build behind the same drawer starts a fresh log.
    cursor.current = undefined;
    setState({ entries: [], complete: false, loading: true, error: undefined });

    const read = async () => {
      const { data, error } = await client.GET(
        "/projects/{projectName}/components/{componentName}/builds/{buildName}/logs",
        {
          params: {
            path: { projectName, componentName, buildName },
            query: cursor.current ? { since: cursor.current } : {},
          },
        },
      );
      if (cancelled) return;

      if (error || data === undefined) {
        setState((prev) => ({
          ...prev,
          loading: false,
          error: apiErrorMessage(error, "Failed to load the build log"),
        }));
        return; // Stop polling: a failing read will keep failing.
      }

      // Only advance on a cursor the server actually returned — a page with no
      // timestamped entry leaves the previous cursor standing rather than
      // rewinding to the start of the log.
      if (data.nextCursor) cursor.current = data.nextCursor;

      setState((prev) => ({
        entries: [...prev.entries, ...(data.logs ?? [])],
        complete: data.complete,
        loading: false,
        error: undefined,
      }));

      if (!data.complete) {
        timer = setTimeout(() => void read(), TAIL_POLL_MS);
      }
    };

    void read();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [projectName, componentName, buildName, open]);

  return state;
}
