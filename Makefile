
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

check: lint test ## Run linters and tests

check-windows: lint-windows test-windows ## Run linters and tests on appveyor windows image

build: host-apps bin ## Install dependencies, build apps and binaries. `go build` with ${OPTS}

build1: BUILD_PATH = ./

build1: build ## Install dependencies, build apps and binaries in root folder. `go build` with ${OPTS}

build-windows: host-apps-windows bin-windows ## Install dependencies, build apps and binaries. `go build` with ${OPTS}

build-windows-appveyor: host-apps-windows-appveyor bin-windows-appveyor ## Install dependencies, build apps and binaries. `go build` with ${OPTS} for AppVeyor image

build-static: host-apps-static bin-static ## Build apps and binaries. `go build` with ${OPTS}

build-static-wos: host-apps-static bin-static-wos ## Build apps and binaries. `go build` with ${OPTS}

build-example: host-apps example-apps bin ## Build apps, example apps and binaries. `go build` with ${OPTS}

installer: mac-installer ## Builds MacOS installer for skywire-visor

install-system-linux: build ## Install apps and binaries over those provided by the linux package - linux package must be installed first!
	sudo install -Dm755 $(BUILD_PATH)skywire-cli /opt/skywire/bin/
	sudo install -Dm755 $(BUILD_PATH)skywire-visor /opt/skywire/bin/
	sudo install -Dm755 $(BUILD_PATH)apps/vpn-server /opt/skywire/apps/
	sudo install -Dm755 $(BUILD_PATH)apps/vpn-client /opt/skywire/apps/
	sudo install -Dm755 $(BUILD_PATH)apps/skysocks-client /opt/skywire/apps/
	sudo install -Dm755 $(BUILD_PATH)apps/skysocks /opt/skywire/apps/
	sudo install -Dm755 $(BUILD_PATH)apps/skychat /opt/skywire/apps/

install-generate: ## Installs required execs for go generate.
	${OPTS} go install github.com/mjibson/esc
	${OPTS} go install github.com/vektra/mockery/v2@latest

generate: ## Generate mocks and config README's
	go generate ./...

clean: ## Clean project: remove created binaries and apps
	-rm -f ./build

clean-windows: ## Clean project: remove created binaries and apps
	powershell -Command Remove-Item -Path ./build -Force -Recurse

install: ## Install `skywire-visor`, `skywire-cli`, `setup-node`
	${OPTS} go install ${BUILD_OPTS} ./cmd/skywire-visor ./cmd/skywire-cli ./cmd/setup-node

install-windows: ## Install `skywire-visor`, `skywire-cli`, `setup-node`
	powershell 'Get-ChildItem .\cmd | % { ${OPTS} go install ${BUILD_OPTS} ./ $$_.FullName }'

install-static: ## Install `skywire-visor`, `skywire-cli`, `setup-node`
	${STATIC_OPTS} go install -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' ./cmd/skywire-visor ./cmd/skywire-cli ./cmd/setup-node

lint: ## Run linters. Use make install-linters first
	${OPTS} golangci-lint run -c .golangci.yml ./...

lint-windows: ## Run linters. Use make install-linters-windows first
	powershell 'golangci-lint run -c .golangci.yml ./...'

lint-appveyor-windows: ## Run linters for appveyor only on windows
	C:\Users\appveyor\go\bin\golangci-lint run -c .golangci.yml ./...

test: ## Run tests
	-go clean -testcache &>/dev/null
	${OPTS} go test ${TEST_OPTS} ./internal/...
	${OPTS} go test ${TEST_OPTS} ./pkg/...

test-windows: ## Run tests on windows
	@go clean -testcache
	${OPTS} go test ${TEST_OPTS} ./internal/...
	${OPTS} go test ${TEST_OPTS} ./pkg/...

install-linters: ## Install linters
	- VERSION=latest ./ci_scripts/install-golangci-lint.sh
	${OPTS} go install golang.org/x/tools/cmd/goimports@latest
	${OPTS} go install github.com/incu6us/goimports-reviser/v2@latest

install-linters-windows: ## Install linters
	${OPTS} go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	${OPTS} go install golang.org/x/tools/cmd/goimports@latest

tidy: ## Tidies and vendors dependencies.
	${OPTS} go mod tidy -v

