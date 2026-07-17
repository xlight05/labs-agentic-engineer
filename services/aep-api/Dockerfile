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

FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# Cache the Go module + build caches (BuildKit) so incremental rebuilds
# reuse compiled packages instead of recompiling from scratch.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o bin/aep-api ./cmd/aep-api

FROM alpine:3.21

# git at runtime: the binary reads/writes/tags project repos via local git
# plumbing over bare mirrors on the shared /workspaces volume
# (docs/design/shared-volume-clone-architecture.md) — clone/fetch/push/tag
# shell out to the git CLI. GitHub REST remains only for repo admin, issues,
# webhooks, and App installs (§9). aep-api is the sole writer of the mount;
# the agents service consumes read-only per-SHA snapshots from it.
RUN apk add --no-cache git
RUN addgroup -g 1000 appgroup && adduser -S -u 1000 -G appgroup appuser

WORKDIR /app
COPY --from=builder /app/bin/aep-api .

USER appuser

EXPOSE 8080

ENTRYPOINT ["./aep-api"]
