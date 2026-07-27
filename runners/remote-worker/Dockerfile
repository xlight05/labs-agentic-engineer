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

# The remote-worker runner image — ONE image for both task kinds
# (AEP_TASK_KIND=implementation and =validation). Debian-based because
# Playwright's browser builds are glibc-linked and cannot run on a musl
# base, so a single image is only possible on Debian.
#
# What each layer is for:
#   - git / gh / curl / bash / python3 — workspace provisioning, the git
#     credential helper + `gh` wrapper, and the agent's Bash tool.
#   - Go (pinned tarball, not a distro package) + node/npm from the base —
#     the agent's compile-level build verification (`go build`,
#     `tsc --noEmit`, lockfile resolution) before it opens a PR. There is
#     no container runtime here: Dockerfiles are verified by the
#     post-merge OpenChoreo build, not in this pod.
#   - Playwright + a baked chromium — validation runs author and execute
#     e2e specs against the deployed system and must never download a
#     browser at task time.

FROM node:22-bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        git \
        curl \
        bash \
        python3 \
        ca-certificates \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# Pinned Go tarball. The distro/apk package the retired alpine image used
# was unpinned, so the toolchain the agent verified against could drift
# under it between rebuilds.
ARG GO_VERSION=1.25.5
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-$(dpkg --print-architecture).tar.gz" \
    | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:${PATH}"

# Playwright toolchain, pinned as a pair: AEP_PLAYWRIGHT_VERSION is the
# contract the aep-validation skill pins tests/e2e/package.json to, so
# the browsers baked here always match the version the specs run with.
ARG PLAYWRIGHT_VERSION=1.61.1
ARG PLAYWRIGHT_CLI_VERSION=0.1.15
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright \
    AEP_PLAYWRIGHT_VERSION=${PLAYWRIGHT_VERSION}
RUN npm install -g "@playwright/test@${PLAYWRIGHT_VERSION}" "@playwright/cli@${PLAYWRIGHT_CLI_VERSION}" \
    && playwright install --with-deps chromium \
    # playwright-cli may bundle a different playwright build; bake its
    # browser too so first use at task time stays offline. Non-fatal:
    # worst case it downloads at runtime.
    && (playwright-cli install-browser || true) \
    && chmod -R a+rX /ms-playwright

RUN groupadd -r aep \
    && useradd -r -g aep -m -d /home/aep aep \
    && mkdir -p /home/aep/aep-workspace \
    && chown -R aep:aep /home/aep

WORKDIR /app

# Install dependencies first so the layer caches across source-only edits.
# --include=optional ensures the platform-native Claude binary is installed.
# The Agent SDK probes the -musl native binary BEFORE the glibc one on
# Linux and only falls through when the probe fails to resolve; npm
# installs both libc variants, so on this Debian image the SDK would
# pick the musl binary and die at spawn (ENOENT: no musl loader).
# Removing the musl variant makes resolution land on the glibc binary.
COPY --chown=aep:aep package.json package-lock.json* ./
RUN npm ci --include=optional \
    && rm -rf node_modules/@anthropic-ai/claude-agent-sdk-linux-*-musl

# .dockerignore excludes node_modules/dist/.git/etc so we never ship a
# duplicate of the dependency tree the previous step already produced.
# --chown writes ownership directly into the layer instead of a trailing
# `chown -R /app` (which would rewrite every file as a fresh layer and
# roughly double the image size).
COPY --chown=aep:aep . .

USER aep
ENTRYPOINT ["npx", "tsx", "src/oneshot.ts"]
