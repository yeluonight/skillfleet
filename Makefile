## SkillFleet root Makefile
##
## Conventions:
##   - All Go targets assume modules under github.com/yeluonight/skillfleet.
##   - All web targets shell out to `npm` inside apps/web (see ADR-0006).
##   - Phases land in order; when a target's source tree is still empty
##     (e.g. apps/agent before phase 2), the target prints a one-line
##     notice and exits 0. The notice goes away once sources arrive.
##   - Run `make help` to list available targets.

SHELL              := /usr/bin/bash
.SHELLFLAGS        := -eu -o pipefail -c
.ONESHELL:
.DEFAULT_GOAL      := help

GO                 ?= go
NPM                ?= npm

WEB_DIR            := apps/web
SERVER_PKG         := ./apps/server
AGENT_PKG          := ./apps/agent
BIN_DIR            := bin
SERVER_BIN         := $(BIN_DIR)/skillfleet-server
AGENT_BIN          := $(BIN_DIR)/skillfleet-agent

# has_go_sources(dir) — non-empty when the given directory contains at least
# one non-test, non-vendored .go file. Used both to filter GO_PKGS and to
# skip per-binary targets cleanly when their source root hasn't been
# populated yet.
#
# `=` recursive assignment so $(call ...) re-evaluates each invocation as
# sources land in subsequent phases. `find -quit` stops at the first match
# so the apps/web/node_modules subtree is never fully walked.
has_go_sources       = $(shell test -d $(1) && find $(1) -name '*.go' -not -name '*_test.go' -not -path '*/node_modules/*' -print -quit 2>/dev/null)

# Project Go packages.
#
# `./...` from the repo root descends into apps/web/node_modules/flatted/golang
# and any other Go subtree shipped inside an npm dep, so we list the roots
# explicitly. has_go_sources filters out roots that haven't been populated
# yet so `go test` / `go build` don't emit "matched no packages" warnings.
# Add new top-level Go package roots here when the codebase grows.
GO_PKG_ROOTS       := apps/server apps/agent internal migrations
GO_PKGS             = $(foreach r,$(GO_PKG_ROOTS),$(if $(call has_go_sources,$(r)),./$(r)/...))

GO_BUILD_FLAGS     ?= -trimpath
# VERSION is injected into both binaries' main.versionOverride. Defaults to
# `git describe` (tag-or-commit, +dirty) for local builds; CI overrides it
# with the pushed tag (make release VERSION=$GITHUB_REF_NAME).
VERSION            ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_LDFLAGS         ?= -s -w -X main.versionOverride=$(VERSION)

# Release matrix: the OS/ARCH pairs `make release` cross-compiles. SQLite is
# pure Go (modernc.org/sqlite) so every target builds with CGO_ENABLED=0 and
# no C toolchain. Edit here to add/drop platforms.
RELEASE_DIR        := dist/release
RELEASE_PLATFORMS  := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

