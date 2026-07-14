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

import { useQuery } from "@tanstack/react-query";
import type { components } from "../../../generated/aep-api";
import { client } from "../../../api/client";
import { specKeys } from "./keys";
import { toSpecEntries } from "./mapping";
import { apiErrorMessage } from "../../../api/errors";

type FileContent = components["schemas"]["FileContent"];

function toError(error: unknown, fallback: string): Error {
  return new Error(apiErrorMessage(error, fallback));
}

/**
 * Spec file metadata at HEAD (#113): list-files, mapped to the view model.
 * A ONE-SHOT load — the committed snapshot for first paint and the fallback
 * while collab is offline / a room seed failed. Live changes (agent-created
 * files, edits) arrive through the collab doc, not this query, so there is no
 * poll (SpecView unions this with the live doc list). Out-of-room commits
 * won't reflect until reload — the parked external-merge concern (#86).
 */
export function useSpecFiles(projectName: string) {
  return useQuery({
    queryKey: specKeys.files(projectName),
    queryFn: async () => {
      const { data, error } = await client.GET(
        "/projects/{projectName}/files",
        { params: { path: { projectName } } },
      );
      if (error) throw toError(error, "Failed to load the spec files");
      return toSpecEntries(data ?? []);
    },
    staleTime: Infinity,
  });
}

/**
 * Fetch one spec file's content. Shared by the lazy selection hook below and
 * the derived-view hooks (useDerivedCellDiagram, useDerivedWireframe), which
 * fetch several files at once outside of a single "selected file" context.
 */
export async function fetchSpecFileContent(
  projectName: string,
  file: { path: string; sha: string },
): Promise<FileContent> {
  // The contract's {path} param is a trailing wildcard — it spans multiple
  // segments, and openapi-fetch's serializer would percent-encode the very
  // slashes the wildcard exists to keep. Build the concrete URL, cast to
  // the contract path so the response stays typed. `file.path` is already
  // the full repo path (specs/…) the Files API expects.
  const repoPath = file.path
    .split("/")
    .map(encodeURIComponent)
    .join("/");
  const { data, error } = await client.GET(
    `/projects/${encodeURIComponent(projectName)}/files/${repoPath}` as "/projects/{projectName}/files/{path}",
    { params: { path: { projectName, path: file.path } } },
  );
  if (error || data === undefined)
    throw toError(error, `Failed to load ${file.path}`);
  return data;
}

/**
 * Content of one spec file, fetched lazily for the selection (#113
 * decision 4). Immutable per (path, sha) — see specKeys.file.
 */
export function useSpecFileContent(
  projectName: string,
  file: { path: string; sha: string } | null,
) {
  return useQuery({
    queryKey: specKeys.file(projectName, file?.path ?? "", file?.sha ?? ""),
    enabled: file !== null,
    staleTime: Infinity,
    queryFn: () => {
      if (!file) throw new Error("no file selected");
      return fetchSpecFileContent(projectName, file);
    },
  });
}