format: tidy ## Formats the code. Must have goimports and goimports-reviser installed (use make install-linters).
	${OPTS} goimports -w -local ${PROJECT_BASE} ./pkg & ${OPTS} goimports -w -local ${PROJECT_BASE} ./cmd &	${OPTS} goimports -w -local ${PROJECT_BASE} ./internal
	find . -type f -name '*.go' -not -path "./.git/*" -not -path "./vendor/*"  -exec goimports-reviser -project-name ${PROJECT_BASE} {} \;

format-windows: tidy ## Formats the code. Must have goimports and goimports-reviser installed (use make install-linters).
	powershell 'Get-ChildItem -Directory | where Name -NotMatch vendor | % { Get-ChildItem $$_ -Recurse -Include *.go } | % {goimports -w -local ${PROJECT_BASE} $$_ }'

dep: tidy ## Sorts dependencies
	${OPTS} go mod vendor -v

snapshot: ## goreleaser --snapshot --skip-publish --rm-dist
	goreleaser --snapshot --skip-publish --rm-dist

snapshot-linux: ## 	goreleaser --snapshot --config .goreleaser-linux.yml --skip-publish --rm-dist
	goreleaser --snapshot --config .goreleaser-linux.yml --skip-publish --rm-dist

snapshot-clean: ## Cleans snapshot / release
	rm -rf ./dist

host-apps: ## Build app
	test -d $(BUILD_PATH) && rm -r $(BUILD_PATH) || true
	mkdir -p $(BUILD_PATH)apps
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)apps/ ./cmd/apps/skychat
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)apps/ ./cmd/apps/skysocks
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)apps/ ./cmd/apps/skysocks-client
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)apps/ ./cmd/apps/vpn-server
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)apps/ ./cmd/apps/vpn-client

example-apps: ## Build example apps
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)apps/ ./example/example-client-app
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH)apps/ ./example/example-server-app

host-apps-windows: ## build apps on windows
	powershell -Command new-item .\apps -itemtype directory -force
	powershell 'Get-ChildItem .\cmd\apps | % { ${OPTS} go build ${BUILD_OPTS} -o ./apps $$_.FullName }'

host-apps-windows-appveyor: ## build apps on windows. `go build` with ${OPTS} for AppVeyor image
	powershell -Command new-item .\apps -itemtype directory -force
	powershell 'Get-ChildItem .\cmd\apps | % { ${OPTS} go build -o ./apps $$_.FullName }'

# Static Apps
host-apps-static: ## Build app
	test -d apps && rm -r apps || true
	mkdir -p ./apps
	${STATIC_OPTS} go build -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH)apps/ ./cmd/apps/skychat
	${STATIC_OPTS} go build -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH)apps/ ./cmd/apps/skysocks
	${STATIC_OPTS} go build -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH)apps/ ./cmd/apps/skysocks-client
	${STATIC_OPTS} go build -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH)apps/ ./cmd/apps/vpn-server
	${STATIC_OPTS} go build -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH)apps/ ./cmd/apps/vpn-client

host-apps-deploy: ## Build app
	test -d apps && rm -r apps || true
	mkdir -p ./apps
	${OPTS} go build ${BUILD_OPTS_DEPLOY} -o $(BUILD_PATH)apps/ ./cmd/apps/skychat
	${OPTS} go build ${BUILD_OPTS_DEPLOY} -o $(BUILD_PATH)apps/ ./cmd/apps/skysocks
	${OPTS} go build ${BUILD_OPTS_DEPLOY} -o $(BUILD_PATH)apps/ ./cmd/apps/skysocks-client
	${OPTS} go build ${BUILD_OPTS_DEPLOY} -o $(BUILD_PATH)apps/ ./cmd/apps/vpn-server
	${OPTS} go build ${BUILD_OPTS_DEPLOY} -o $(BUILD_PATH)apps/ ./cmd/apps/vpn-client

host-apps-race: ## Build app
	test -d apps && rm -r apps || true
	mkdir -p ./apps
	CGO_ENABLED=1${OPTS} go build ${BUILD_OPTS} -race -o $(BUILD_PATH)apps/ ./cmd/apps/skychat
	CGO_ENABLED=1${OPTS} go build ${BUILD_OPTS} -race -o $(BUILD_PATH)apps/ ./cmd/apps/skysocks
	CGO_ENABLED=1${OPTS} go build ${BUILD_OPTS} -race -o $(BUILD_PATH)apps/ ./cmd/apps/skysocks-client
	CGO_ENABLED=1${OPTS} go build ${BUILD_OPTS} -race -o $(BUILD_PATH)apps/ ./cmd/apps/vpn-server
	CGO_ENABLED=1${OPTS} go build ${BUILD_OPTS} -race -o $(BUILD_PATH)apps/ ./cmd/apps/vpn-client

