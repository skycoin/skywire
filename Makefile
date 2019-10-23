.DEFAULT_GOAL := help
.PHONY : check lint install-linters dep test
.PHONY : build  clean install  format  bin
.PHONY : host-apps bin
.PHONY : run stop config
.PHONY : docker-image  docker-clean docker-network
.PHONY : docker-apps docker-bin docker-volume
.PHONY : docker-run docker-stop

OPTS?=GO111MODULE=on
TMP_BUILD_DIR?= /tmp/$(notdir $(CURDIR))
DOCKER_IMAGE?=skywire-runner # docker image to use for running skywire-visor.`golang`, `buildpack-deps:stretch-scm`  is OK too
DOCKER_NETWORK?=SKYNET
DOCKER_NODE?=SKY01
DOCKER_OPTS?=GO111MODULE=on GOOS=linux # go options for compiling for docker container
TEST_OPTS?=-race -tags no_ci -cover -timeout=5m
TEST_OPTS_NOCI?=-race -cover -timeout=5m -v
BUILD_OPTS?=-race

check: lint test ## Run linters and tests

build: dep host-apps bin ## Install dependencies, build apps and binaries. `go build` with ${OPTS}

run: stop build	config  ## Run skywire-visor on host
	./skywire-visor skywire.json

stop: ## Stop running skywire-visor on host
	-bash -c "kill $$(ps aux |grep '[s]kywire-visor' |awk '{print $$2}')"

config: ## Generate skywire.json
	-./skywire-cli node gen-config -o  ./skywire.json -r

clean: ## Clean project: remove created binaries and apps
	-rm -rf ./apps
	-rm -f ./skywire-visor ./skywire-cli ./setup-node ./hypervisor ./skyssh-cli

install: ## Install `skywire-visor`, `skywire-cli`, `hypervisor`, `skyssh-cli`
	${OPTS} go install ./cmd/skywire-visor ./cmd/skywire-cli ./cmd/setup-node ./cmd/hypervisor ./cmd/skyssh-cli

rerun: stop
	${OPTS} go build -race -o ./skywire-visor ./cmd/skywire-visor
	-./skywire-cli node gen-config -o  ./skywire.json -r
	perl -pi -e 's/localhost//g' ./skywire.json
	./skywire-visor skywire.json


lint: ## Run linters. Use make install-linters first
	${OPTS} golangci-lint run -c .golangci.yml ./...
	# The govet version in golangci-lint is out of date and has spurious warnings, run it separately
	${OPTS} go vet -all ./...

vendorcheck:  ## Run vendorcheck
	GO111MODULE=off vendorcheck ./internal/...
	GO111MODULE=off vendorcheck ./pkg/...
	GO111MODULE=off vendorcheck ./cmd/apps/...
	GO111MODULE=off vendorcheck ./cmd/hypervisor/...
	GO111MODULE=off vendorcheck ./cmd/setup-node/...
	GO111MODULE=off vendorcheck ./cmd/skywire-cli/...
	GO111MODULE=off vendorcheck ./cmd/skywire-visor/...
	# vendorcheck fails on ./cmd/skyssh-cli
	# the problem is indirect dependency to github.com/sirupsen/logrus
	#GO111MODULE=off vendorcheck ./cmd/skyssh-cli/...

test: ## Run tests
	-go clean -testcache &>/dev/null
	${OPTS} go test ${TEST_OPTS} ./internal/...
	${OPTS} go test ${TEST_OPTS} ./pkg/...

test-no-ci: ## Run no_ci tests
	-go clean -testcache
	${OPTS} go test ${TEST_OPTS_NOCI} ./pkg/transport/... -run "TCP|PubKeyTable"

install-linters: ## Install linters
	- VERSION=1.17.1 ./ci_scripts/install-golangci-lint.sh
	# GO111MODULE=off go get -u github.com/FiloSottile/vendorcheck
	# For some reason this install method is not recommended, see https://github.com/golangci/golangci-lint#install
	# However, they suggest `curl ... | bash` which we should not do
	# ${OPTS} go get -u github.com/golangci/golangci-lint/cmd/golangci-lint
	${OPTS} go get -u golang.org/x/tools/cmd/goimports

format: ## Formats the code. Must have goimports installed (use make install-linters).
	${OPTS} goimports -w -local github.com/SkycoinProject/skywire ./pkg
	${OPTS} goimports -w -local github.com/SkycoinProject/skywire ./cmd
	${OPTS} goimports -w -local github.com/SkycoinProject/skywire ./internal

