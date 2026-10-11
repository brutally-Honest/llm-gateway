# Local gate (ADR 0001). `make verify` is what the pre-push hook runs.

GOLANGCI_LINT_VERSION := 2.13.2

BIN           := $(CURDIR)/bin
GOLANGCI_LINT := $(BIN)/golangci-lint
NOTES_REFSPEC := +refs/notes/*:refs/notes/*

# `dev` when git can't describe the tree, e.g. a Docker build without .git.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: setup setup-git setup-lint build run image test kit-test lint verify

# Idempotent: safe to run again at any time.
setup: setup-git setup-lint
	go mod download

setup-git:
	git config core.hooksPath .githooks
	git config notes.rewriteRef refs/notes/commits
	@git config --get-all remote.origin.fetch | grep -qxF '$(NOTES_REFSPEC)' || \
		git config --add remote.origin.fetch '$(NOTES_REFSPEC)'

# Installs the pinned binary into ./bin, not through the go.mod tool directive.
setup-lint:
	@if $(GOLANGCI_LINT) version 2>/dev/null | grep -q 'has version $(GOLANGCI_LINT_VERSION) '; then \
		echo 'golangci-lint $(GOLANGCI_LINT_VERSION) already installed'; \
	else \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/v$(GOLANGCI_LINT_VERSION)/install.sh | \
			sh -s -- -b $(BIN) v$(GOLANGCI_LINT_VERSION); \
		$(GOLANGCI_LINT) version 2>/dev/null | grep -q 'has version $(GOLANGCI_LINT_VERSION) ' || \
			{ echo 'golangci-lint install failed' >&2; exit 1; }; \
	fi

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=$(VERSION)" -o $(BIN)/gateway ./cmd/gateway

run: build
	$(BIN)/gateway

# The build context has no .git, so the version is passed in as a build arg.
image:
	VERSION=$(VERSION) docker compose build

test:
	go test -race ./...

# The agent-loop kit's shell tests; each runs in a throwaway git repo (needs jq).
kit-test:
	.claude/scripts/tests/run.sh

lint:
	@test -x $(GOLANGCI_LINT) || { echo 'golangci-lint not found: run make setup' >&2; exit 1; }
	$(GOLANGCI_LINT) run ./...

# Lint, then test, then the kit's tests, in that order even under -j.
verify:
	@$(MAKE) --no-print-directory lint
	@$(MAKE) --no-print-directory test
	@$(MAKE) --no-print-directory kit-test
