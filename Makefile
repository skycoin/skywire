
.PHONY : check lint install-linters dep test lint-extra
.PHONY : update-deps update-dmsg update-skycoin push-deps
.PHONY : build clean install format  bin build-race wasm-visor tpviz-gl embed-wasm-visor embed-wasm-visor-tinygo prune-wasm-embed-history
.PHONY : build-mobile android-mobile android-mobile-check android-mobile-ndk android-apk android-apk-debug
.PHONY : generate services vet check-cg check-help check-inner check-ci
.PHONY : e2e-build e2e-run e2e-test e2e-stop e2e-clean e2e-skychat

VERSION := $(shell git describe --always)
RFC_3339 := "+%Y-%m-%dT%H:%M:%SZ"
COMMIT := $(shell git rev-list -1 HEAD)

PROJECT_BASE := github.com/skycoin/skywire
SKYWIRE_UTILITIES_BASE := github.com/skycoin/skywire/pkg/skywire-utilities
ifeq ($(OS),Windows_NT)
	SHELL := pwsh
	OPTS?=powershell -Command setx GO111MODULE on;
	DATE := $(shell powershell -Command date -u ${RFC_3339})
	.DEFAULT_GOAL := help-windows
else
	SHELL := /usr/bin/env bash
	OPTS?=GO111MODULE=on
	DATE := $(shell date -u $(RFC_3339))
	.DEFAULT_GOAL := help
endif

ifeq ($(VERSION),)
	VERSION = unknown
endif

ifeq ($(COMMIT),)
	COMMIT = unknown
endif

SYSTRAY_CGO_ENABLED := 1

ifeq ($(BUILDTAG),)
	ifeq ($(OS),Windows_NT)
		BUILDTAG = Windows
	else
		UNAME_S := $(shell uname -s)
		ifeq ($(UNAME_S),Linux)
			BUILDTAG = Linux
			SYSTRAY_CGO_ENABLED := 0
		endif
	 	ifeq ($(UNAME_S),Darwin)
			BUILDTAG = Darwin
			export CGO_CFLAGS := -Wno-deprecated-declarations
		endif
	endif
endif

STATIC_OPTS?= $(OPTS) CC=musl-gcc
MANAGER_UI_DIR = static/skywire-manager-src
MANAGER_UI_BUILT_DIR=pkg/visor/static
DOCKER_COMPOSE_FILE:=./docker/docker-compose.yml
DOCKER_REGISTRY:=skycoin

# -timeout is go test's PER-PACKAGE deadline: a deadlock backstop, not a
# budget. A package that passes is unaffected by raising it.
#
# 5m was tight enough to BE the failure. cmd/apps/skychat/group runs
# ~270 tests, a few dozen of them full dmsg round trips, and takes ~12.5
# min in aggregate; crossing 5m panicked the package with "test timed
# out" naming whichever 1s test happened to be running when the alarm
# fired. That reads exactly like a hang and isn't one. 25m keeps real
# headroom over that 12.5 min on a loaded runner.
#
# Race overhead doesn't scale this package the way it scales CPU-bound
# ones — its wall clock is dominated by fixed network timeouts — so the
# -race CI run measures about the same as a local run without it.
TEST_OPTS:=-cover -timeout=25m -mod=vendor -tags no_ci

GOARCH:=$(shell go env GOARCH)

ifneq (,$(findstring 64,$(GOARCH)))
    TEST_OPTS:=$(TEST_OPTS) -race
endif

BUILDINFO_PATH := $(SKYWIRE_UTILITIES_BASE)/pkg/buildinfo
BUILD_PATH := ./build/

BUILDINFO_VERSION := -X $(BUILDINFO_PATH).version=$(VERSION)
BUILDINFO_DATE := -X $(BUILDINFO_PATH).date=$(DATE)
BUILDINFO_COMMIT := -X $(BUILDINFO_PATH).commit=$(COMMIT)
BUILDTAGINFO := -X $(PROJECT_BASE)/pkg/visor.BuildTag=$(BUILDTAG)

BUILDINFO?=$(BUILDINFO_VERSION) $(BUILDINFO_DATE) $(BUILDINFO_COMMIT) $(BUILDTAGINFO)
INFO?=$(VERSION) $(DATE) $(COMMIT) $(BUILDTAG)

# The wasm-visor imports skywire's OWN pkg/buildinfo (not skywire-utilities), so
# it needs its own -X targets, stamped explicitly below.
#
# The embedded blob is a build artifact carried in the repository, so it can
# never be verified by rebuilding: the commit that compiles it always precedes
# the commit that carries it, and the date stamped below makes two builds of
# identical source differ anyway. The stamp is the only account of what it was
# built from, so WASM_VERSION uses --dirty: a blob built from an uncommitted
# tree says so, in a string TestCommittedWasmBuiltFromCommit can find.
#
# It is stamped here as well as by Go, not instead of it. `go version -m` cannot
# parse wasm, but that is a limitation of that command rather than of the
# format: Go writes vcs.revision/vcs.modified into a GOOS=js binary as plain
# text, and skycoin reads them straight out of its own js/wasm cipher in
# ci-scripts/check-wasm-version.js. The committed std-Go blob here carries none
# only because it was built while this file still passed -buildvcs=false; once
# it is regenerated it will, and the test can require it of both artifacts
# rather than of the TinyGo one alone.
#
# -buildvcs=false was previously passed on the std-Go build to keep a "+dirty"
# suffix out of the version of a blob built from an uncommitted tree. It is gone:
# suppressing the marker removed the evidence rather than the problem, and it
# also removed the vcs records the test would otherwise be able to require.
# The repo's OWN pkg/buildinfo (vs skywire-utilities' above). Despite the
# historical WASM_ prefix there is nothing wasm about the path — the wasm-visor
# was simply the first target that needed to stamp it. The skywire-mobile
# targets stamp it too (MOBILE_APPINFO below): /api/about + cobra --version
# read this package.
SKYWIRE_BUILDINFO_PATH := $(PROJECT_BASE)/pkg/buildinfo
WASM_BUILDINFO_PATH := $(SKYWIRE_BUILDINFO_PATH)
# Unlike VERSION, this carries --dirty, so a committed artifact built from an
# uncommitted tree is detectable afterwards. See the note above.
WASM_VERSION := $(shell git describe --always --dirty)
WASM_BUILDINFO := -X $(WASM_BUILDINFO_PATH).version=$(WASM_VERSION) -X $(WASM_BUILDINFO_PATH).commit=$(COMMIT) -X $(WASM_BUILDINFO_PATH).date=$(DATE)

BUILD_OPTS?="-ldflags=$(BUILDINFO)" -mod=vendor
BUILD_OPTS_DEPLOY?="-ldflags=$(BUILDINFO) -w -s"

export COMPOSE_FILE=${DOCKER_COMPOSE_FILE}
export REGISTRY=${DOCKER_REGISTRY}

buildinfo:
	@echo $(INFO)

version:
	@echo $(VERSION)

date:
	@echo $(DATE)

commit:
	@echo $(COMMIT)

services: ## update services-config.json
	scripts/services.sh

dig-services: ## show IP addresses for the services
	scripts/dig-services.sh

dmsghttp: ## update dmsghttp-config.json
	scripts/dmsghttp.sh

dmsg-servers: ## update embedded dmsg_servers in deployment/services-config.json from the discovery, over dmsg
	go run . dmsg conf pull
	go generate ./deployment/

check: ## Run linters and tests (lint, check-cg, check-help, test). Serialized via flock so two concurrent invocations don't trigger overlapping parallel test runs that pin every core and make the machine unresponsive.
	@if command -v flock >/dev/null 2>&1; then \
		exec 9>.make-check.lock; \
		if ! flock -n 9; then \
			echo "another 'make check' is already holding .make-check.lock; waiting for it to finish..." >&2; \
			flock 9; \
		fi; \
		$(MAKE) --no-print-directory -f $(firstword $(MAKEFILE_LIST)) check-inner; \
	else \
		echo "WARNING: 'flock' not available; running unguarded. install util-linux's flock to enable serialization." >&2; \
		$(MAKE) --no-print-directory -f $(firstword $(MAKEFILE_LIST)) check-inner; \
	fi

