# Local gate (ADR 0001). `make verify` is what the pre-push hook runs.

GOLANGCI_LINT_VERSION := 2.13.2

BIN           := $(CURDIR)/bin
GOLANGCI_LINT := $(BIN)/golangci-lint
NOTES_REFSPEC := +refs/notes/*:refs/notes/*

# `dev` when git can't describe the tree, e.g. a source tarball without .git.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: setup setup-git setup-lint setup-shellcheck setup-node build run dist test web-test lint verify \
	observability-up observability-down

# Idempotent: safe to run again at any time.
setup: setup-git setup-lint setup-shellcheck setup-node
	go mod download

setup-git:
	git config core.hooksPath .githooks
	git config notes.rewriteRef refs/notes/commits
	@git config --get-all remote.origin.fetch | grep -qxF '$(NOTES_REFSPEC)' || \
		git config --add remote.origin.fetch '$(NOTES_REFSPEC)'

# make test (so make verify) lints install.sh with shellcheck (spec 023 AC23); setup only
# reports whether it is installed, because it comes from the system package manager.
setup-shellcheck:
	@if command -v shellcheck >/dev/null 2>&1; then \
		echo "shellcheck $$(shellcheck --version | sed -n 's/^version: //p') found (make test lints install.sh with it)"; \
	else \
		echo 'shellcheck not found: install it (apt install shellcheck, or brew install shellcheck) — make test and make verify fail without it'; \
	fi

# make web-test (so make verify) runs the web client's tests with node (ADR 0013, 22.12 or
# later); setup only reports whether it is installed.
setup-node:
	@if command -v node >/dev/null 2>&1; then \
		echo "node $$(node --version) found (make web-test needs 22.12 or later)"; \
	else \
		echo 'node not found: install node 22.12 or later — make web-test and make verify fail without it'; \
	fi

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

# The release assets install.sh downloads (spec 023, research "Release assets"):
# dist/llm-gateway_<version>_<os>_<arch>.tar.gz, each holding `gateway`, and
# dist/SHA256SUMS. <version> is VERSION without a leading v. A release publishes exactly
# these files.
DIST         := $(CURDIR)/dist
DIST_VERSION := $(patsubst v%,%,$(VERSION))
DIST_TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

dist:
	@rm -rf $(DIST) && mkdir -p $(DIST)
	@for t in $(DIST_TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; work=$$(mktemp -d) || exit 1; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-X main.version=$(VERSION)" \
			-o $$work/gateway ./cmd/gateway || { rm -rf $$work; exit 1; }; \
		tar -czf $(DIST)/llm-gateway_$(DIST_VERSION)_$${os}_$${arch}.tar.gz -C $$work gateway || { rm -rf $$work; exit 1; }; \
		rm -rf $$work; \
	done
	@cd $(DIST) && if command -v sha256sum >/dev/null 2>&1; then sha256sum llm-gateway_*.tar.gz; \
		else shasum -a 256 llm-gateway_*.tar.gz; fi > SHA256SUMS
	@cat $(DIST)/SHA256SUMS

# The self-observability stack (spec 021, docs/observability.md): Prometheus, Grafana and
# Jaeger on 127.0.0.1, for a gateway running on the host. On Linux the override puts
# Prometheus and Grafana on the host's network, where the gateway's loopback is.
OBS_COMPOSE := docker compose -f deploy/observability/compose.yml \
	$(if $(filter Linux,$(shell uname -s)),-f deploy/observability/compose.linux.yml)

observability-up:
	$(OBS_COMPOSE) up -d

observability-down:
	$(OBS_COMPOSE) down

test:
	go test -race ./...

# The web client's tests run on node's built-in runner (ADR 0013). The glob is quoted so
# node expands it: the directory form exits 1 on node v25.
web-test:
	@command -v node >/dev/null 2>&1 || { echo 'node not found: install node 22.12 or later (ADR 0013)' >&2; exit 1; }
	# Locale and time zone pinned, so date and time text is the same on every machine (Node
	# takes its default locale from LC_ALL/LANG; spec 024 AC6, macos-v1.md B6).
	LC_ALL=en_US.UTF-8 TZ=UTC node --test "internal/ui/webtest/*.test.js"

lint:
	@test -x $(GOLANGCI_LINT) || { echo 'golangci-lint not found: run make setup' >&2; exit 1; }
	$(GOLANGCI_LINT) run ./...

# Lint, then test, then the web tests, in that order even under -j.
verify:
	@$(MAKE) --no-print-directory lint
	@$(MAKE) --no-print-directory test
	@$(MAKE) --no-print-directory web-test
