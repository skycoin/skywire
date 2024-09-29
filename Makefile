
.PHONY : check lint install-linters dep test
.PHONY : build clean install format  bin
.PHONY : host-apps bin
.PHONY : docker-image docker-clean docker-network
.PHONY : docker-apps docker-bin docker-volume
.PHONY : docker-run docker-stop

VERSION := $(shell git describe)
RFC_3339 := "+%Y-%m-%dT%H:%M:%SZ"
COMMIT := $(shell git rev-list -1 HEAD)

PROJECT_BASE := github.com/skycoin/skywire
SKYWIRE_UTILITIES_BASE := github.com/skycoin/skywire-utilities
ifeq ($(OS),Windows_NT)
	SHELL := pwsh
	OPTS?=powershell -Command setx GO111MODULE on;
	DATE := $(shell powershell -Command date -u ${RFC_3339})
	.DEFAULT_GOAL := help-windows
else
	SHELL := /bin/bash
	OPTS?=GO111MODULE=on
	DATE := $(shell date -u $(RFC_3339))
	.DEFAULT_GOAL := help
endif

ifeq ($(OS),Windows_NT)
    SYSTRAY_CGO_ENABLED := 1
else
    UNAME_S := $(shell uname -s)
    ifeq ($(UNAME_S),Linux)
        SYSTRAY_CGO_ENABLED := 0
    endif
    ifeq ($(UNAME_S),Darwin)
        SYSTRAY_CGO_ENABLED := 1
    endif
endif

ifeq ($(VERSION),)
	VERSION = unknown
endif

ifeq ($(COMMIT),)
	COMMIT = unknown
endif

ifeq ($(BUILDTAG),)
	ifeq ($(OS),Windows_NT)
		BUILDTAG = Windows
	else
		UNAME_S := $(shell uname -s)
		ifeq ($(UNAME_S),Linux)
			BUILDTAG = Linux
		endif
	 	ifeq ($(UNAME_S),Darwin)
			BUILDTAG = Darwin
		endif
	endif
endif

STATIC_OPTS?= $(OPTS) CC=musl-gcc
MANAGER_UI_DIR = static/skywire-manager-src
GO_BUILDER_VERSION=v1.17
MANAGER_UI_BUILT_DIR=pkg/visor/static

TEST_OPTS:=-cover -timeout=5m -mod=vendor

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

BUILD_OPTS?="-ldflags=$(BUILDINFO)" -mod=vendor $(RACE_FLAG)
BUILD_OPTS_DEPLOY?="-ldflags=$(BUILDINFO) -w -s"

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

count-dmsg-disc-entries:
	curl -sL $(jq -r '.prod.dmsg_discovery' services-config.json)/dmsg-discovery/entries | jq '. | length'

check: lint check-cg test ## Run linters and tests

check-cg: ## Cursory check of the main help menu, offline dmsghttp config gen and offline config gen
	@echo "checking help menu for compilation without errors"
	@echo
	go run cmd/skywire/skywire.go --help
	@echo
	@echo "checking dmsghttp offline config gen"
	@echo
	go run cmd/skywire/skywire.go cli config gen --nofetch -dnw
	@echo
	@echo "checking offline config gen"
	@echo
	go run cmd/skywire/skywire.go cli config gen --nofetch -nw
	@echo
	@echo "config gen succeeded without error"
	@echo


check-windows: lint-windows test-windows ## Run linters and tests on windows image

build: clean build-merged ## Install dependencies, build apps and binaries. `go build` with ${OPTS}

build-merged: ## Install dependencies, build apps and binaries. `go build` with ${OPTS}
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)skywire ./cmd/skywire

build-merged-windows: clean-windows
	powershell '${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)skywire.exe ./cmd/skywire'

install-system-linux: build ## Install apps and binaries over those provided by the linux package - linux package must be installed first!
	sudo echo "sudo cache"
	sudo install -Dm755 $(BUILD_PATH)skywire /opt/skywire/bin/
	sudo install -Dm644 services-config.json /opt/skywire/
	sudo install -Dm644 dmsghttp-config.json /opt/skywire/


install-generate: ## Installs required execs for go generate.
	${OPTS} go install github.com/mjibson/esc github.com/vektra/mockery/v2@latest

	## TO DO: it may be unnecessary to install required execs for go generate into the path. An alternative method may exist which does not require this
	## https://eli.thegreenplace.net/2021/a-comprehensive-guide-to-go-generate

generate: ## Generate mocks and config README's
	go generate ./...

clean: ## Clean project: remove created binaries and apps
	-rm -rf ./build ./local

clean-windows: ## Clean project: remove created binaries and apps
	powershell -Command "If (Test-Path ./local) { Remove-Item -Path ./local -Force -Recurse }"
	powershell -Command "If (Test-Path ./build) { Remove-Item -Path ./build -Force -Recurse }"