check-inner: lint check-cg check-help test ## Internal: the actual check targets, run inside flock by 'make check'. Don't call directly unless you've already acquired .make-check.lock.

check-cg: ## Cursory check of the main help menu, offline dmsghttp config gen and offline config gen
	@echo "checking dmsghttp offline config gen"
	@echo
	go run . cli config gen -f --nofetch -dnw
	@echo
	@echo "checking offline config gen"
	@echo
	go run . cli config gen -f --nofetch -nw
	@echo
	@echo "config gen succeeded without error"
	@echo

check-help: ## Cursory check of the help menus
	@echo "checking help menu for compilation without errors"
	go run . --help
	@echo "compilation successful"

vet: ## Run go vet (the non-golangci-lint half of 'lint')
	CGO_ENABLED=0 ${OPTS} go vet -mod=vendor -tags 'withoutsystray withoutgotop' ./...

check-ci: vet check-cg check-help test ## CI check: like 'check' but skips golangci-lint (CI runs it as a separate pinned, cached step). Keeps go vet + config-gen smoke + tests.

check-windows: lint-windows test-windows ## Run linters and tests on windows image

build: clean build-merged ## Install dependencies, build apps and binaries. `go build` with ${OPTS}

print-build: ## Print the command used to build the binary
	@echo "${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)skywire ./cmd/skywire"

print-run-source: ## Print the command used to go run from source
	@echo "${OPTS} go run ${BUILD_OPTS} . cli config gen -n | sudo ${OPTS} go run ${BUILD_OPTS} . visor -n || true"

build-merged: ## Install dependencies, build apps and binaries. `go build` with ${OPTS}
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)skywire .

build-merged-cgo: ## Build with CGO optimization for faster DMSG handshakes (requires libsecp256k1-dev)
	${OPTS} go build -tags=cgo ${BUILD_OPTS} -o $(BUILD_PATH)skywire .

# ---- skywire-mobile: the lite multicall core for the Android app ----------
# One binary: visor + cli `config` subtree + the 4 client apps (in-proc via the
# launcher registry). The `mobile` tag strips the embedded desktop assets
# (geoip db 30 MB, manager UI 6.8 MB, vendored browser wallet 11 MB, tpviz
# legacy 2.8 MB). Output lands in the android project's jniLibs so Android
# Studio picks it up directly. -checklinkname=0 is REQUIRED on GOOS=android:
# the vendored wlynxg/anet uses //go:linkname into net (Go ≥1.23 blocks it).
ANDROID_JNILIBS := android/app/src/main/jniLibs/arm64-v8a
MOBILE_TAGS := mobile,withoutsystray
# Size budget for the android payload, in bytes (80 MB). The CI lane fails
# over it so the lite variant can't silently rot or regain the stripped fat.
ANDROID_MOBILE_MAX_BYTES := 83886080

# Both buildinfo paths are stamped: the visor internals read skywire-utilities'
# buildinfo ($(BUILDINFO)), but /api/about + cobra --version — what the phone
# app's info card shows — read the repo-local pkg/buildinfo. That version MUST
# parse as semver (visorconfig.Parse semver-checks it and FATALs otherwise), and
# on a checkout with no reachable tag `git describe --always` is a bare hash —
# so fall back to a v0.0.0-<sha> pseudo-version.
MOBILE_VERSION := $(shell git describe --tags 2>/dev/null || echo "v0.0.0-$(VERSION)")
MOBILE_APPINFO := -X $(SKYWIRE_BUILDINFO_PATH).version=$(MOBILE_VERSION) -X $(SKYWIRE_BUILDINFO_PATH).commit=$(COMMIT) -X $(SKYWIRE_BUILDINFO_PATH).date=$(DATE)
build-mobile: ## Build the skywire-mobile lite core for the HOST OS (desktop smoke of the mobile build variant)
	${OPTS} go build -tags $(MOBILE_TAGS) "-ldflags=$(BUILDINFO) $(MOBILE_APPINFO)" -mod=vendor -o $(BUILD_PATH)skywire-mobile ./cmd/skywire-mobile

android-mobile: ## Build libskywire-mobile.so (android/arm64) into android jniLibs — pure-Go lane (CI/emulator); use android-mobile-ndk for release (bionic DNS)
	mkdir -p $(ANDROID_JNILIBS)
	GOOS=android GOARCH=arm64 CGO_ENABLED=0 ${OPTS} go build -tags $(MOBILE_TAGS) "-ldflags=$(BUILDINFO) $(MOBILE_APPINFO) -w -s -checklinkname=0" -mod=vendor -o $(ANDROID_JNILIBS)/libskywire-mobile.so ./cmd/skywire-mobile
	@ls -la $(ANDROID_JNILIBS)/libskywire-mobile.so

android-mobile-check: android-mobile ## CI lane: android-mobile + fail over the size budget
	@size=$$(wc -c < $(ANDROID_JNILIBS)/libskywire-mobile.so | tr -d '[:space:]'); \
	echo "libskywire-mobile.so: $$size bytes (budget $(ANDROID_MOBILE_MAX_BYTES))"; \
	if [ "$$size" -gt "$(ANDROID_MOBILE_MAX_BYTES)" ]; then \
		echo "ERROR: libskywire-mobile.so exceeds the size budget — the mobile variant regained fat"; \
		exit 1; \
	fi

