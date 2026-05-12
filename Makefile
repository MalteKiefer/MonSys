SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w -buildid= \
  -X github.com/MalteKiefer/MonSys/internal/shared/version.Version=$(VERSION) \
  -X github.com/MalteKiefer/MonSys/internal/shared/version.Commit=$(COMMIT) \
  -X github.com/MalteKiefer/MonSys/internal/shared/version.Date=$(DATE)

GOFLAGS_BASE := -trimpath -ldflags='$(LDFLAGS)'

BIN_DIR := bin

.PHONY: all build build-server build-agent build-all tidy test vet fmt lint clean \
        web web-dev compose-up compose-down compose-logs install-hooks \
        generate-spec

all: web build

# Install local git hooks:
#   commit-msg                  -> blocks AI-attribution trailers
#   pre-commit (symlink to
#     pre-commit-signing-check) -> warns (does NOT block) when
#                                  commit.gpgsign is unset; the CI
#                                  commit-signing job is the actual
#                                  enforcement point. See
#                                  docs/COMMIT-SIGNING.md.
# Idempotent — re-running it is safe.
install-hooks:
	git config core.hooksPath .githooks
	@echo "core.hooksPath -> .githooks"
	@echo "hooks active: commit-msg, pre-commit (advisory signing check)"

# `make web` builds the SPA and stages it into the embed directory used by
# `internal/server/spa`. Run this before `make build-server` if you've
# changed anything under web/.
web:
	cd web && npm ci --no-audit --no-fund && npm run build
	rm -rf internal/server/spa/dist
	mkdir -p internal/server/spa/dist
	cp -r web/dist/. internal/server/spa/dist/

# Local dev: assumes mon-server is already running on :8080. Vite proxies
# /v1, /healthz, /readyz, /docs to it. Open http://localhost:5173.
# AUDIT 4.3.1.20: `npm ci` (not `npm install`) so a stale lockfile fails
# loudly rather than silently mutating package-lock.json under the dev's
# feet — matches the convention used by the `web` target above.
web-dev:
	cd web && npm ci --no-audit --no-fund && npm run dev

build: build-server build-agent

build-server:
	CGO_ENABLED=0 go build $(GOFLAGS_BASE) -o $(BIN_DIR)/mon-server ./cmd/mon-server

build-agent:
	CGO_ENABLED=0 go build $(GOFLAGS_BASE) -o $(BIN_DIR)/mon-agent ./cmd/mon-agent

build-all:
	@for arch in amd64 arm64; do \
	  echo ">>> linux/$$arch"; \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build $(GOFLAGS_BASE) -o $(BIN_DIR)/mon-server-linux-$$arch ./cmd/mon-server; \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build $(GOFLAGS_BASE) -o $(BIN_DIR)/mon-agent-linux-$$arch  ./cmd/mon-agent; \
	done

tidy:
	go mod tidy

test:
	go test ./...

vet:
	go vet ./...

# golangci-lint runs the curated set in .golangci.yml. Install locally with:
#   go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
# CI uses golangci/golangci-lint-action which manages its own binary cache.
lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w .

clean:
	rm -rf $(BIN_DIR)

compose-up:
	docker compose -f deploy/docker-compose.yaml up -d --build

compose-down:
	docker compose -f deploy/docker-compose.yaml down

compose-logs:
	docker compose -f deploy/docker-compose.yaml logs -f

# Regenerate api/openapi.yaml from the live API surface. huma emits keys
# in alphabetical order and a stable quote style, so no yq normalisation
# pass is needed — the previous pipe through mikefarah yq introduced
# environment-dependent quote-style drift (Python kislyuk/yq on most
# distros produced single-quoted $ref values, breaking the CI gate).
# Write to a temp file first and mv on success so a failed regen doesn't
# truncate the committed spec. AUDIT-203 / AUDIT-210.
generate-spec:
	@tmp=$$(mktemp) && \
		go run $(GOFLAGS_BASE) ./cmd/mon-server --print-spec > $$tmp && \
		mv $$tmp api/openapi.yaml