install: ## Install `skywire-visor`, `skywire-cli`, `setup-node`
	${OPTS} go install ${BUILD_OPTS} ./cmd/skywire

install-windows: ## Install `skywire-visor`, `skywire-cli`, `setup-node`
	powershell 'Get-ChildItem .\cmd | % { ${OPTS} go install ${BUILD_OPTS} ./ $$_.FullName }'

install-static: ## Install `skywire-visor`, `skywire-cli`, `setup-node`
	${STATIC_OPTS} go install -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' ./cmd/skywire

lint: ## Run linters. Use make install-linters first
	golangci-lint --version
	${OPTS} golangci-lint run -c .golangci.yml skywire.go	#break down the linter run over smaller sections of the source code
	${OPTS} golangci-lint run -c .golangci.yml ./cmd/...
	${OPTS} golangci-lint run -c .golangci.yml ./pkg/...
	${OPTS} golangci-lint run -c .golangci.yml	 ./...

lint-windows: ## Run linters. Use make install-linters-windows first
	powershell 'golangci-lint --version'
	powershell 'golangci-lint run -c .golangci.yml ./...'

test: ## Run tests
	-go clean -testcache &>/dev/null
	${OPTS} go test ${TEST_OPTS} ./internal/... ./pkg/... ./cmd/...
	${OPTS} go test ${TEST_OPTS}
	go run cmd/skywire/skywire.go --help
	go run cmd/skywire/skywire.go cli config gen -dnw
	go run cmd/skywire/skywire.go cli config gen --nofetch -nw

test-windows: ## Run tests on windows
	@go clean -testcache
	${OPTS} go test ${TEST_OPTS} ./internal/... ./pkg/... ./cmd/skywire-cli... ./cmd/skywire-visor... ./cmd/skywire... ./cmd/apps...

install-linters: ## Install linters
	- VERSION=latest ./ci_scripts/install-golangci-lint.sh
	${OPTS} go install golang.org/x/tools/cmd/goimports@latest github.com/incu6us/goimports-reviser/v2@latest

install-linters-windows: ## Install linters
	${OPTS} go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest golang.org/x/tools/cmd/goimports@latest

tidy: ## Tidies and vendors dependencies.
	${OPTS} go mod tidy -v

format: tidy ## Formats the code. Must have goimports and goimports-reviser installed (use make install-linters).
	${OPTS} goimports -w -local ${PROJECT_BASE} ./pkg ./cmd ./internal
	find . -type f -name '*.go' -not -path "./.git/*" -not -path "./vendor/*"  -exec goimports-reviser -project-name ${PROJECT_BASE} {} \;

format-windows: tidy ## Formats the code. Must have goimports and goimports-reviser installed (use make install-linters).
	powershell 'Get-ChildItem -Directory | where Name -NotMatch vendor | % { Get-ChildItem $$_ -Recurse -Include *.go } | % {goimports -w -local ${PROJECT_BASE} $$_ }'

dep: tidy ## Sorts dependencies
	${OPTS} go mod vendor -v

snapshot: ## goreleaser --snapshot --clean --skip=publish
	goreleaser --snapshot --clean --skip=publish

snapshot-linux: ## 	goreleaser --snapshot --config .goreleaser-linux.yml --clean --skip=publish
	goreleaser --snapshot --config .goreleaser-linux.yml --clean --skip=publish

snapshot-clean: ## Cleans snapshot / release
	rm -rf ./dist

example-apps: ## Build example apps
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)apps/ ./example/...

# Bin
bin: fix-systray-vendor bin-fix unfix-systray-vendor

bin-fix: ## Build `skywire`
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH) ./cmd/skywire

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
	${STATIC_OPTS} go build 8 -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH) ./cmd/skywire

# Static Bin without Systray
build-static-wos: ## Build `skywire-visor`, `skywire-cli`
	${STATIC_OPTS} go build -tags withoutsystray -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH)skywire-visor ./cmd/skywire

build-deploy: ## Build for deployment Docker images
	${OPTS} go build -tags netgo ${BUILD_OPTS_DEPLOY} -o /release/skywire ./cmd/skywire

build-race: ## Build for testing Docker images
	CGO_ENABLED=1 ${OPTS} go build -tags netgo ${BUILD_OPTS} -race -o /release/skywire ./cmd/skywire

github-prepare-release:
	$(eval GITHUB_TAG=$(shell git describe --abbrev=0 --tags | cut -c 2-6))
	sed '/^## ${GITHUB_TAG}$$/,/^## .*/!d;//d;/^$$/d' ./CHANGELOG.md > releaseChangelog.md

