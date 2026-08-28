.DEFAULT_GOAL := help
.PHONY: help format tidy lint vet test test-wasm test-browser cover check install-linters docs

# The targets that matter are `format` and `check`, and they mean the same
# thing here as in 0pcom/skywire, which is the reference for these repos.
# .golangci.yml is copied from there.

PROJECT_BASE := github.com/0magnet/plot-go
OPTS ?= GO111MODULE=on

# Whether the host checks run with cgo. 0 matches skywire, which is pure Go.
# Repos whose real build needs cgo — a GUI, audio, anything binding a C library
# — set this to 1, because linting them without it type-checks almost nothing.
CGO ?= 0

# Packages this toolchain cannot build at all — firmware for a different
# target, say. Not the same as code that merely does not build for this host:
# js/wasm is handled below by running the checks again in that context, which
# is the better answer whenever it is available. Empty in most repos.
SKIP ?=

# Directories rather than import paths, because golangci-lint resolves a bare
# import path against the working directory and then cannot find it.
#
# Listed with the same CGO_ENABLED the checks use. Listed with cgo on and
# linted with it off, a cgo-only package is named and then found to have no
# files in it, which fails the run rather than reporting anything about it.
#
# -e so that one package that cannot be listed does not empty the list. Without
# it, a single unbuildable package makes `go list` fail for the whole module,
# this comes back blank, and the run below reports that there is nothing to
# check — which reads exactly like passing.
PKGS = $(shell CGO_ENABLED=$(CGO) go list -e -f '{{.Dir}}' ./... 2>/dev/null $(if $(SKIP),| grep -vE '$(SKIP)'))
JSPKGS = $(shell CGO_ENABLED=0 GOOS=js GOARCH=wasm go list -e -f '{{.Dir}}' ./... 2>/dev/null $(if $(SKIP),| grep -vE '$(SKIP)'))

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-18s\033[0m %s\n", $$1, $$2}'

tidy: ## Tidy dependencies
	${OPTS} go mod tidy -v

format: tidy ## Format the code. Needs goimports (make install-linters)
	@if grep -qE '^(replace|exclude)' go.mod; then \
		echo "ERROR: go.mod contains replace or exclude directives which break go install @version"; \
		grep -E '^(replace|exclude)' go.mod; \
		exit 1; \
	fi
	${OPTS} goimports -w -local ${PROJECT_BASE} $(shell go list -f '{{.Dir}}' ./... 2>/dev/null | grep -v /vendor/)

lint: ## Run golangci-lint. Needs it installed (make install-linters)
	command -v golangci-lint || go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	golangci-lint --version
	@# Some of these repos are entirely js/wasm-tagged, so the host context has
	@# nothing in it and linting it is an error rather than a pass.
	@if [ -n "$(PKGS)" ]; then \
		CGO_ENABLED=$(CGO) ${OPTS} golangci-lint run -c .golangci.yml $(PKGS); \
	else \
		echo '--- nothing builds for this host; skipping the host pass'; \
	fi
	@# A host run cannot see js/wasm-tagged files, so anything only they use
	@# reads as dead — and anything wrong inside them is never checked at all.
	@if grep -rlq '^//go:build js' --include='*.go' . 2>/dev/null; then \
		echo '--- again in the js/wasm build context'; \
		CGO_ENABLED=0 GOOS=js GOARCH=wasm ${OPTS} golangci-lint run -c .golangci.yml $(JSPKGS); \
	fi

vet: ## Run go vet
	@if [ -n "$(PKGS)" ]; then \
		CGO_ENABLED=$(CGO) ${OPTS} go vet $(PKGS); \
	fi
	@if grep -rlq '^//go:build js' --include='*.go' . 2>/dev/null; then \
		CGO_ENABLED=0 GOOS=js GOARCH=wasm ${OPTS} go vet $(JSPKGS); \
	fi

test: ## Run tests. Falls back to the js/wasm ones where nothing builds here
	@if [ -n "$(PKGS)" ]; then \
		${OPTS} go test $(PKGS); \
	else \
		echo '--- nothing builds for this host; running the js/wasm tests instead'; \
		$(MAKE) --no-print-directory test-wasm; \
	fi

# The exec wrapper Go ships for running a js/wasm binary under Node. It is what
# makes `go test` work for code the host cannot build at all.
# The wrapper moved from misc/wasm to lib/wasm in Go 1.24, and CI may install
# either side of that, so both are tried. Empty means this toolchain cannot
# run js/wasm tests at all.
WASMEXEC = $(shell for d in lib misc; do p="$$(go env GOROOT)/$$d/wasm/go_js_wasm_exec"; if [ -x "$$p" ]; then echo "$$p"; break; fi; done)

test-wasm: ## Run the js/wasm tests under Node
	@# One shell for the whole target. An `exit 0` in a recipe line of its own
	@# ends that line and nothing else, so the earlier form announced it was
	@# skipping and then ran the tests regardless, against the exec wrapper it
	@# had just reported missing.
	@# Node has no DOM: document, window and requestAnimationFrame are all
	@# undefined. JS core is there, and a fake document can be installed from
	@# Go with js.Global().Set, which is how the DOM-facing code is covered.
	@if [ -z "$(WASMEXEC)" ]; then \
		echo 'no js/wasm exec wrapper in this Go installation; skipping'; \
	elif ! command -v node >/dev/null; then \
		echo 'node is not installed; skipping'; \
	elif [ -n "$(JSPKGS)" ]; then \
		CGO_ENABLED=0 GOOS=js GOARCH=wasm ${OPTS} go test -exec="$(WASMEXEC)" $(JSPKGS); \
	else \
		echo 'no js/wasm packages'; \
	fi

# Running the js/wasm tests in a real browser instead of Node. Node has no DOM;
# a browser has one, plus canvas and WebGL. Tests that build a fake DOM should
# detect a real one and use it, so the same tests run both ways — anything the
# fake gets wrong then shows up as a test that passes under Node and fails here.
#
# CHROME may name any Chrome-compatible binary. Brave is one. Not called BROWSER:
# that is a conventional environment variable (xdg uses it) and ?= would take
# whatever it happens to be set to — firefox, on this machine.
CHROME ?= $(shell command -v google-chrome chromium chromium-browser brave 2>/dev/null | head -1)

test-browser: ## Run the js/wasm tests in a headless browser (needs wasmbrowsertest)
	@command -v wasmbrowsertest >/dev/null || { \
		echo 'wasmbrowsertest is not installed; go install github.com/agnivade/wasmbrowsertest@latest'; exit 0; }
	@[ -n "$(CHROME)" ] || { echo 'no Chrome-compatible browser found (set CHROME=); skipping'; exit 0; }
	@if [ -z "$(JSPKGS)" ]; then echo 'no js/wasm packages'; exit 0; fi
	@# chromedp looks for a binary called google-chrome and does not pass
	@# --no-sandbox, which some environments require. A shim supplies both.
	@d=$$(mktemp -d); printf '#!/bin/sh\nexec %s --no-sandbox "$$@"\n' '$(CHROME)' > $$d/google-chrome; \
		chmod +x $$d/google-chrome; \
		PATH=$$d:$$PATH CGO_ENABLED=0 GOOS=js GOARCH=wasm ${OPTS} \
			go test -exec=wasmbrowsertest $(JSPKGS); \
		rc=$$?; rm -rf $$d; exit $$rc

# Nothing here is host-specific — no cgo, no syscalls beyond os — so it should
# build wherever Go does. These targets are what says so rather than assumes it.
test-tinygo: ## Run the tests under TinyGo, and build the wasm targets
	@command -v tinygo >/dev/null || { echo 'tinygo is not installed; skipping'; exit 0; }
	@# TinyGo trails each new Go release by some weeks; until it catches up,
	@# building against the newer Go fails outright. The helper reports the
	@# newest Go this TinyGo accepts, or "auto" once the system one will do.
	@# Resolved once and reused, so the three commands agree and ask once.
	TC=$$(sh scripts/tinygo-toolchain.sh); \
		GOTOOLCHAIN=$$TC tinygo test ./... && \
		GOTOOLCHAIN=$$TC tinygo build -target=wasip1 -o /dev/null ./cmd/plot-go && \
		GOTOOLCHAIN=$$TC tinygo build -target=wasm -o /dev/null ./cmd/plot-go

build-wasm: ## Check the wasm targets build with the Go toolchain
	CGO_ENABLED=0 GOOS=js GOARCH=wasm go build -o /dev/null ./cmd/plot-go
	CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm go build -o /dev/null ./cmd/plot-go

cover: ## Report test coverage per package
	@if [ -n "$(PKGS)" ]; then \
		CGO_ENABLED=$(CGO) ${OPTS} go test -cover $(PKGS) 2>&1 | grep -v '^ok.*no test files'; \
	fi

check: lint vet test ## Run linters, vet and tests

install-linters: ## Install the linters
	${OPTS} go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	${OPTS} go install golang.org/x/tools/cmd/goimports@latest

docs: ## Regenerate the dependency graph and code counts in the README
	./gendocs.sh
