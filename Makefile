SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
  -X github.com/pr0ph37/mon/internal/shared/version.Version=$(VERSION) \
  -X github.com/pr0ph37/mon/internal/shared/version.Commit=$(COMMIT) \
  -X github.com/pr0ph37/mon/internal/shared/version.Date=$(DATE)

GOFLAGS_BASE := -trimpath -ldflags='$(LDFLAGS)'

BIN_DIR := bin

.PHONY: all build build-server build-agent build-all tidy test vet fmt clean \
        compose-up compose-down compose-logs

all: build

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
