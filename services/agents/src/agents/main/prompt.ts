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

import type { Skill } from "@aep/contracts";

/** System instructions for the file-mutating main agent. */
export const instructions = `You are a spec-bundle editing agent. You are given a set of existing files
(inlined in the user message) and an instruction. Apply the instruction by calling the file tools.

Tools:
- editFile(path, oldString, newString) — change part of an existing file. PREFER THIS: it is far cheaper
  than rewriting a file. Copy oldString VERBATIM from the inlined file, including its exact leading
  indentation and newlines. oldString must match EXACTLY ONE place.
- addFile(path, content) — create a NEW file (emits a whole body). Use only for files that do not exist yet.
- removeFile(path) — delete a file.
- setFrontmatterField(path, key, value) — set a key in a markdown file's YAML frontmatter (between the ---
  fences, e.g. language, buildpack, skillsApplied). Use this for ANY frontmatter change — never anchor an
  editFile inside the --- fences; list values are fragile to indent by hand.

Editing discipline:
- To replace MOST of a file, removeFile then addFile — do not chain many edits.
- openapi.yaml and frontmatter are indentation-sensitive: include the exact leading whitespace in both
  oldString and newString. To keep an anchor unique in a repetitive YAML file, include the parent key line.

Reacting to tool results (each result tells you the next move):
- ok:true — applied. status "already-applied" or "noop" means it was already done; do NOT retry, move on.
- NOT_UNIQUE — your anchor matched several lines (listed with line numbers); re-issue editFile with a longer
  oldString that includes a surrounding unique line.
- NOT_FOUND — the snippet is not present verbatim; re-copy it exactly (mind indentation) from the inlined file.
- INVALID_YAML — your edit would break the YAML and was rejected; fix the indentation of newString and retry.

Keep prose outside tool calls to a single short sentence. When the instruction is fully applied, stop.`;

/**
 * The skill catalog appended to the END of the system prompt (ADR-0002): skill
 * names + one-line descriptions only, never bodies. It is identical across a
 * conversation's turns (the caller sends the same library), so the cacheable
 * instruction prefix is preserved. Returns "" for an empty list, leaving the
 * base instructions byte-identical to a skill-free turn.
 */
export function buildSkillCatalog(skills: readonly Skill[]): string {
  if (skills.length === 0) return "";
  const lines = skills.map((s) => `- ${s.name}: ${s.description}`).join("\n");
  return `

# Skills

You have access to skills — reusable guidance for specific tasks. Only their names and one-line descriptions are listed below; the full guidance is hidden until you load it. If a skill is relevant to the instruction, call loadSkill(name) to read its guidance BEFORE applying it — never guess a skill's contents.

${lines}`;
}

/** Base instructions + the skill catalog (empty when no skills are supplied). */
export function buildInstructions(skills: readonly Skill[] = []): string {
  return instructions + buildSkillCatalog(skills);
}

/**
 * The starting spec bundle the demo mutates. Mirrors the hello-api example in
 * design.md: free-form prose, markdown-with-frontmatter, and indentation-
 * sensitive OpenAPI YAML — the three shapes the tools must handle.
 */
export const SEED_FILES: Record<string, string> = {
  "specs/requirements/requirements.md": `# Overview

A simple API that responds with "Hello, World!" when called.

# Personas

- Developer — calls the API to get a hello world response.

# Features

- A developer sends a request to the API.
- The API responds with "Hello, World!" in the response body.
- The response is in JSON format with a message field.
- The API is accessible via a single endpoint.
- Requests work without requiring any parameters or authentication.
`,

  "specs/design/design.md": `---
skillsApplied:
  - api-management
  - go
  - react-webapp
  - thunder-authentication
---

A simple public API service that responds with "Hello, World!" in JSON format. Built as a single Go service exposing one endpoint, requiring no authentication.
`,

  "specs/design/components/hello-api/design.md": `---
type: service
language: Go
buildpack: docker
appPath: hello-api
entrypoint: deployment/service
---

# hello-api

Implement a simple Go HTTP service on port 9090 using net/http. Expose GET /hello that returns {"message": "Hello, World!"} with Content-Type: application/json. Include GET /health returning 200 OK for liveness probes. This is a public API — no authentication required, no X-User-Id checks.
`,

  "specs/design/components/hello-api/openapi.yaml": `openapi: 3.0.3
info:
  title: Hello API
  version: 1.0.0
  description: A simple API that responds with "Hello, World!" when called.

servers:
  - url: /
    description: Default server

paths:
  /hello:
    get:
      summary: Get hello world message
      description: Returns a simple "Hello, World!" message in JSON format.
      operationId: getHello
      responses:
        '200':
          description: Successful response with hello message
          content:
            application/json:
              schema:
                type: object
                required:
                  - message
                properties:
                  message:
                    type: string
                    example: "Hello, World!"

  /health:
    get:
      summary: Health check endpoint
      description: Returns the health status of the service.
      operationId: getHealth
      responses:
        '200':
          description: Service is healthy
`,
};

/** Build the user prompt: the current bundle inlined + the mutation instruction. */
export function buildPrompt(files: Record<string, string>, instruction: string): string {
  const inlined = Object.entries(files)
    // Ensure a newline before the closing fence so a file lacking a trailing \n
    // doesn't glue its last line to ``` (corrupting the block boundary).
    .map(([path, content]) => `### ${path}\n\`\`\`\n${content.endsWith("\n") ? content : `${content}\n`}\`\`\``)
    .join("\n\n");
  return `Existing files:

${inlined}

Instruction: ${instruction}`;
}
