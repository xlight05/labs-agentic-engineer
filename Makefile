# AEP root Makefile — the single entry point for the uniform verbs.
#
# Fans out to Turborepo (TypeScript) and a `go` loop over the go.work members.
# This is how Go packages get the same verb names without a package.json.
#
#   make install      install JS deps (pnpm) and sync the Go workspace
#   make gen          regenerate contracts (TS clients + Go server interfaces)
#   make build        build everything (runs gen first)
#   make dev          start dev servers (TS)
#   make test         run tests
#   make lint         lint TS (eslint) + Go (golangci-lint)
#   make typecheck    typecheck TS (tsc) + Go (go vet)
#   make license      add license headers to all in-scope sources
#   make license-check  fail if any in-scope source is missing a header
#   make tools        install pinned Go tools (golangci-lint)
#   make clean        remove build output and caches

SHELL := /bin/bash
ROOT := $(CURDIR)
GOBIN := $(shell go env GOPATH)/bin

# Workspace Go modules = modules whose dir lives under the repo root.
# Discovered dynamically from go.work, so adding a `use` line is the only edit
# needed to adopt a new Go module.
GO_MODULE_DIRS := $(shell go list -m -f '{{.Dir}}' 2>/dev/null | grep -F '$(ROOT)')

PNPM := pnpm
TURBO := $(PNPM) turbo
GOLANGCI := $(GOBIN)/golangci-lint

# golangci-lint must be built with a Go toolchain >= the modules' go directive
# (it refuses to analyze a newer-targeted module). `make tools` forces the
# project toolchain so the installed binary matches.
GOLANGCI_VERSION := v2.12.2
GO_TOOLCHAIN := go1.26.0

# addlicense applies the WSO2 Apache-2.0 header, picking the comment style per
# file type. Idempotent. Generated Go and vendored/build output are excluded.
ADDLICENSE := go run github.com/google/addlicense@v1.2.0
LICENSE_HEADER := .github/license-header.txt
LICENSE_FILES = $(shell git ls-files | \
	grep -E '\.(go|ts|tsx|sh)$$|(^|/)Dockerfile$$' | \
	grep -vE '\.gen\.go$$|_mock\.go$$|/mocks/|/node_modules/|/dist/|/generated/')

.PHONY: install gen build dev test lint typecheck license license-check tools clean eval

install:
	$(PNPM) install
	go work sync

gen:
	$(TURBO) run gen
	@for d in $(GO_MODULE_DIRS); do echo ">> go generate $$d"; ( cd "$$d" && go generate ./... ); done

build: gen
	$(TURBO) run build
	@for d in $(GO_MODULE_DIRS); do echo ">> go build $$d"; ( cd "$$d" && go build ./... ); done

dev:
	$(TURBO) run dev

test: gen
	$(TURBO) run test
	@for d in $(GO_MODULE_DIRS); do echo ">> go test $$d"; ( cd "$$d" && go test ./... ); done

# Model eval for @aep/agents (report-not-gate; spends tokens, skips without a key).
# Not a turbo task — kept out of the CI `test` graph.
eval:
	$(PNPM) --filter @aep/agents eval

lint:
	$(TURBO) run lint
	@for d in $(GO_MODULE_DIRS); do echo ">> golangci-lint $$d"; ( cd "$$d" && $(GOLANGCI) run ./... ); done

typecheck: gen
	$(TURBO) run typecheck
	@for d in $(GO_MODULE_DIRS); do echo ">> go vet $$d"; ( cd "$$d" && go vet ./... ); done

license:
	$(ADDLICENSE) -f $(LICENSE_HEADER) $(LICENSE_FILES)

license-check:
	$(ADDLICENSE) -check -f $(LICENSE_HEADER) $(LICENSE_FILES)

tools:
	GOTOOLCHAIN=$(GO_TOOLCHAIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

clean:
	$(TURBO) run build --force >/dev/null 2>&1 || true
	rm -rf .turbo
	find . -type d -name dist -prune -not -path './node_modules/*' -exec rm -rf {} + 2>/dev/null || true
	find . -type d -name .turbo -prune -not -path './node_modules/*' -exec rm -rf {} + 2>/dev/null || true
	@for d in $(GO_MODULE_DIRS); do ( cd "$$d" && go clean ./... ); done
