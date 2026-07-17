# Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
#
# WSO2 LLC. licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.
#
# Build context MUST be the repo root (not this directory) — pnpm workspace
# resolution needs the root pnpm-lock.yaml / pnpm-workspace.yaml. See the
# `aep-mcp-server` entry in deployments/docker-compose.yml.

FROM node:22-alpine AS builder
WORKDIR /workspace

COPY package.json pnpm-lock.yaml pnpm-workspace.yaml tsconfig.base.json ./
COPY services/aep-mcp-server/package.json services/aep-mcp-server/package.json

RUN --mount=type=cache,id=pnpm-store,target=/pnpm/store \
    corepack enable && corepack prepare pnpm@10.12.1 --activate && \
    pnpm install --filter @aep/aep-mcp-server... --frozen-lockfile --store-dir=/pnpm/store

COPY services/aep-mcp-server/tsconfig.json services/aep-mcp-server/tsconfig.json
COPY services/aep-mcp-server/src services/aep-mcp-server/src

RUN pnpm --filter @aep/aep-mcp-server build

# `pnpm deploy --legacy` clones this package's prod deps from the
# content-addressable store into a self-contained node_modules (real files +
# a local .pnpm virtual store, not symlinks back into /workspace) — required
# because a plain `COPY .../node_modules` would carry dangling symlinks that
# resolve nowhere once /workspace doesn't exist in the runtime stage.
RUN pnpm --filter @aep/aep-mcp-server deploy --prod --legacy /deploy

FROM node:22-alpine
WORKDIR /app

COPY --from=builder /deploy/dist ./dist
COPY --from=builder /deploy/node_modules ./node_modules
COPY --from=builder /deploy/package.json ./

# node:22-alpine already ships a non-root `node` user (uid/gid 1000) — reuse
# it instead of creating a new one (which collides on gid 1000).
USER node

EXPOSE 3400
ENTRYPOINT ["node", "dist/main.js"]