github-release: github-prepare-release
	goreleaser --clean --config .goreleaser-linux.yml --release-notes releaseChangelog.md

github-release-darwin:
	goreleaser --clean  --config .goreleaser-darwin.yml --skip=publish
	$(eval GITHUB_TAG=$(shell git describe --abbrev=0 --tags))
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} ./dist/skywire-${GITHUB_TAG}-darwin-amd64.tar.gz
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} ./dist/skywire-${GITHUB_TAG}-darwin-arm64.tar.gz
	gh release download ${GITHUB_TAG} --repo skycoin/skywire --pattern 'checksums*'
	cat ./dist/checksums.txt >> ./checksums.txt
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} --clobber ./checksums.txt

github-release-windows:
	.\goreleaser\goreleaser.exe --clean  --config .goreleaser-windows.yml --skip=publish
	$(eval GITHUB_TAG=$(shell powershell git describe --abbrev=0 --tags))
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} ./dist/skywire-${GITHUB_TAG}-windows-amd64.zip
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} ./dist/skywire-${GITHUB_TAG}-windows-386.zip
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} ./dist/skywire-${GITHUB_TAG}-windows-arm64.zip
	gh release download ${GITHUB_TAG} --repo skycoin/skywire --pattern 'checksums*'
	cat ./dist/checksums.txt >> ./checksums.txt
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} --clobber ./checksums.txt

dep-github-release:
	mkdir musl-data
	wget -c https://more.musl.cc/10/x86_64-linux-musl/aarch64-linux-musl-cross.tgz -O aarch64-linux-musl-cross.tgz
	tar -xzf aarch64-linux-musl-cross.tgz -C ./musl-data && rm aarch64-linux-musl-cross.tgz
	wget -c https://more.musl.cc/10/x86_64-linux-musl/arm-linux-musleabi-cross.tgz -O arm-linux-musleabi-cross.tgz
	tar -xzf arm-linux-musleabi-cross.tgz -C ./musl-data && rm arm-linux-musleabi-cross.tgz
	wget -c https://more.musl.cc/10/x86_64-linux-musl/arm-linux-musleabihf-cross.tgz -O arm-linux-musleabihf-cross.tgz
	tar -xzf arm-linux-musleabihf-cross.tgz -C ./musl-data && rm arm-linux-musleabihf-cross.tgz
	wget -c https://more.musl.cc/10/x86_64-linux-musl/x86_64-linux-musl-cross.tgz -O x86_64-linux-musl-cross.tgz
	tar -xzf x86_64-linux-musl-cross.tgz -C ./musl-data && rm x86_64-linux-musl-cross.tgz
	wget -c https://more.musl.cc/10/x86_64-linux-musl/riscv64-linux-musl-cross.tgz -O riscv64-linux-musl-cross.tgz
	tar -xzf riscv64-linux-musl-cross.tgz -C ./musl-data && rm riscv64-linux-musl-cross.tgz

build-docker: ## Build docker image
	./ci_scripts/docker-push.sh -t latest -b

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
	go run ./cmd/skywire/skywire.go cli config gen -in | sudo go run ./cmd/skywire/skywire.go visor -n || true

run-systray: prepare ## Run skywire from source, with vpn server enabled
	go run ./cmd/skywire/skywire.go cli config gen -ni | sudo go run ./cmd/skywire/skywire.go visor -n --systray || true

run-vpnsrv: prepare ## Run skywire from source, without compiling binaries
	go run ./cmd/skywire/skywire.go cli config gen -in --servevpn | sudo go run ./cmd/skywire/skywire.go visor -n || true

run-source-dmsghttp: prepare ## Run skywire from source with dmsghttp config
	go run ./cmd/skywire/skywire.go cli config gen -din | sudo go run ./cmd/skywire/skywire.go visor -n || true

run-vpnsrv-dmsghttp: prepare ## Run skywire from source with dmsghttp config and vpn server
	go run ./cmd/skywire/skywire.go cli config gen -din --servevpn | sudo go run ./cmd/skywire/skywire.go visor -n || true

lint-ui:  ## Lint the UI code
	cd $(MANAGER_UI_DIR) && npm run lint

build-ui: install-deps-ui  ## Builds the UI
	cd $(MANAGER_UI_DIR) && npm run build
	mkdir -p ${PWD}/bin
	rm -rf ${MANAGER_UI_BUILT_DIR}
	mkdir ${MANAGER_UI_BUILT_DIR}
	cp -r ${MANAGER_UI_DIR}/dist/. ${MANAGER_UI_BUILT_DIR}

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
	@powershell '.\scripts\win_installer\script.ps1 latest'

win-installer: ## Build the windows .msi (installer) custom version
	@powershell '.\scripts\win_installer\script.ps1 $(CUSTOM_VERSION)'

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