# Bin
bin: fix-systray-vendor bin-fix unfix-systray-vendor

bin-fix: ## Build `skywire-visor`, `skywire-cli`
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH) ./cmd/skywire-visor
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH) ./cmd/skywire-cli
	${OPTS} go build ${BUILD_OPTS} -o $(BUILD_PATH) ./cmd/setup-node

fix-systray-vendor:
	@if [ $(UNAME_S) = "Linux" ]; then\
		sed -i '/go conn.handleCall(msg)/c\conn.handleCall(msg)' ./vendor/github.com/godbus/dbus/v5/conn.go ;\
	fi

unfix-systray-vendor:
	@if [ $(UNAME_S) = "Linux" ]; then\
		sed -i '/conn.handleCall(msg)/c\			go conn.handleCall(msg)' ./vendor/github.com/godbus/dbus/v5/conn.go ;\
	fi

bin-windows: ## Build `skywire-visor`, `skywire-cli`
	powershell 'Get-ChildItem .\cmd | % { ${OPTS} go build ${BUILD_OPTS} -o ./ $$_.FullName }'

bin-windows-appveyor: ## Build `skywire-visor`, `skywire-cli`
	powershell 'Get-ChildItem .\cmd | % { ${OPTS} go build -o ./ $$_.FullName }'

# Static Bin
bin-static: ## Build `skywire-visor`, `skywire-cli`
	${STATIC_OPTS} go build -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH)skywire-visor ./cmd/skywire-visor
	${STATIC_OPTS} go build -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH)skywire-cli  ./cmd/skywire-cli
	${STATIC_OPTS} go build -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH)setup-node ./cmd/setup-node

# Static Bin without Systray
bin-static-wos: ## Build `skywire-visor`, `skywire-cli`
	${STATIC_OPTS} go build -tags withoutsystray -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH)skywire-visor ./cmd/skywire-visor
	${STATIC_OPTS} go build -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH)skywire-cli  ./cmd/skywire-cli
	${STATIC_OPTS} go build -trimpath --ldflags '-linkmode external -extldflags "-static" -buildid=' -o $(BUILD_PATH)setup-node ./cmd/setup-node

build-deploy: host-apps-deploy ## Build for deployment Docker images
	${OPTS} go build -tags netgo ${BUILD_OPTS_DEPLOY} -o /release/skywire-visor ./cmd/skywire-visor
	${OPTS} go build ${BUILD_OPTS_DEPLOY} -o /release/skywire-cli ./cmd/skywire-cli

build-race: host-apps-race ## Build for testing Docker images
	CGO_ENABLED=1 ${OPTS} go build -tags netgo ${BUILD_OPTS} -race -o /release/skywire-visor ./cmd/skywire-visor
	CGO_ENABLED=1 ${OPTS} go build ${BUILD_OPTS} -race -o /release/skywire-cli ./cmd/skywire-cli

github-prepare-release:
	$(eval GITHUB_TAG=$(shell git describe --abbrev=0 --tags | cut -c 2-6))
	sed '/^## ${GITHUB_TAG}$$/,/^## .*/!d;//d;/^$$/d' ./CHANGELOG.md > releaseChangelog.md

github-release: github-prepare-release
	goreleaser --rm-dist --config .goreleaser-linux.yml --release-notes releaseChangelog.md

github-release-archlinux: github-prepare-release
	goreleaser --rm-dist --config .goreleaser-archlinux.yml --release-notes releaseChangelog.md

github-release-darwin:
	goreleaser --rm-dist  --config .goreleaser-darwin.yml --skip-publish
	$(eval GITHUB_TAG=$(shell git describe --abbrev=0 --tags))
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} ./dist/skywire-${GITHUB_TAG}-darwin-amd64.tar.gz
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} ./dist/skywire-${GITHUB_TAG}-darwin-arm64.tar.gz
	gh release download ${GITHUB_TAG} --repo skycoin/skywire --pattern 'checksums*'
	cat ./dist/checksums.txt >> ./checksums.txt
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} --clobber ./checksums.txt

