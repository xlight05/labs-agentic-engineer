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

// @vitest-environment jsdom

import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Each entry is one GET's answer, consumed in order.
let responses: Array<{
  logs: Array<{ log: string; timestamp?: string }>;
  complete: boolean;
  nextCursor?: number;
}> = [];
let errorOnce = false;
let sinceSeen: Array<number | undefined> = [];

vi.mock("../../../api/client", () => ({
  client: {
    GET: (_path: string, opts: { params: { query?: { since?: number } } }) => {
      sinceSeen.push(opts.params.query?.since);
      if (errorOnce) {
        errorOnce = false;
        return Promise.resolve({ data: undefined, error: { message: "boom" } });
      }
      const next = responses.shift();
      return Promise.resolve({ data: next ?? { logs: [], complete: true }, error: undefined });
    },
  },
}));

import { useBuildLog } from "./useBuildLog";

function open() {
  return renderHook(() => useBuildLog("acme", "api", "run-1", true));
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
});

afterEach(() => {
  vi.useRealTimers();
  responses = [];
  errorOnce = false;
  sinceSeen = [];
});

describe("useBuildLog", () => {
  // One request, no cursor, no polling — the common case of scrolling back to
  // a version that finished days ago.
  it("reads a finished build once and stops", async () => {
    responses = [
      { logs: [{ log: "done" }], complete: true, nextCursor: 1_000 },
    ];
    const { result } = open();

    await waitFor(() => expect(result.current.complete).toBe(true));
    expect(result.current.entries.map((e) => e.log)).toEqual(["done"]);

    await vi.advanceTimersByTimeAsync(10_000);
    expect(sinceSeen).toHaveLength(1);
  });

  // A running build tails: each read starts where the last ended, and lines
  // ACCUMULATE rather than replacing what is on screen.
  it("appends from the cursor while the build is still running", async () => {
    responses = [
      { logs: [{ log: "line 1" }], complete: false, nextCursor: 1_000 },
      { logs: [{ log: "line 2" }], complete: true, nextCursor: 2_000 },
    ];
    const { result } = open();

    await waitFor(() => expect(result.current.entries).toHaveLength(1));
    await vi.advanceTimersByTimeAsync(2_000);

    await waitFor(() => expect(result.current.complete).toBe(true));
    expect(result.current.entries.map((e) => e.log)).toEqual(["line 1", "line 2"]);
    // First read has no cursor; the second resumes from the first's.
    expect(sinceSeen).toEqual([undefined, 1_000]);
  });

  // Without this the cursor would rewind and the whole log would replay.
  it("keeps the previous cursor when a page carries none", async () => {
    responses = [
      { logs: [{ log: "line 1" }], complete: false, nextCursor: 1_000 },
      { logs: [], complete: false }, // nothing new, so no cursor came back
      { logs: [{ log: "line 2" }], complete: true, nextCursor: 3_000 },
    ];
    const { result } = open();

    await waitFor(() => expect(result.current.entries).toHaveLength(1));
    await vi.advanceTimersByTimeAsync(2_000);
    await vi.advanceTimersByTimeAsync(2_000);

    await waitFor(() => expect(result.current.complete).toBe(true));
    expect(sinceSeen).toEqual([undefined, 1_000, 1_000]);
  });

  it("surfaces a failed read and stops polling rather than spinning", async () => {
    errorOnce = true;
    const { result } = open();

    await waitFor(() => expect(result.current.error).toBeDefined());
    await vi.advanceTimersByTimeAsync(10_000);
    expect(sinceSeen).toHaveLength(1);
  });

  // A collapsed row must cost nothing.
  it("reads nothing while closed", async () => {
    renderHook(() => useBuildLog("acme", "api", "run-1", false));
    await vi.advanceTimersByTimeAsync(5_000);
    expect(sinceSeen).toHaveLength(0);
  });
});