## ----------------------------------------------------------------------------
## help
## ----------------------------------------------------------------------------

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage: make \033[36m<target>\033[0m\n\nTargets:\n"} \
		/^[a-zA-Z0-9_.-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## ----------------------------------------------------------------------------
## dev (concurrent) — server + web hot reload
## ----------------------------------------------------------------------------

.PHONY: dev
dev: ## Run server + web dev servers concurrently (requires phase 1 server).
	@echo "==> starting server + web (Ctrl-C to stop)"
	@trap 'kill 0' INT TERM EXIT; \
		$(MAKE) -j2 --no-print-directory dev-server dev-web

.PHONY: dev-server
dev-server: ## Run the Go server with file watching (phase 1+).
	$(GO) run $(SERVER_PKG)

.PHONY: dev-agent
dev-agent: ## Run the Go agent against the local server (phase 2+).
	$(GO) run $(AGENT_PKG)

.PHONY: dev-web
dev-web: ## Run the Vite dev server for apps/web.
	cd $(WEB_DIR) && $(NPM) run dev

## ----------------------------------------------------------------------------
## build — produce single-binary server + agent (with embedded web)
## ----------------------------------------------------------------------------

.PHONY: build
build: web-build web-embed server agent ## Build server + agent binaries with embedded WebUI.

.PHONY: server
server: $(BIN_DIR) ## Build the server binary.
	$(GO) build $(GO_BUILD_FLAGS) -ldflags '$(GO_LDFLAGS)' -o $(SERVER_BIN) $(SERVER_PKG)

.PHONY: agent
agent: $(BIN_DIR) ## Build the agent binary (no-op until phase 2 introduces sources).
ifeq ($(call has_go_sources,apps/agent),)
	@echo "agent: no Go sources yet (phase 2 t1 introduces apps/agent/main.go)"
else
	$(GO) build $(GO_BUILD_FLAGS) -ldflags '$(GO_LDFLAGS)' -o $(AGENT_BIN) $(AGENT_PKG)
endif

.PHONY: release
release: web-embed ## Cross-compile agent + server for all RELEASE_PLATFORMS into dist/release + SHA256SUMS.
	@echo "==> release $(VERSION) → $(RELEASE_DIR)"
	rm -rf $(RELEASE_DIR)
	mkdir -p $(RELEASE_DIR)
	for platform in $(RELEASE_PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		for comp in agent server; do \
			out="$(RELEASE_DIR)/skillfleet-$$comp-$$os-$$arch$$ext"; \
			echo "  building $$out"; \
			GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
				$(GO) build $(GO_BUILD_FLAGS) -ldflags '$(GO_LDFLAGS)' \
				-o "$$out" ./apps/$$comp; \
		done; \
	done
	( cd $(RELEASE_DIR) && sha256sum skillfleet-* > SHA256SUMS )
	@echo "==> wrote $$(ls $(RELEASE_DIR)/skillfleet-* | wc -l) binaries + SHA256SUMS into $(RELEASE_DIR)"


.PHONY: web-install
web-install: ## Install web dependencies (run once after clone).
	cd $(WEB_DIR) && $(NPM) install

.PHONY: web-build
web-build: ## Build the WebUI production bundle into apps/web/dist.
	cd $(WEB_DIR) && $(NPM) run build

# Sync the freshly-built bundle into the Go embed path. The committed
# dist/.gitignore keeps only index.html + .gitignore in version control,
# so wiping the directory before copy is safe — any leftover hashed
# asset from a previous build cycle gets pruned.
EMBED_DIR := internal/webui/embed/dist
.PHONY: web-embed
web-embed: web-build ## Copy apps/web/dist into the Go embed path.
	@echo "==> syncing WebUI bundle into $(EMBED_DIR)"
	find $(EMBED_DIR) -mindepth 1 ! -name '.gitignore' -delete
	cp -R $(WEB_DIR)/dist/. $(EMBED_DIR)/

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

## ----------------------------------------------------------------------------
## test + lint
## ----------------------------------------------------------------------------

.PHONY: test
test: test-go test-web ## Run Go tests + web tests.

.PHONY: test-go
test-go: ## Run Go tests over project packages.
	$(GO) test $(GO_PKGS)

.PHONY: test-web
test-web: ## Run web tests (placeholder until phase 1 t7 wires Vitest).
	cd $(WEB_DIR) && $(NPM) test --if-present

.PHONY: lint
lint: lint-go lint-web ## Run all linters.

.PHONY: lint-go
lint-go: ## Run golangci-lint over Go packages (requires the binary on PATH).
	golangci-lint run $(GO_PKGS)

.PHONY: lint-web
lint-web: ## Run ESLint over apps/web.
	cd $(WEB_DIR) && $(NPM) run lint

## ----------------------------------------------------------------------------
## maintenance
## ----------------------------------------------------------------------------

.PHONY: fixtures
fixtures: ## Rebuild adapter fixtures (phase 3+).
	@echo "fixtures target is wired in phase 3 — nothing to rebuild yet."

.PHONY: gc
gc: ## Trigger a package GC against the local server (dev only, phase 4+).
	@echo "gc target needs the server's admin API — available from phase 4 onward."

.PHONY: clean
clean: ## Remove build artifacts (bin/, apps/web/dist/, Go test cache when go is present).
	rm -rf $(BIN_DIR) $(WEB_DIR)/dist $(RELEASE_DIR)
	@command -v $(GO) >/dev/null 2>&1 && $(GO) clean -testcache || echo "skip go clean (toolchain not installed)"
