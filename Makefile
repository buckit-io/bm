SHELL := /bin/bash

BIN          := bm
PKG          := github.com/buckit-io/bm
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT       ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE         ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS      := -s -w \
                -X '$(PKG)/internal/version.Version=$(VERSION)' \
                -X '$(PKG)/internal/version.Commit=$(COMMIT)' \
                -X '$(PKG)/internal/version.Date=$(DATE)'

GO           ?= go
GOOS         ?= $(shell $(GO) env GOOS)
GOARCH       ?= $(shell $(GO) env GOARCH)

.PHONY: all build web build-all test lint tidy clean run help e2e-import e2e-up e2e-down

all: build

## build: compile the bm binary for the host platform
build: web
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/bm

## web: install deps and build the React frontend into web/dist
web:
	cd web && npm install && npm run build

## build-all: cross-compile binaries for all supported platforms into dist/
build-all: web
	@mkdir -p dist
	@for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do \
	    os=$${target%/*}; arch=$${target#*/}; \
	    ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
	    echo "==> $$os/$$arch"; \
	    GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
	        -o dist/$(BIN)-$$os-$$arch$$ext ./cmd/bm; \
	done

## test: run unit tests
test: web
	$(GO) test -race -count=1 ./...

## lint: run golangci-lint (installs to .bin if missing)
lint:
	@./scripts/lint.sh

## tidy: tidy go.mod
tidy:
	$(GO) mod tidy

## run: run bm web locally (host platform)
run: build
	./$(BIN) web

## e2e-import: run the Buckit import end-to-end test
e2e-import:
	bash ./integration-test/scripts/run-import.sh

## e2e-up: build and start the e2e lab without running browser tests
e2e-up: web
	bash ./integration-test/scripts/up-import.sh

## e2e-down: tear down the e2e lab
e2e-down:
	bash ./integration-test/scripts/down.sh

## clean: remove build artifacts
clean:
	rm -rf $(BIN) dist web/dist web/node_modules integration-test/playwright/node_modules integration-test/.generated

## help: print this message
help:
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