android-mobile-ndk: ## Release lane: NDK/cgo android build (DNS via bionic getaddrinfo); requires ANDROID_NDK_HOME
	@set -e; \
	test -n "$(ANDROID_NDK_HOME)" || { echo "ANDROID_NDK_HOME is not set"; exit 1; }; \
	CC_BIN=$$(ls $(ANDROID_NDK_HOME)/toolchains/llvm/prebuilt/*/bin/aarch64-linux-android26-clang 2>/dev/null | head -n1); \
	test -n "$$CC_BIN" || { echo "aarch64-linux-android26-clang not found under ANDROID_NDK_HOME"; exit 1; }; \
	mkdir -p $(ANDROID_JNILIBS); \
	GOOS=android GOARCH=arm64 CGO_ENABLED=1 CC="$$CC_BIN" go build -tags $(MOBILE_TAGS) "-ldflags=$(BUILDINFO) $(MOBILE_APPINFO) -w -s -checklinkname=0" -mod=vendor -o $(ANDROID_JNILIBS)/libskywire-mobile.so ./cmd/skywire-mobile; \
	ls -la $(ANDROID_JNILIBS)/libskywire-mobile.so

# Android Studio's bundled JDK (gradle needs JDK 17+); override if yours differs.
ANDROID_JAVA_HOME ?= /Applications/Android Studio.app/Contents/jbr/Contents/Home

# APK_VERSION=X.Y.Z stamps the build; without it the committed gradle
# fallbacks apply. The versionCode formula lives HERE and nowhere else — the
# release workflow calls this target rather than computing its own — so a tag
# build and a local build can never disagree about the number. Minor and patch
# are capped at 99, past which the scheme stops being monotonic.
ifdef APK_VERSION
APK_VERSION_CODE := $(shell printf '%s' '$(APK_VERSION)' | awk -F. \
	'{ if (NF==3 && $$1$$2$$3 ~ /^[0-9]+$$/ && $$2<=99 && $$3<=99) print $$1*10000+$$2*100+$$3 }')
APK_GRADLE_ARGS := -PskywireVersionName=$(APK_VERSION) -PskywireVersionCode=$(APK_VERSION_CODE)
endif

android-apk: ## Build the Android APK (release; APK_VERSION=X.Y.Z to stamp it; signed when ANDROID_KEYSTORE_FILE/_PASSWORD + ANDROID_KEY_ALIAS/_PASSWORD are set, else unsigned) — run android-mobile-ndk first for a fresh Go payload
	@if [ -n "$(APK_VERSION)" ] && [ -z "$(APK_VERSION_CODE)" ]; then \
		echo "APK_VERSION='$(APK_VERSION)' is not X.Y.Z with minor/patch <= 99"; exit 1; fi
	@if [ "$(APK_VERSION_CODE)" = "0" ]; then \
		echo "APK_VERSION=0.0.0 derives versionCode 0, and Android requires a"; \
		echo "positive integer — the lowest usable version is 0.0.1."; exit 1; fi
	@if [ -n "$(APK_VERSION)" ]; then \
		echo "version: $(APK_VERSION) (versionCode $(APK_VERSION_CODE))"; fi
	@if [ -n "$$ANDROID_KEYSTORE_FILE" ]; then \
		echo "signing with $$ANDROID_KEYSTORE_FILE"; \
	else \
		echo "ANDROID_KEYSTORE_FILE unset — building UNSIGNED (app-release-unsigned.apk)"; fi
	cd android && JAVA_HOME="$(ANDROID_JAVA_HOME)" ./gradlew assembleRelease $(APK_GRADLE_ARGS)

android-apk-debug: ## Build + the debug-signed APK (installable via adb) — the dev loop's CLI twin of Android Studio Run
	cd android && JAVA_HOME="$(ANDROID_JAVA_HOME)" ./gradlew assembleDebug

build-merged-windows: clean-windows
	powershell '${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)skywire.exe .'

install-system-linux: build ## Install apps and binaries over those provided by the linux package - linux package must be installed first!
	sudo echo "sudo cache"
	sudo install -Dm755 $(BUILD_PATH)skywire /opt/skywire/bin/

install-generate: ## Installs required execs for go generate.
	${OPTS} go install github.com/vektra/mockery/v2@latest

	## TO DO: it may be unnecessary to install required execs for go generate into the path. An alternative method may exist which does not require this
	## https://eli.thegreenplace.net/2021/a-comprehensive-guide-to-go-generate

generate: ## Generate mocks and config README's
	go generate ./...

doc-gen: ## Regenerate docs/skywire/ from the live cobra command tree
	go run ./cmd/skywire doc --out docs/skywire

docs-prepare: ## Stage external doc trees (specs/, rewards/, cmd/apps/, cmd/dmsg/, cmd/svc/) under docs/ for MkDocs
	bash scripts/docs-prepare.sh

docs-serve: docs-prepare ## Local preview of the MkDocs site at http://127.0.0.1:8000
	mkdocs serve

docs-build: docs-prepare ## Build the MkDocs site into ./site (mirrors CI)
	mkdocs build

docs-install-deps: ## Install MkDocs + plugins (run once per machine)
	pip install --user 'mkdocs-material==9.5.*' 'mkdocs-awesome-pages-plugin==2.9.*' 'pymdown-extensions==10.*'

clean: ## Clean project: remove created binaries and apps
	-rm -rf ./build ./local

build-wasm: ## Compile-check every js/wasm binary (GOOS=js GOARCH=wasm), no run — mirrors the CI wasm lane
	@echo "compile-checking js/wasm binaries..."
	@for p in ./pkg/tpviz/wasm ./cmd/dmsg-wasm ./cmd/wasm-visor ./cmd/wasm-visor-probe ./cmd/websh-probe ./cmd/skywire/commands/web/wasm; do \
		echo "  GOOS=js GOARCH=wasm go build $$p"; \
		GOOS=js GOARCH=wasm go build -mod=vendor -o /dev/null "$$p" || exit 1; \
	done
	@echo "all js/wasm binaries compile."

build-wasm-tinygo: ## Compile-check every TinyGo wasm binary (-o /dev/null, no run) — mirrors the CI wasm-tinygo lane
	@command -v tinygo >/dev/null 2>&1 || { echo "tinygo not installed — see docs/design/tinygo-dmsg-client.md (TinyGo 0.41+)"; exit 1; }
	@echo "compile-checking TinyGo wasm binaries..."
	@echo "  tinygo build -target wasip1 ./cmd/dmsg-tinygo-probe"
	@tinygo build -target wasip1 -no-debug -opt=z -o /dev/null ./cmd/dmsg-tinygo-probe || exit 1
	@# Only net/http-free binaries belong here: TinyGo 0.41 cannot compile net/http
	@# for wasm (roundtrip_js.go references an unexported Transport.roundTrip). The
	@# full js/wasm binaries ./cmd/dmsg-wasm and ./cmd/wasm-visor deliberately use
	@# net/http (HTTP-over-dmsg; dmsg-wasm also needs logrus+gob reflection) and are
	@# standard-Go-only — they are compile-checked by the `build-wasm` lane instead.
	@for p in ./pkg/tpviz/wasm ./cmd/websh-probe; do \
		echo "  tinygo build -target wasm $$p"; \
		tinygo build -target wasm -no-debug -o /dev/null "$$p" || exit 1; \
	done
	@echo "  tinygo build -target wasi (app-mux routing policy)"
	@cd docs/examples/routing-policies/wasm/app-mux && tinygo build -target=wasi -no-debug -opt=2 -o /dev/null . || exit 1
	@echo "all TinyGo wasm binaries compile."

test-wasm-policy: ## Rebuild app-mux.wasm from source with TinyGo and run the policy loader test against the FRESH build (real WASM runtime smoke test)
	@command -v tinygo >/dev/null 2>&1 || { echo "tinygo not installed — see docs/examples/routing-policies/wasm/README.md (TinyGo 0.32+)"; exit 1; }
	@mkdir -p ./build/wasm-fixtures
	cd docs/examples/routing-policies/wasm/app-mux && tinygo build -target=wasi -no-debug -opt=2 -o "$(CURDIR)/build/wasm-fixtures/app-mux.wasm" .
	SKYWIRE_APPMUX_WASM="$(CURDIR)/build/wasm-fixtures/app-mux.wasm" go test -mod=vendor -count=1 -v -run TestWasmEvaluator ./pkg/router/policy/wasm/

tpviz-wasm: ## Build transport visualizer WASM binary into pkg/tpviz/dist for embedding
	GOOS=js GOARCH=wasm go build -o ./pkg/tpviz/dist/main.wasm ./pkg/tpviz/wasm
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" ./pkg/tpviz/dist/

tpviz-wasm-standalone: ## Build transport visualizer WASM binary to build/tpviz (standalone)
	mkdir -p ./build/tpviz
	GOOS=js GOARCH=wasm go build -o ./build/tpviz/main.wasm ./pkg/tpviz/wasm
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" ./build/tpviz/
	cp ./pkg/tpviz/dist/index.html ./build/tpviz/

tpviz-wasm-tinygo: ## Build transport visualizer WASM binary with tinygo (smaller, ~750KB)
	tinygo build -o ./pkg/tpviz/dist/main.wasm -target wasm -no-debug -opt=z -panic=trap ./pkg/tpviz/wasm
	cp "$$(tinygo env TINYGOROOT)/targets/wasm_exec.js" ./pkg/tpviz/dist/

tinygo-dmsg: ## Build-check the dmsg client under TinyGo (IoT target wasip1); ~2.2MB -opt=z
	tinygo build -target wasip1 -no-debug -opt=z -o ./build/dmsg-tinygo.wasm ./cmd/dmsg-tinygo-probe
	@echo "built ./build/dmsg-tinygo.wasm — the dmsg client compiles under TinyGo (see docs/design/tinygo-dmsg-client.md)"

dmsg-wasm: ## Build the browser WASM dmsg client + dev harness into build/dmsg-wasm
	mkdir -p ./build/dmsg-wasm
	GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o ./build/dmsg-wasm/dmsg.wasm ./cmd/dmsg-wasm
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" ./build/dmsg-wasm/
	cp ./cmd/dmsg-wasm/index.html ./build/dmsg-wasm/
	@echo "built ./build/dmsg-wasm — serve it: 'go run cmd/dmsg-wasm/serve.go' then open http://localhost:8085/"

tinygo-dmsg-wasm: ## Build the browser WASM dmsg client with TinyGo (~6.5MB vs ~21MB) into build/dmsg-wasm
	mkdir -p ./build/dmsg-wasm
	tinygo build -target wasm -o ./build/dmsg-wasm/dmsg.wasm ./cmd/dmsg-wasm
	cp "$$(tinygo env TINYGOROOT)/targets/wasm_exec.js" ./build/dmsg-wasm/
	cp ./cmd/dmsg-wasm/index.html ./build/dmsg-wasm/
	@echo "built ./build/dmsg-wasm (TinyGo) — serve it: 'go run cmd/dmsg-wasm/serve.go' then open http://localhost:8085/"

# TINYGO points at the TinyGo binary. The FULL wasm-visor (net/http + crypto/tls)
# needs the fork at github.com/0magnet/tinygo (v0.42.0-skycoin.1+, LLVM 22) — its
# net/http-on-js support is what unblocks the browser visor. Build the fork with
# `go build -tags llvm22 -o build/tinygo .` and point TINYGO at it + export
# TINYGOROOT=<fork checkout>. Stock upstream TinyGo cannot compile this target.
TINYGO ?= tinygo

# The Go/wasm WebGL tpviz view. Unlike the wasm-visor lane this needs no fork —
# the engine (third_party/0magnet/cosmos-go) is pure syscall/js, so stock TinyGo
# 0.41+ builds it. The artifacts live in pkg/tpviz/legacy/ because that whole
# directory is go:embed'ed and served next to bundle.js, which loads the module
# lazily when the "WebGL (Go)" view is selected.
tpviz-gl: ## Build the Go/wasm WebGL tpviz view into pkg/tpviz/legacy/ (TinyGo, ~630 KB). Runs alongside the JS WebGL view for comparison.
	$(TINYGO) build -target wasm -no-debug -opt=z -o ./pkg/tpviz/legacy/tpviz-gl.wasm ./pkg/tpviz/wasmgl
	cp "$$($(TINYGO) env TINYGOROOT)/targets/wasm_exec.js" ./pkg/tpviz/legacy/tpviz-gl-exec.js
	@echo "built pkg/tpviz/legacy/tpviz-gl.wasm — commit it intentionally; select it with the 'WebGL (Go)' toggle or ?view=go"

tinygo-wasm-visor: ## Build the FULL browser WASM visor (dmsg+transport+router+appserver, net/http+crypto/tls, route origination) into build/wasm-visor — TinyGo FORK (~7MB / ~2.9MB gzip vs 43MB/9.5MB std-Go). Needs the 0magnet/tinygo fork; set TINYGO=<fork>/build/tinygo TINYGOROOT=<fork>
	mkdir -p ./build/wasm-visor
	$(TINYGO) build -target wasm -no-debug -opt=z -o ./build/wasm-visor/wasm-visor.wasm ./cmd/wasm-visor
	cp "$$($(TINYGO) env TINYGOROOT)/targets/wasm_exec.js" ./build/wasm-visor/wasm_exec.js
	cp ./pkg/wasmhv/browseui/winbox.min.js ./build/wasm-visor/
	cp ./pkg/wasmhv/browseui/browse.js ./build/wasm-visor/
	cp ./pkg/wasmhv/hv-boot.js ./build/wasm-visor/
	cp ./pkg/wasmhv/worker.js ./build/wasm-visor/
	@echo "built ./build/wasm-visor (TinyGo fork) — embed it with 'make embed-wasm-visor-tinygo', then serve the real UI: './skywire cli hv serve --variant tinygo'"

test-wasm-headless: ## Tier B headless smoke: run the REAL compiled wasm-visor blob under Node (no browser) against a loopback dmsg server — boot → ws dmsg session → /api core. Needs node >= 22.
	./scripts/wasm-headless/run.sh

wasm-visor: ## Build the browser WASM visor edge with STANDARD Go js/wasm into build/wasm-visor-go — larger (~38MB) but full crypto/tls + net/http (https clearnet via skysocks). Does NOT touch the committed embed blob.
	mkdir -p ./build/wasm-visor-go
	GOOS=js GOARCH=wasm go build -ldflags="$(WASM_BUILDINFO) -s -w" -o ./build/wasm-visor-go/wasm-visor.wasm ./cmd/wasm-visor
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" ./build/wasm-visor-go/wasm_exec.js
	cp ./pkg/wasmhv/browseui/winbox.min.js ./build/wasm-visor-go/
	cp ./pkg/wasmhv/browseui/browse.js ./build/wasm-visor-go/
	cp ./pkg/wasmhv/hv-boot.js ./build/wasm-visor-go/
	cp ./pkg/wasmhv/worker.js ./build/wasm-visor-go/
	@echo "built ./build/wasm-visor-go (standard Go js/wasm) — serve dev: 'go run cmd/dmsg-wasm/serve.go -dir build/wasm-visor-go'"

embed-wasm-visor: wasm-visor ## Update the COMMITTED std-Go embedded wasm-visor blob (pkg/wasmhv/wasmbin/wasmgo/) — run intentionally, then `git add` + commit it. Deterministic gzip (-n) so re-running on the same wasm yields no diff.
	gzip -9 -n -c ./build/wasm-visor-go/wasm-visor.wasm > ./pkg/wasmhv/wasmbin/wasmgo/wasm-visor.wasm.gz
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" ./pkg/wasmhv/wasmbin/wasmgo/wasm_exec.js
	@echo "updated pkg/wasmhv/wasmbin/wasmgo/ (wasm-visor.wasm.gz + wasm_exec.js) — review with 'git status', commit intentionally (~9.5MB blob)."

embed-wasm-visor-tinygo: tinygo-wasm-visor ## Update the COMMITTED TinyGo embedded wasm-visor blob (pkg/wasmhv/wasmbin/wasmtinygo/) — needs the 0magnet/tinygo fork (set TINYGO=<fork>/build/tinygo TINYGOROOT=<fork>). Pairs the TinyGo wasm with TinyGo's wasm_exec.js.
	gzip -9 -n -c ./build/wasm-visor/wasm-visor.wasm > ./pkg/wasmhv/wasmbin/wasmtinygo/wasm-visor.wasm.gz
	cp "$$($(TINYGO) env TINYGOROOT)/targets/wasm_exec.js" ./pkg/wasmhv/wasmbin/wasmtinygo/wasm_exec.js
	@echo "updated pkg/wasmhv/wasmbin/wasmtinygo/ (wasm-visor.wasm.gz + wasm_exec.js) — review with 'git status', commit intentionally (~2.9MB blob)."

# embed-wallet was removed: the wallet is now served straight from the VENDORED
# skycoin module's own embedded dist (skycoin-web/src/gui.DistFS, via
# visor.WalletUIFS) — a skycoin vendor bump IS the wallet update; there is no
# copied pkg/visor/static/wallet tree to sync (or forget to sync) anymore.

prune-wasm-embed-history: ## Drop OLD embedded wasm-visor blobs from git history, keeping only the current one (reclaims ~9MB per past update). REWRITES HISTORY — needs git-filter-repo; run on a fresh clone, then force-push. See scripts/prune-wasm-embed-history.sh for the full warning.
	scripts/prune-wasm-embed-history.sh

dmsg-wasm-hv: ## Build the browser hypervisor-over-dmsg bundle (Service Worker proxy) into build/dmsg-wasm-hv
	mkdir -p ./build/dmsg-wasm-hv
	GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o ./build/dmsg-wasm-hv/dmsg.wasm ./cmd/dmsg-wasm
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" ./build/dmsg-wasm-hv/
	cp ./cmd/dmsg-wasm/sw.js ./cmd/dmsg-wasm/hv.html ./build/dmsg-wasm-hv/
	@echo "built ./build/dmsg-wasm-hv — serve over http (cd build/dmsg-wasm-hv && python -m http.server 8080) and open http://localhost:8080/hv.html"

dmsg-wasm-inline: dmsg-wasm ## Emit a single self-contained dmsg-client.html (wasm_exec.js + base64 wasm inlined, no static host needed)
	@cd ./build/dmsg-wasm && { \
	  printf '<!DOCTYPE html><html><head><meta charset="utf-8"><title>skywire dmsg</title></head><body><script>'; \
	  cat wasm_exec.js; \
	  printf '</script><script>const _b="'; base64 -w0 dmsg.wasm; printf '";'; \
	  printf 'const _go=new Go();const _u8=Uint8Array.from(atob(_b),c=>c.charCodeAt(0));'; \
	  printf 'WebAssembly.instantiate(_u8,_go.importObject).then(r=>{_go.run(r.instance);console.log("skywireDmsg ready");});'; \
	  printf '</script></body></html>'; \
	} > dmsg-client.html
	@echo "built ./build/dmsg-wasm/dmsg-client.html (single self-contained file, base64-inlined wasm)"

tpviz-ui: ## Build transport visualizer TypeScript UI into pkg/tpviz/legacy for embedding
	cd ./pkg/tpviz/ui && npm install && npm run build

clean-windows: ## Clean project: remove created binaries and apps
	powershell -Command "If (Test-Path ./local) { Remove-Item -Path ./local -Force -Recurse }"
	powershell -Command "If (Test-Path ./build) { Remove-Item -Path ./build -Force -Recurse }"

install: ## Install `skywire-visor`, `skywire-cli`, `setup-node`
	${OPTS} go install ${BUILD_OPTS} .

install-windows: ## Install `skywire-visor`, `skywire-cli`, `setup-node`
	powershell 'Get-ChildItem .\cmd | % { ${OPTS} go install ${BUILD_OPTS} ./ $$_.FullName }'

install-static: ## Install `skywire-visor`, `skywire-cli`, `setup-node`
	${STATIC_OPTS} go install -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' .

lint: ## Run linters. Use make install-linters first
	command -v golangci-lint || go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	golangci-lint --version
	CGO_ENABLED=0 ${OPTS} golangci-lint run -c .golangci.yml --build-tags 'withoutsystray withoutgotop' ./...
	CGO_ENABLED=0 ${OPTS} go vet -mod=vendor -tags 'withoutsystray withoutgotop' ./...

lint-extra: ## Run linters with extra checks.
	golangci-lint run --no-config --enable-all ./...
	go vet -all -mod=vendor ./...

gocyclo: ## Run gocyclo
	gocyclo -over 14 .

lint-windows: ## Run linters. Use make install-linters-windows first
	powershell 'if (-not (Get-Command golangci-lint -ErrorAction SilentlyContinue)) { go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest }'
	powershell 'golangci-lint --version'
	powershell '$$env:CGO_ENABLED=0; golangci-lint run -c .golangci.yml --build-tags "withoutsystray withoutgotop" ./...'

gocyclo-windows: ## Run gocyclo on windows
	powershell 'gocyclo -over 14 .'

install-shellcheck: ## install shellcheck to current directory
	./ci_scripts/install-shellcheck.sh

lint-shell:
	find ./ci_scripts -type f -iname '*.sh' -print0 | xargs -0 -I {} bash -c "$$(command -v ./shellcheck || command -v shellcheck) \"{}\""
	find ./docker -type f -iname '*.sh' -print0 | xargs -0 -I {} bash -c "$$(command -v ./shellcheck || command -v shellcheck) -e SC2086 \"{}\""

test: ## Run tests
	${OPTS} go test ${TEST_OPTS} ./internal/... ./pkg/... ./cmd/...
	${OPTS} go test ${TEST_OPTS}

test-windows: ## Run tests on windows
	@go clean -testcache
	${OPTS} go test ${TEST_OPTS} ./internal/... ./pkg/... ./cmd/skywire-cli... ./cmd/skywire-visor... ./cmd/skywire... ./cmd/apps...

install-linters: ## Install linters
	${OPTS} go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	${OPTS} go install golang.org/x/tools/cmd/goimports@latest github.com/FiloSottile/vendorcheck@latest

install-linters-windows: ## Install linters
	${OPTS} go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest golang.org/x/tools/cmd/goimports@latest

tidy: ## Tidies and vendors dependencies.
	${OPTS} go mod tidy -v

format: tidy ## Formats the code. Must have goimports installed (use make install-linters).
	@if grep -qE '^(replace|exclude)' go.mod; then \
		echo "ERROR: go.mod contains replace or exclude directives which break go install @version and Docker builds"; \
		grep -E '^(replace|exclude)' go.mod; \
		exit 1; \
	fi
	${OPTS} goimports -w -local ${PROJECT_BASE} ./pkg ./cmd ./internal

format-windows: tidy ## Formats the code. Must have goimports installed (use make install-linters).
	powershell 'Get-ChildItem -Directory | where Name -NotMatch vendor | % { Get-ChildItem $$_ -Recurse -Include *.go } | % {goimports -w -local ${PROJECT_BASE} $$_ }'

dep: tidy ## Sorts dependencies
	${OPTS} go mod vendor -v

update-deps: ## Update all dependencies to latest versions (use 'make update-deps push-deps' to also commit and push)
	${OPTS} go get -v -u ./...
	${OPTS} go mod tidy -v
	${OPTS} go mod vendor -v
	@echo "Dependencies updated. Run 'make push-deps' to commit and push changes."

update-dmsg: ## Update dmsg to latest develop branch
	@echo "Updating dmsg to latest develop..."
	${OPTS} GOPROXY=direct go get -v github.com/skycoin/dmsg@develop
	${OPTS} go mod tidy -v
	${OPTS} go mod vendor -v
	@echo "dmsg updated successfully"

update-skycoin: ## Update skycoin to latest develop branch
	@echo "Updating skycoin to latest develop..."
	${OPTS} go get -v github.com/skycoin/skycoin@develop
	${OPTS} go mod tidy -v
	${OPTS} go mod vendor -v
	@echo "skycoin updated successfully"

push-deps: ## Commit and push dependency updates
	@echo "Committing dependency updates..."
	git add go.mod go.sum vendor
	git commit -m "Update dependencies"
	git push
	@echo "Dependencies pushed successfully"

example-apps: ## Build example apps
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)apps/ ./example/...

# Bin
bin: fix-systray-vendor bin-fix unfix-systray-vendor

bin-fix: ## Build `skywire`
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH) .

fix-systray-vendor:
	@if [ $(UNAME_S) = "Linux" ]; then\
		sed -i '/go conn.handleCall(msg)/c\conn.handleCall(msg)' ./vendor/github.com/godbus/dbus/v5/conn.go ;\
	fi

unfix-systray-vendor:
	@if [ $(UNAME_S) = "Linux" ]; then\
		sed -i '/conn.handleCall(msg)/c\			go conn.handleCall(msg)' ./vendor/github.com/godbus/dbus/v5/conn.go ;\
	fi

build-windows: ## Build `skywire-visor`
	powershell '${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH) ./cmd/skywire'

# Static Bin
build-static: ## Build `skywire-visor`, `skywire-cli`
	${STATIC_OPTS} go build -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH) .

# Static Bin without Systray
build-static-wos: ## Build `skywire-visor`, `skywire-cli`
	${STATIC_OPTS} go build -tags withoutsystray -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH)skywire-visor .

build-deploy: ## Build for deployment Docker images
	${OPTS} go build -tags netgo ${BUILD_OPTS_DEPLOY} -o /release/skywire .

build-race: ## Build for testing Docker images
	CGO_ENABLED=1 ${OPTS} go build -tags netgo ${BUILD_OPTS} -race -o /release/skywire .

dep-github-release:
	rm -rf musl-data
	mkdir -p musl-data
	go run github.com/melbahja/got/cmd/got@latest https://github.com/skycoin/skywire/releases/download/v1.3.29/aarch64-linux-musl-cross.tgz #https://more.musl.cc/10/x86_64-linux-musl/aarch64-linux-musl-cross.tgz
	tar -xzf aarch64-linux-musl-cross.tgz -C ./musl-data && rm aarch64-linux-musl-cross.tgz
	go run github.com/melbahja/got/cmd/got@latest https://github.com/skycoin/skywire/releases/download/v1.3.29/arm-linux-musleabi-cross.tgz #https://more.musl.cc/10/x86_64-linux-musl/arm-linux-musleabi-cross.tgz
	tar -xzf arm-linux-musleabi-cross.tgz -C ./musl-data && rm arm-linux-musleabi-cross.tgz
	go run github.com/melbahja/got/cmd/got@latest https://github.com/skycoin/skywire/releases/download/v1.3.29/arm-linux-musleabihf-cross.tgz #https://more.musl.cc/10/x86_64-linux-musl/arm-linux-musleabihf-cross.tgz
	tar -xzf arm-linux-musleabihf-cross.tgz -C ./musl-data && rm arm-linux-musleabihf-cross.tgz
	go run github.com/melbahja/got/cmd/got@latest https://github.com/skycoin/skywire/releases/download/v1.3.29/i686-linux-musl-cross.tgz #https://more.musl.cc/10/x86_64-linux-musl/x86_64-linux-musl-cross.tgz
	tar -xzf i686-linux-musl-cross.tgz -C ./musl-data && rm i686-linux-musl-cross.tgz
	go run github.com/melbahja/got/cmd/got@latest https://github.com/skycoin/skywire/releases/download/v1.3.29/riscv64-linux-musl-cross.tgz #https://more.musl.cc/10/x86_64-linux-musl/riscv64-linux-musl-cross.tgz
	tar -xzf riscv64-linux-musl-cross.tgz -C ./musl-data && rm riscv64-linux-musl-cross.tgz
	go run github.com/melbahja/got/cmd/got@latest https://github.com/skycoin/skywire/releases/download/v1.3.29/x86_64-linux-musl-cross.tgz #https://more.musl.cc/10/x86_64-linux-musl/i686-linux-musl-cross.tgz
	tar -xzf x86_64-linux-musl-cross.tgz -C ./musl-data && rm x86_64-linux-musl-cross.tgz
	# Build secp256k1 static libraries for each musl target
	./ci_scripts/build-secp256k1-musl.sh amd64 x86_64-linux-musl ./musl-data/x86_64-linux-musl-cross
	./ci_scripts/build-secp256k1-musl.sh arm64 aarch64-linux-musl ./musl-data/aarch64-linux-musl-cross
	./ci_scripts/build-secp256k1-musl.sh arm arm-linux-musleabi ./musl-data/arm-linux-musleabi-cross
	./ci_scripts/build-secp256k1-musl.sh armhf arm-linux-musleabihf ./musl-data/arm-linux-musleabihf-cross
	./ci_scripts/build-secp256k1-musl.sh 386 i686-linux-musl ./musl-data/i686-linux-musl-cross
	#./ci_scripts/build-secp256k1-musl.sh riscv64 riscv64-linux-musl ./musl-data/riscv64-linux-musl-cross
	# Build libusb static libraries for each musl target (for hardware wallet support)
	./ci_scripts/build-libusb-musl.sh amd64 x86_64-linux-musl ./musl-data/x86_64-linux-musl-cross
	./ci_scripts/build-libusb-musl.sh arm64 aarch64-linux-musl ./musl-data/aarch64-linux-musl-cross
	./ci_scripts/build-libusb-musl.sh arm arm-linux-musleabi ./musl-data/arm-linux-musleabi-cross
	./ci_scripts/build-libusb-musl.sh armhf arm-linux-musleabihf ./musl-data/arm-linux-musleabihf-cross
	#./ci_scripts/build-libusb-musl.sh 386 i686-linux-musl ./musl-data/i686-linux-musl-cross  # 386 uses stub, no libusb needed
	./ci_scripts/build-libusb-musl.sh riscv64 riscv64-linux-musl ./musl-data/riscv64-linux-musl-cross

build-docker: ## Build docker image
	./ci_scripts/docker-push.sh -t latest -b

.PHONY: check-ui check-onpush

# Manager UI
install-deps-ui:  ## Install the UI dependencies
	cd $(MANAGER_UI_DIR) && npm ci

config: ## Create or regenerate a config with correct default app bin_path for `make build`
	$(BUILD_PATH)skywire-cli config gen -irx --binpath $(BUILD_PATH)apps

run: ## Run skywire visor with skywire-config.json, and start a browser if running a hypervisor
	$(BUILD_PATH)skywire-visor -bc ./skywire-config.json

## Prepare to run skywire from source, without compiling binaries
prepare:
	test -d apps && rm -r apps || true
	test -d build && rm -r build || true
	mkdir -p build || true
	ln ./scripts/skywire ./build/
	chmod +x ./build/*
	sudo echo "sudo cache"


run-source: prepare ## Run skywire from source, without compiling binaries
	${OPTS} go run ${BUILD_OPTS} . cli config gen -n | sudo ${OPTS} go run ${BUILD_OPTS} . visor -n || true

run-systray: prepare ## Run skywire from source, with vpn server enabled
	${OPTS} go run ${BUILD_OPTS} . cli config gen -n | sudo ${OPTS} go run ${BUILD_OPTS} . visor -n --systray || true

run-vpnsrv: prepare ## Run skywire from source, without compiling binaries
	${OPTS} go run ${BUILD_OPTS} . cli config gen -n --servevpn | sudo ${OPTS} go run ${BUILD_OPTS} . visor -n || true

run-source-dmsghttp: prepare ## Run skywire from source with dmsghttp config
	${OPTS} go run ${BUILD_OPTS} . cli config gen -dn | sudo ${OPTS} go run ${BUILD_OPTS} . visor -n || true

run-vpnsrv-dmsghttp: prepare ## Run skywire from source with dmsghttp config and vpn server
	${OPTS} go run ${BUILD_OPTS} . cli config gen -dn --servevpn | sudo ${OPTS} go run ${BUILD_OPTS} . visor -n || true

lint-ui:  ## Lint the UI code
	cd $(MANAGER_UI_DIR) && npm run lint

test-ui:  ## Run the hypervisor UI unit tests headless (needs Chrome; set CHROME_BIN if it isn't on PATH)
	cd $(MANAGER_UI_DIR) && npm run test-headless

build-ui: install-deps-ui  ## Builds the UI
	cd $(MANAGER_UI_DIR) && npm run build
	mkdir -p ${PWD}/bin
	rm -rf ${MANAGER_UI_BUILT_DIR}
	mkdir ${MANAGER_UI_BUILT_DIR}
	cp -r ${MANAGER_UI_DIR}/dist/. ${MANAGER_UI_BUILT_DIR}

check-onpush:  ## Fail if an OnPush component has an unmarked asynchronous callback
	node ci-scripts/check-onpush-marks.js $(MANAGER_UI_DIR)/src

check-ui: build-ui  ## Fail if the committed manager UI bundle is stale vs a fresh build
	@# pkg/visor/static is a build artifact committed to the repository and
	@# embedded in the visor, so a stale one ships an old UI from source that
	@# looks current. CI builds the UI already; this is what makes the build
	@# prove the committed copy matches.
	@git diff --quiet -- $(MANAGER_UI_BUILT_DIR) && git diff --quiet --cached -- $(MANAGER_UI_BUILT_DIR) \
		&& test -z "$$(git ls-files --others --exclude-standard -- $(MANAGER_UI_BUILT_DIR))" || { \
		echo "ERROR: the committed manager UI bundle is stale."; \
		echo "Run 'make build-ui' and commit the result."; \
		git --no-pager diff --stat -- $(MANAGER_UI_BUILT_DIR); \
		git ls-files --others --exclude-standard -- $(MANAGER_UI_BUILT_DIR); \
		exit 1; \
	}
	@echo "The committed manager UI bundle is up to date."

build-ui-windows: install-deps-ui ## Builds the UI on windows
	cd $(MANAGER_UI_DIR) && npm run build
	powershell 'Remove-Item -Recurse -Force -Path ${MANAGER_UI_BUILT_DIR}'
	powershell 'New-Item -Path ${MANAGER_UI_BUILT_DIR} -ItemType Directory'
	powershell 'Copy-Item -Recurse ${MANAGER_UI_DIR}\dist\* ${MANAGER_UI_BUILT_DIR}'

installer: mac-installer ## Builds MacOS installer for skywire-visor

mac-installer: ## Create unsigned and not-notarized application, run make mac-installer-help for more
	./scripts/mac_installer/create_installer.sh

mac-installer-help: ## Show installer creation help
	./scripts/mac_installer/create_installer.sh -h

mac-installer-release: mac-installer ## Upload created signed and notarized applciation to github
	$(eval GITHUB_TAG=$(shell git describe --abbrev=0 --tags))
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} ./skywire-installer-${GITHUB_TAG}-darwin-amd64.pkg
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} ./skywire-installer-${GITHUB_TAG}-darwin-arm64.pkg

win-installer-latest: ## Build the windows .msi (installer) latest version
	@powershell '.\scripts\win_installer\script.ps1 latest amd64'
	@powershell '.\scripts\win_installer\script.ps1 latest 386'

win-installer: ## Build the windows .msi (installer) custom version
	@powershell '.\scripts\win_installer\script.ps1 $(CUSTOM_VERSION) amd64'
	@powershell '.\scripts\win_installer\script.ps1 $(CUSTOM_VERSION) 386'

windows-installer-release:
	$(eval GITHUB_TAG=$(shell git describe --abbrev=0 --tags))
	make win-installer CUSTOM_VERSION=$(GITHUB_TAG)
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} ./skywire-installer-${GITHUB_TAG}-windows-amd64.msi
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} ./skywire-installer-${GITHUB_TAG}-windows-386.msi

# useful commands
#dmsghttp-update: ## update dmsghttp config
#	go run cmd/skywire/skywire.go cli config update dmsghttp -p dmsghttp-config.json

help: ## `make help` menu
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

help-windows: ## Display help for windows
	@powershell 'Select-String -Pattern "windows[a-zA-Z_-]*:.*## .*$$" $(MAKEFILE_LIST) | % { $$_.Line -split ":.*?## " -Join "`t:`t" } '

## : ## _ [E2E tests suite]

e2e-build: set-forwarding e2e-dock ## E2E. Set port forwarding & Build dockers and containers for e2e-tests

e2e-dock: ## E2E. Build dockers and containers for e2e-tests
	./docker/docker_build.sh e2e "" $(BUILD_ARCH)

e2e-run: ## E2E. Start e2e environment and wait for all health checks to pass
	@# Start services in stages to avoid overwhelming the CI runner.
	@# On a dual-core GitHub Actions runner, starting everything at
	@# once causes healthcheck timeouts because Go services are too
	@# slow to initialize when competing for CPU.
	@#
	@# After #2471 the nine deployment-side services (tpd, rf,
	@# dmsg-disc, dmsg-server, sn, sd, ar, tps, stun) collapse into
	@# one `deployment-services` container; the previous per-service
	@# staging is replaced by one wait on the supervisor. The five
	@# per-service redis containers are now a single `redis` (each
	@# service uses its own logical DB), and the deprecated
	@# uptime-tracker + its postgres are gone (uptime is integrated
	@# into the discovery services).
	bash -c "DOCKER_TAG=e2e docker compose up -d --wait redis"
	bash -c "DOCKER_TAG=e2e docker compose up -d --wait deployment-services || { echo '=== deployment-services unhealthy — logs: ==='; docker compose logs --tail=150 deployment-services; exit 1; }"
	bash -c "DOCKER_TAG=e2e docker compose up -d --wait visor-b || { echo '=== visor-b unhealthy — deployment-services (dmsg-server side) + visor-b logs: ==='; docker compose logs --tail=200 deployment-services; docker compose logs --tail=120 visor-b; exit 1; }"
	bash -c "DOCKER_TAG=e2e docker compose up -d --wait visor-a visor-c || { echo '=== visor-a/visor-c unhealthy — logs: ==='; docker compose logs --tail=150 visor-a visor-c; exit 1; }"
	bash -c "DOCKER_TAG=e2e docker compose ps"

e2e-logs:
	bash -c "docker compose logs --tail=all --follow"

e2e-test:  ## E2E. Run e2e-tests suite in Docker. Prepare e2e environment with `make e2e-build && make e2e-run`
	DOCKER_TAG=e2e docker compose -f ${COMPOSE_FILE} run --rm \
		e2e-test \
		sh -c "go clean -testcache && go test -v -timeout=45m ./internal/integration"

e2e-test-local:  ## E2E. Run e2e-tests suite in Docker. Prepare e2e environment with `make e2e-build && make e2e-run`
	DOCKER_TAG=integration docker compose -f ${COMPOSE_FILE} run --rm \
		e2e-test \
		sh -c "go clean -testcache && go test -v -timeout=45m ./internal/integration"

e2e-config: ## E2E. Regenerate visor configs from template and deployment config
	@# skychat comes out as `--addr *:8001 --pair-enable` on its own:
	@# SKYCHATADDR in docker/integration/e2e.conf supplies the
	@# all-interfaces bind (the suite reaches http://visor-x:8001 across
	@# containers, and docker-compose publishes those ports to the host)
	@# and SKYCHATPAIR defaults on. It is still the in-process app — no
	@# "binary" key, bin_path is "./", so an external launch could not
	@# resolve one anyway — just one that also listens. This used to need
	@# a manual re-apply after every regeneration; it does not any more.
	@#
	@# --pty-rpc-exec is required by TestEnv_DmsgPtyExec: #3658 gated the
	@# RPC-initiated `cli pty exec` path behind pty.allow_rpc_exec, OFF by
	@# default. Every visor needs it, not just the hypervisor — the test's
	@# negative case drives visor-a as the CALLER to prove the remote's
	@# whitelist rejects it, and without the flag that call dies on the
	@# gate instead, which is a different (and untested) rejection.
	@echo "Regenerating E2E visor configs..."
	@# visor-A: skychat node with hypervisor set to visor-B
	SKYDEPLOY=docker/integration/services-config.json SKYENV=docker/integration/e2e.conf \
		go run . cli config gen -f --nofetch --sk 42bca4df2f3189b28872d40e6c61aacd5e85b8e91f8fea65780af27c142419e5 \
		-j 0348c941c5015a05c455ff238af2e57fb8f914c399aab604e9abb5b32b91a4c1fe \
		--pty-rpc-exec \
		-o docker/integration/visorA.json
	@# visor-B: hypervisor
	SKYDEPLOY=docker/integration/services-config.json SKYENV=docker/integration/e2e.conf \
		go run . cli config gen -f --nofetch --sk da4f48916e99aa3de794bffe1b5ecd465335e38b55457a9f78b411eb8585e36f \
		--pty-rpc-exec \
		-i -o docker/integration/visorB.json
	@# visor-C: skychat node with hypervisor set to visor-B
	SKYDEPLOY=docker/integration/services-config.json SKYENV=docker/integration/e2e.conf \
		go run . cli config gen -f --nofetch --sk 0e17cd505d81f998950e22864ae4692249124441bd9148b801f76f1595ac688f \
		-j 0348c941c5015a05c455ff238af2e57fb8f914c399aab604e9abb5b32b91a4c1fe \
		--pty-rpc-exec \
		-o docker/integration/visorC.json
	@echo "E2E visor configs regenerated."
	@echo ""
	@echo "NOTE: skychat should read \"--addr *:8001 --pair-enable\" in"
	@echo "      docker/integration/visor{A,B,C}.json (from SKYCHATADDR in"
	@echo "      docker/integration/e2e.conf). If it does not, the e2e suite"
	@echo "      and the browser UIs cannot reach it — see make e2e-skychat."

e2e-skychat: ## E2E. Wire transports between all three visors and print the skychat UIs + PKs
	@# Manual/browser testing of the chat app against three real visors.
	@# Run after `make e2e-build && make e2e-run`.
	@#
	@# Every visor runs skychat in-process on container port 8001, published
	@# to the host by docker-compose on 8001/8002/8003 (127.0.0.1 only — the
	@# mic/camera and notification APIs need a secure context, which plain
	@# http only provides for localhost).
	@#
	@# The composer's default network is "skynet", which routes over a
	@# transport, so this pairs every visor with every other one. ("dmsg"
	@# needs none — it goes through the dmsg server — so if a transport
	@# fails you can still test by switching the composer's network select.)
	@set -e; \
	pk() { docker exec "$$1" /release/skywire cli visor --rpc localhost:3435 pk --json | tr -dc '0-9a-f'; }; \
	tp() { \
	  if docker exec "$$1" /release/skywire cli --rpc localhost:3435 tp add "$$2" --json >/dev/null 2>&1; then \
	    echo "  $$1 -> $$3  ok"; \
	  else \
	    echo "  $$1 -> $$3  not added (already present, or the pair is still settling)"; \
	  fi; }; \
	PKA=$$(pk visor-a); PKB=$$(pk visor-b); PKC=$$(pk visor-c); \
	for p in "$$PKA" "$$PKB" "$$PKC"; do \
	  if [ $${#p} -ne 66 ]; then \
	    echo "could not read a visor public key — is the environment up? (make e2e-build && make e2e-run)"; \
	    exit 1; \
	  fi; \
	done; \
	echo ""; \
	echo "Transports:"; \
	tp visor-a "$$PKB" visor-b; \
	tp visor-a "$$PKC" visor-c; \
	tp visor-b "$$PKC" visor-c; \
	echo ""; \
	echo "Open a skychat UI per visor, then add a peer by pasting its public key:"; \
	echo ""; \
	printf '  %-9s %-28s %s\n' visor-a "http://127.0.0.1:8001" "$$PKA"; \
	printf '  %-9s %-28s %s\n' visor-b "http://127.0.0.1:8002" "$$PKB"; \
	printf '  %-9s %-28s %s\n' visor-c "http://127.0.0.1:8003" "$$PKC"; \
	echo ""; \
	echo "  hypervisor (visor-b)  http://127.0.0.1:8000"; \
	echo ""

e2e-stop: ## E2E. Stop e2e environment without destroying it. Restart with `make e2e-run`
	bash -c "DOCKER_TAG=e2e docker compose -f ${COMPOSE_FILE} stop"
	bash -c "DOCKER_TAG=e2e docker compose -f ${COMPOSE_FILE} ps"
	-sudo ./ci_scripts/cleanup-ip-aliases.sh

e2e-clean: ## E2E. Stop e2e environment and clean everything. Restart only with `make e2e-build && make e2e-run`
	bash -c "DOCKER_TAG=e2e docker compose -f ${COMPOSE_FILE} down"
	bash ./docker/docker_clean.sh e2e
	-sudo ./ci_scripts/cleanup-ip-aliases.sh

e2e-help: ## E2E. Show env-vars and useful commands
	@echo -e "\nNow you can use docker compose:\n"
	@echo -e "   docker compose ps/top/logs"
	@echo -e "   docker compose up/down/start/stop"
	@echo -e "\nConsult with:\n\n   docker compose help\n"

docker-build-test:
	bash ./docker/docker_build.sh test "" $(BUILD_ARCH)

docker-build:
	bash ./docker/docker_build.sh prod "" $(BUILD_ARCH)

docker-push-test:
	bash ./docker/docker_build.sh test "" $(BUILD_ARCH)
	bash ./docker/docker_push.sh test

docker-push:
	bash ./docker/docker_build.sh prod "" $(BUILD_ARCH)
	bash ./docker/docker_push.sh prod

set-forwarding: #sudo ./ci_scripts/setup-sudo-requirements.sh
	sudo ./ci_scripts/setup-sudo-requirements.sh

#reset-forwarding:
	# If you need to disable forwarding, run:
	#   sudo sysctl -w net.ipv4.ip_forward=0
	#   sudo sysctl -w net.ipv6.conf.all.forwarding=0

integration-env-build: #build
	./docker/docker_build.sh integration "" $(BUILD_ARCH)
	bash -c "DOCKER_TAG=integration docker compose up -d"

integration-env-start: #start
	bash -c "DOCKER_TAG=integration docker compose up -d"

integration-env-stop: #stop
	bash -c "DOCKER_TAG=integration docker compose -f ${COMPOSE_FILE} stop"

integration-env-clean: #clean
	bash -c "DOCKER_TAG=integration docker compose -f ${COMPOSE_FILE} down"
	bash ./docker/docker_clean.sh integration

update-dep: update-deps ## Alias for update-deps

## Sync local develop branch with upstream skycoin/skywire develop.
## Requires: origin = your fork, upstream = skycoin/skywire
sync-upstream-develop: #sync develop branch with upstream develop branch for forks
	@normalize() { \
		echo "$$1" | sed \
			-e 's|git@github.com:|https://github.com/|' \
			-e 's|\.git$$||' \
			-e 's|https://github.com/||' \
			| tr '[:upper:]' '[:lower:]'; \
	}; \
	# --- check upstream points to skycoin/skywire --- \
	UPSTREAM_URL=$$(git remote get-url upstream 2>/dev/null); \
	if [ -z "$$UPSTREAM_URL" ]; then \
		echo "[error] no 'upstream' remote found. Add it with:"; \
		echo "  git remote add upstream https://github.com/skycoin/skywire.git"; \
		exit 1; \
	fi; \
	UPSTREAM_NORM=$$(normalize "$$UPSTREAM_URL"); \
	if [ "$$UPSTREAM_NORM" != "skycoin/skywire" ]; then \
		echo "[error] upstream remote does not point to skycoin/skywire."; \
		echo "  Found: $$UPSTREAM_URL"; \
		exit 1; \
	fi; \
	# --- check origin is NOT skycoin/skywire (must be a fork) --- \
	ORIGIN_URL=$$(git remote get-url origin 2>/dev/null); \
	if [ -z "$$ORIGIN_URL" ]; then \
		echo "[error] no 'origin' remote found."; \
		exit 1; \
	fi; \
	ORIGIN_NORM=$$(normalize "$$ORIGIN_URL"); \
	if [ "$$ORIGIN_NORM" = "skycoin/skywire" ]; then \
		echo "[error] origin points to skycoin/skywire directly."; \
		echo "  This target must be run from a fork, not a clone of the canonical repo."; \
		exit 1; \
	fi; \
	echo "[ok] origin is a fork ($$ORIGIN_NORM), upstream is skycoin/skywire — syncing develop..."; \
	git checkout develop && \
	git pull && \
	git fetch upstream && \
	git merge upstream/develop && \
	git push