dep: ## Sorts dependencies
	${OPTS} go mod vendor -v

create-tmp-build-dir:
	if [ ! -d ${TMP_BUILD_DIR} ]; then mkdir ${TMP_BUILD_DIR}; fi
	if [ ! -d ${TMP_BUILD_DIR}/bin ]; then mkdir ${TMP_BUILD_DIR}/bin; fi
	if [ ! -d ${TMP_BUILD_DIR}/bin/apps ]; then mkdir ${TMP_BUILD_DIR}/bin/apps; fi

# Apps
host-apps: ## Build app
	${OPTS} go build ${BUILD_OPTS} -o ./apps/skychat.v1.0 ./cmd/apps/skychat
	${OPTS} go build ${BUILD_OPTS} -o ./apps/helloworld.v1.0 ./cmd/apps/helloworld
	${OPTS} go build ${BUILD_OPTS} -o ./apps/skysocks.v1.0 ./cmd/apps/skysocks
	${OPTS} go build ${BUILD_OPTS} -o ./apps/skysocks-client.v1.0  ./cmd/apps/skysocks-client
	${OPTS} go build ${BUILD_OPTS} -o ./apps/skyssh.v1.0  ./cmd/apps/skyssh
	${OPTS} go build ${BUILD_OPTS} -o ./apps/skyssh-client.v1.0  ./cmd/apps/skyssh-client

# Bin
bin: ## Build `skywire-visor`, `skywire-cli`, `hypervisor`, `skyssh-cli`
	${OPTS} go build ${BUILD_OPTS} -o ./skywire-visor ./cmd/skywire-visor
	${OPTS} go build ${BUILD_OPTS} -o ./skywire-cli  ./cmd/skywire-cli
	${OPTS} go build ${BUILD_OPTS} -o ./setup-node ./cmd/setup-node
	${OPTS} go build ${BUILD_OPTS} -o ./dmsg-server ./cmd/dmsg-server
	${OPTS} go build ${BUILD_OPTS} -o ./hypervisor ./cmd/hypervisor
	${OPTS} go build ${BUILD_OPTS} -o ./skyssh-cli ./cmd/skyssh-cli


release: ## Build `skywire-visor`, `skywire-cli`, `hypervisor`, `skyssh-cli` and apps without -race flag
	${OPTS} go build -o ${TMP_BUILD_DIR}/skywire-visor ./cmd/skywire-visor
	${OPTS} go build -o ${TMP_BUILD_DIR}/skywire-cli  ./cmd/skywire-cli
	${OPTS} go build -o ${TMP_BUILD_DIR}/setup-node ./cmd/setup-node
	${OPTS} go build -o ${TMP_BUILD_DIR}/hypervisor ./cmd/hypervisor
	${OPTS} go build -o ${TMP_BUILD_DIR}/SSH-cli ./cmd/therealssh-cli
	${OPTS} go build -o ${TMP_BUILD_DIR}/apps/skychat.v1.0 ./cmd/apps/skychat
	${OPTS} go build -o ${TMP_BUILD_DIR}/apps/helloworld.v1.0 ./cmd/apps/helloworld
	${OPTS} go build -o ${TMP_BUILD_DIR}/apps/socksproxy.v1.0 ./cmd/apps/therealproxy
	${OPTS} go build -o ${TMP_BUILD_DIR}/apps/socksproxy-client.v1.0  ./cmd/apps/therealproxy-client
	${OPTS} go build -o ${TMP_BUILD_DIR}/apps/SSH.v1.0  ./cmd/apps/therealssh
	${OPTS} go build -o ${TMP_BUILD_DIR}/apps/SSH-client.v1.0  ./cmd/apps/therealssh-client

run-syslog: ## Run syslog-ng in docker. Logs are mounted under /tmp/syslog
	-rm -rf /tmp/syslog
	-mkdir -p /tmp/syslog
	-docker container rm syslog-ng -f
	docker run -d -p 514:514/udp  -v /tmp/syslog:/var/log  --name syslog-ng balabit/syslog-ng:latest


mod-comm: ## Comments the 'replace' rule in go.mod
	./ci_scripts/go_mod_replace.sh comment go.mod

mod-uncomm: ## Uncomments the 'replace' rule in go.mod
	./ci_scripts/go_mod_replace.sh uncomment go.mod

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