github-release-windows:
	.\goreleaser\goreleaser.exe --rm-dist  --config .goreleaser-windows.yml --skip-publish
	$(eval GITHUB_TAG=$(shell powershell git describe --abbrev=0 --tags))
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} ./dist/skywire-${GITHUB_TAG}-windows-amd64.zip
	gh release upload --repo skycoin/skywire ${GITHUB_TAG} ./dist/skywire-${GITHUB_TAG}-windows-386.zip
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

run: ## Run skywire visor with skywire-config.json, and start a browser if running a hypervisor
	$(BUILD_PATH)skywire-visor -bc ./skywire-config.json

## Prepare to run skywire from source, without compiling binaries
prepare:
	test -d apps && rm -r apps || true
	mkdir -p apps
	ln ./scripts/_apps/skychat ./apps/
	ln ./scripts/_apps/skysocks ./apps/
	ln ./scripts/_apps/skysocks-client ./apps/
	ln ./scripts/_apps/vpn-server ./apps/
	ln ./scripts/_apps/vpn-client ./apps/
	chmod +x ./apps/*
	sudo echo "sudo cache"

run-source: prepare ## Run skywire from source, without compiling binaries
	go run ./cmd/skywire-cli/skywire-cli.go config gen -in | sudo go run ./cmd/skywire-visor/skywire-visor.go -n || true

run-systray: prepare ## Run skywire from source, with vpn server enabled
	go run ./cmd/skywire-cli/skywire-cli.go config gen -ni | sudo go run ./cmd/skywire-visor/skywire-visor.go -n --systray || true


run-vpnsrv: prepare ## Run skywire from source, without compiling binaries
	go run ./cmd/skywire-cli/skywire-cli.go config gen -in --servevpn | sudo go run ./cmd/skywire-visor/skywire-visor.go -n || true

run-source-test: prepare ## Run skywire from source with test endpoints
	go run ./cmd/skywire-cli/skywire-cli.go config gen -nit | sudo go run ./cmd/skywire-visor/skywire-visor.go -n || true

run-vpnsrv-test: prepare ## Run skywire from source, with vpn server enabled
	go run ./cmd/skywire-cli/skywire-cli.go config gen -nit --servevpn | sudo go run ./cmd/skywire-visor/skywire-visor.go -n || true

run-systray-test: prepare ## Run skywire from source, with vpn server enabled
	go run ./cmd/skywire-cli/skywire-cli.go config gen -nit | sudo go run ./cmd/skywire-visor/skywire-visor.go --systray -nb || true

run-source-dmsghttp: prepare ## Run skywire from source with dmsghttp config
	go run ./cmd/skywire-cli/skywire-cli.go config gen -din | sudo go run ./cmd/skywire-visor/skywire-visor.go -nb || true

run-vpnsrv-dmsghttp: prepare ## Run skywire from source with dmsghttp config and vpn server
	go run ./cmd/skywire-cli/skywire-cli.go config gen -din --servevpn | sudo go run ./cmd/skywire-visor/skywire-visor.go -nb || true

run-source-dmsghttp-test: prepare ## Run skywire from source with dmsghttp config and test endpoints
	go run ./cmd/skywire-cli/skywire-cli.go config gen -dint | sudo go run ./cmd/skywire-visor/skywire-visor.go -nb || true

run-vpnsrv-dmsghttp-test: prepare ## Run skywire from source with dmsghttp config, vpn server, and test endpoints
	go run ./cmd/skywire-cli/skywire-cli.go config gen -dint --servevpn | sudo go run ./cmd/skywire-visor/skywire-visor.go -nb || true

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

deb-install-prequisites: ## Create unsigned application
	sudo chmod +x ./scripts/deb_installer/prequisites.sh
	./scripts/deb_installer/prequisites.sh

deb-package: deb-install-prequisites ## Create unsigned application
	./scripts/deb_installer/package_deb.sh

deb-package-help: ## Show installer creation help
	./scripts/deb_installer/package_deb.sh -h

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

help: ## `make help` menu
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

help-windows: ## Display help for windows
	@powershell 'Select-String -Pattern "windows[a-zA-Z_-]*:.*## .*$$" $(MAKEFILE_LIST) | % { $$_.Line -split ":.*?## " -Join "`t:`t" } '
