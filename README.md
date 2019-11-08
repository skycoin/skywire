[![Build Status](https://travis-ci.com/SkycoinProject/skywire-mainnet.svg?branch=master)](https://travis-ci.com/SkycoinProject/skywire-mainnet)

# Skywire Mainnet

- [Skywire Mainnet](#skywire-mainnet)
  - [Build and run](#build-and-run)
    - [Requirements](#requirements)
    - [Build](#build)
    - [Configure](#configure)
      - [`stcp` setup](#stcp-setup)
      - [`dmsgpty` setup](#dmsgpty-setup)
    - [Run `skywire-visor`](#run-skywire-visor)
    - [Run `skywire-cli`](#run-skywire-cli)
    - [Run `dmsgpty`](#run-dmsgpty)
    - [Apps](#apps)
    - [Transports](#transports)
  - [App programming API](#app-programming-api)
  - [Testing](#testing)
    - [Testing with default settings](#testing-with-default-settings)
    - [Customization with environment variables](#customization-with-environment-variables)
      - [$TEST_OPTS](#testopts)
      - [$TEST_LOGGING_LEVEL](#testlogginglevel)
      - [$SYSLOG_OPTS](#syslogopts)
  - [Running skywire in docker containers](#running-skywire-in-docker-containers)
    - [Run dockerized `skywire-visor`](#run-dockerized-skywire-visor)
      - [Structure of `./node`](#structure-of-node)
    - [Refresh and restart `SKY01`](#refresh-and-restart-sky01)
    - [Customization of dockers](#customization-of-dockers)
      - [1. DOCKER_IMAGE](#1-dockerimage)
      - [2. DOCKER_NETWORK](#2-dockernetwork)
      - [3. DOCKER_NODE](#3-dockernode)
      - [4. DOCKER_OPTS](#4-dockeropts)
    - [Dockerized `skywire-visor` recipes](#dockerized-skywire-visor-recipes)
      - [1. Get Public Key of docker-node](#1-get-public-key-of-docker-node)
      - [2. Get an IP of node](#2-get-an-ip-of-node)
      - [3. Open in browser containerized `skychat` application](#3-open-in-browser-containerized-skychat-application)
      - [4. Create new dockerized `skywire-visor`s](#4-create-new-dockerized-skywire-visors)
      - [5. Env-vars for development-/testing- purposes](#5-env-vars-for-development-testing--purposes)
      - [6. "Hello-Mike-Hello-Joe" test](#6-%22hello-mike-hello-joe%22-test)

**NOTE:** The project is still under heavy development and should only be used for testing purposes right now. Miners should not switch over to this project if they want to receive testnet rewards. 

## Build and run

### Requirements

Skywire requires a version of [golang](https://golang.org/) with [go modules](https://github.com/golang/go/wiki/Modules) support.

### Build

```bash
# Clone.
$ git clone https://github.com/SkycoinProject/skywire-mainnet.git
$ cd skywire-mainnet

# Build.
$ make build # installs all dependencies, build binaries and skywire apps

# Install skywire-visor, skywire-cli, dmsgpty, hypervisor and app CLI execs.
$ make install
```

**Note: Environment variable OPTS**

Build can be customized with environment variable `OPTS` with default value `GO111MODULE=on`

E.g.

```bash
$ export OPTS="GO111MODULE=on GOOS=darwin"
$ make
# or
$ OPTS="GSO111MODULE=on GOOS=linux GOARCH=arm" make
```

### Configure

The configuration file provides the configuration for `skywire-visor`. It is a text file in JSON format.

You can generate a default configuration file by running:

```bash
$ skywire-cli node gen-config
```

Additional options are displayed when `skywire-cli node gen-config -h` is run.

We will cover certain fields of the configuration file below.

#### `stcp` setup

With `stcp`, you can establish *skywire transports* to other skywire visors with the `tcp` protocol.

As visors are identified with public keys and not IP addresses, we need to directly define the associations between IP address and public keys. This is done via the configuration file for `skywire-visor`.

```json
{
  "stcp": {
    "pk_table": {
      "024a2dd77de324d543561a6d9e62791723be26ddf6b9587060a10b9ba498e096f1": "127.0.0.1:7031",
      "0327396b1241a650163d5bc72a7970f6dfbcca3f3d67ab3b15be9fa5c8da532c08": "127.0.0.1:7032"
    },
    "local_address": "127.0.0.1:7033"
  }
}
```

In the above example, we have two other visors running on localhost (that we wish to connect to via `stcp`).
- The field `stcp.pk_table` holds the associations of `<public_key>` to `<ip_address>:<port>`.
- The field `stcp.local_address` should only be specified if you want the visor in question to listen for incoming `stcp` connection.

#### `dmsgpty` setup

With `dmsgpty`, you can access a remote `pty` on a remote `skywire-visor`. Note that `dmsgpty` can only access remote visors that have a dmsg transport directly established with the client visor. Having a route connecting two visors together does not allow `dmsgpty` to function between the two visors.

Here is an example configuration for enabling the `dmsgpty` server within `skywire-visor`:

```json5
{
  "dmsg_pty": {
  
    // "port" provides the dmsg port to listen for remote pty requests.
    "port": 233, 
    
    // "authorization_file" is the path to a JSON file containing an array of whitelisted public keys.
    "authorization_file": "./dmsgpty/whitelist.json",
    
    // "cli_network" is the network to host the dmsgpty CLI.
    "cli_network": "unix",
    
    // "cli_address" is the address to host the dmsgpty CLI.
    "cli_address": "/tmp/dmsgpty.sock"
  }
}
```

For `dmsgpty` usage, refer to [#run-dmsgpty](#run-dmsgpty).

### Run `skywire-visor`

`skywire-visor` hosts apps, proxies app's requests to remote nodes and exposes communication API that apps can use to implement communication protocols. App binaries are spawned by the node, communication between node and app is performed via unix pipes provided on app startup.

Note that `skywire-visor` requires a valid configuration file in order to execute.

```bash
# Run skywire-visor. It takes one argument; the path of a configuration file (`skywire-config.json` if unspecified).
$ skywire-visor skywire-config.json
```

### Run `skywire-cli`

The `skywire-cli` tool is used to control the `skywire-visor`. Refer to the help menu for usage:

```bash
$ skywire-cli -h
```

### Run `dmsgpty`

`dmsgpty` allows the user to access local and remote pty sessions via the `skywire-visor`. To use `dmsgpty`, one needs to have a `skywire-visor` up and running with the `dmsgpty-server` properly configured (as specified here: [#dmsgpty-setup](#dmsgpty-setup)).

To access a remote pty, the local `skywire-visor` needs to have a direct dmsg transport with the remote visor, and the remote visor needs to have the local visor's public key included in it's dmsgpty whitelist.

One can add public key entries to the `"authorization_file"` via the following command:

```bash
$ dmsgpty whitelist-add --pk 0327396b1241a650163d5bc72a7970f6dfbcca3f3d67ab3b15be9fa5c8da532c08
```

To open an interactive pty shell on a remote visor, who's public key is `0327396b1241a650163d5bc72a7970f6dfbcca3f3d67ab3b15be9fa5c8da532c08`, run the following command:

```bash
$ dmsgpty -a 0327396b1241a650163d5bc72a7970f6dfbcca3f3d67ab3b15be9fa5c8da532c08 
```

To open a non-interactive shell and run a command:

```bash
$ dmsgpty --addr='0327396b1241a650163d5bc72a7970f6dfbcca3f3d67ab3b15be9fa5c8da532c08' --cmd='echo' --arg='hello world'
```

### Apps

After `skywire-visor` is up and running with default environment, default apps are run with the configuration specified in `skywire-config.json`. Refer to the following for usage of the default apps:

- [Chat](/cmd/apps/skychat)
- [Hello World](/cmd/apps/helloworld)
- [The Real Proxy](/cmd/apps/therealproxy) ([Client](/cmd/apps/therealproxy-client))

### Transports

In order for a local Skywire App to communicate with an App running on a remote Skywire visor, a transport to that remote Skywire visor needs to be established.

Transports can be established via the `skywire-cli`.

```bash
# Establish transport to `0276ad1c5e77d7945ad6343a3c36a8014f463653b3375b6e02ebeaa3a21d89e881`.
$ skywire-cli node add-tp 0276ad1c5e77d7945ad6343a3c36a8014f463653b3375b6e02ebeaa3a21d89e881

# List established transports.
$ skywire-cli node ls-tp
```

## App programming API

App is a generic binary that can be executed by the node. On app
startup node will open pair of unix pipes that will be used for
communication between app and node. `app` packages exposes
communication API over the pipe.

```golang
// Config defines configuration parameters for App
&app.Config{AppName: "helloworld", AppVersion: "1.0", ProtocolVersion: "0.0.1"}
// Setup setups app using default pair of pipes
func Setup(config *Config) (*App, error) {}

// Accept awaits for incoming loop confirmation request from a Node and
// returns net.Conn for a received loop.
func (app *App) Accept() (net.Conn, error) {}

// Addr implements net.Addr for App connections.
&Addr{PubKey: pk, Port: 12}
// Dial sends create loop request to a Node and returns net.Conn for created loop.
func (app *App) Dial(raddr *Addr) (net.Conn, error) {}

// Close implements io.Closer for App.
func (app *App) Close() error {}
```

## Testing

### Testing with default settings

```bash
$ make test
```

### Customization with environment variables

#### $TEST_OPTS

Options for `go test` could be customized with $TEST_OPTS variable

E.g.
```bash
$ export TEST_OPTS="-race -tags no_ci -timeout 90s -v"
$ make test
```

#### $TEST_LOGGING_LEVEL

By default all log messages during tests are disabled.
In case of need to turn on log messages it could be achieved by setting $TEST_LOGGING_LEVEL variable

Possible values:
- "debug"
- "info", "notice"
- "warn", "warning"
- "error"
- "fatal", "critical"
- "panic"

E.g.
```bash 
$ export TEST_LOGGING_LEVEL="info"
$ go clean -testcache || go test ./pkg/transport -v -run ExampleManager_CreateTransport
$ unset TEST_LOGGING_LEVEL
$ go clean -testcache || go test ./pkg/transport -v
```

#### $SYSLOG_OPTS

In case of need to collect logs in syslog during integration tests $SYSLOG_OPTS variable can be used.

E.g.
```bash
$ make run_syslog ## run syslog-ng in docker container with logs mounted to /tmp/syslog
$ export SYSLOG_OPTS='--syslog localhost:514'
$ make integration-run-messaging ## or other integration-run-* goal
$ sudo cat /tmp/syslog/messages ## collected logs from NodeA, NodeB, NodeC instances
```

## Running skywire in docker containers

There are two make goals for running in development environment dockerized `skywire-visor`.

### Run dockerized `skywire-visor`

```bash
$ make docker-run
```

This will:

- create docker image `skywire-runner` for running `skywire-visor`
- create docker network `SKYNET` (can be customized)
- create docker volume ./node with linux binaries and apps
- create container  `SKY01` and starts it (can be customized)

#### Structure of `./node`

```
./node
├── apps                            # node `apps` compiled with DOCKER_OPTS
│   ├── skychat.v1.0                #
│   ├── helloworld.v1.0             #
│   ├── socksproxy-client.v1.0      #
│   ├── socksproxy.v1.0             #
├── local                           # **Created inside docker**
│   ├── skychat                     #  according to "local_path" in skywire-config.json
│   ├── socksproxy                  #                       #
├── PK                              # contains public key of node
├── skywire                         # db & logs. **Created inside docker**
│   ├── routing.db                  #
│   └── transport_logs              #
├── skywire-config.json             # config of node
└── skywire-visor                   # `skywire-visor` binary compiled with DOCKER_OPTS
```

Directory `./node` is mounted as docker volume for `skywire-visor` container.

Inside docker container it is mounted on `/sky`

Structure of `./skywire-visor` partially replicates structure of project root directory.

Note that files created inside docker container has ownership `root:root`, 
so in case you want to `rm -rf ./node` (or other file operations) - you will need `sudo` it.

Look at "Recipes: Creating new dockerized node" for further details.

### Refresh and restart `SKY01`

```bash
$ make refresh-node
```

This will:

 - stops running node
 - recompiles `skywire-visor` for container
 - start node again

### Customization of dockers

#### 1. DOCKER_IMAGE

Docker image for running `skywire-visor`.

Default value: `skywire-runner` (built with `make docker-image`)

Other images can be used.
E.g.

```bash
DOCKER_IMAGE=golang make docker-run #buildpack-deps:stretch-scm is OK too
```

#### 2. DOCKER_NETWORK

Name of virtual network for `skywire-visor`

Default value: SKYNET

#### 3. DOCKER_NODE

Name of container for `skywire-visor`

Default value: SKY01

#### 4. DOCKER_OPTS

`go build` options for binaries and apps in container.

Default value: "GO111MODULE=on GOOS=linux"

### Dockerized `skywire-visor` recipes

#### 1. Get Public Key of docker-node

```bash
$ cat ./node/skywire-config.json|grep static_public_key |cut -d ':' -f2 |tr -d '"'','' '
# 029be6fa68c13e9222553035cc1636d98fb36a888aa569d9ce8aa58caa2c651b45
```

#### 2. Get an IP of node

```bash
$ docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' SKY01
# 192.168.112
```

#### 3. Open in browser containerized `skychat` application

```bash
$ firefox http://$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' SKY01):8000  
```

#### 4. Create new dockerized `skywire-visor`s

In case you need more dockerized nodes or maybe it's needed to customize node
let's look how to create new node.

```bash
# 1. We need a folder for docker volume
$ mkdir /tmp/SKYNODE
# 2. compile  `skywire-visor`
$ GO111MODULE=on GOOS=linux go build -o /tmp/SKYNODE/skywire-visor ./cmd/skywire-visor
# 3. compile apps
$ GO111MODULE=on GOOS=linux go build -o /tmp/SKYNODE/apps/skychat.v1.0 ./cmd/apps/skychat
$ GO111MODULE=on GOOS=linux go build -o /tmp/SKYNODE/apps/helloworld.v1.0 ./cmd/apps/helloworld
$ GO111MODULE=on GOOS=linux go build -o /tmp/SKYNODE/apps/socksproxy.v1.0 ./cmd/apps/therealproxy
# 4. Create skywire-config.json for node
$ skywire-cli node gen-config -o /tmp/SKYNODE/skywire-config.json
# 2019/03/15 16:43:49 Done!
$ tree /tmp/SKYNODE
# /tmp/SKYNODE
# ├── apps
# │   ├── skychat.v1.0
# │   ├── helloworld.v1.0
# │   ├── socksproxy.v1.0
# ├── skywire-config.json
# └── skywire-visor
# So far so good. We prepared docker volume. Now we can:
$ docker run -it -v /tmp/SKYNODE:/sky --network=SKYNET --name=SKYNODE skywire-runner bash -c "cd /sky && ./skywire-visor"
# [2019-03-15T13:55:08Z] INFO [messenger]: Opened new link with the server # 02a49bc0aa1b5b78f638e9189be4ed095bac5d6839c828465a8350f80ac07629c0
# [2019-03-15T13:55:08Z] INFO [messenger]: Updating discovery entry
# [2019-03-15T13:55:10Z] INFO [skywire]: Connected to messaging servers
# [2019-03-15T13:55:10Z] INFO [skywire]: Starting skychat.v1.0
# [2019-03-15T13:55:10Z] INFO [skywire]: Starting RPC interface on 127.0.0.1:3435
# [2019-03-15T13:55:10Z] INFO [skywire]: Starting socksproxy.v1.0
# [2019-03-15T13:55:10Z] INFO [skywire]: Starting packet router
# [2019-03-15T13:55:10Z] INFO [router]: Starting router
# [2019-03-15T13:55:10Z] INFO [trmanager]: Starting transport manager
# [2019-03-15T13:55:10Z] INFO [router]: Got new App request with type Init: {"app-name":"skychat",# "app-version":"1.0","protocol-version":"0.0.1"}
# [2019-03-15T13:55:10Z] INFO [router]: Handshaked new connection with the app skychat.v1.0
# [2019-03-15T13:55:10Z] INFO [skychat.v1.0]: 2019/03/15 13:55:10 Serving HTTP on :8000
# [2019-03-15T13:55:10Z] INFO [router]: Got new App request with type Init: {"app-name":"SSH",# "app-version":"1.0","protocol-version":"0.0.1"}
# [2019-03-15T13:55:10Z] INFO [router]: Handshaked new connection with the app SSH.v1.0
# [2019-03-15T13:55:10Z] INFO [router]: Got new App request with type Init: {"app-name":"socksproxy",# "app-version":"1.0","protocol-version":"0.0.1"}
# [2019-03-15T13:55:10Z] INFO [router]: Handshaked new connection with the app socksproxy.v1.0
```

Note that in this example docker is running in non-detached mode - it could be useful in some scenarios.

Instead of skywire-runner you can use:

- `golang`, `buildpack-deps:stretch-scm` "as is"
- and `debian`, `ubuntu` - after `apt-get install ca-certificates` in them. Look in `skywire-runner.Dockerfile` for example

#### 5. Env-vars for development-/testing- purposes

```bash
export SW_NODE_A=127.0.0.1
export SW_NODE_A_PK=$(cat ./skywire-config.json|grep static_public_key |cut -d ':' -f2 |tr -d '"'','' ')
export SW_NODE_B=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' SKY01)
export SW_NODE_B_PK=$(cat ./node/skywire-config.json|grep static_public_key |cut -d ':' -f2 |tr -d '"'','' ')
```

#### 6. "Hello-Mike-Hello-Joe" test

Idea of test from Erlang classics: <https://youtu.be/uKfKtXYLG78?t=120>

```bash
# Setup: run skywire-visors on host and in docker
$ make run
$ make docker-run
# Open in browser skychat application
$ firefox http://$SW_NODE_B:8000  &
# add transport
$ ./skywire-cli add-transport $SW_NODE_B_PK
# "Hello Mike!" - "Hello Joe!" - "System is working!"
$ curl --data  {'"recipient":"'$SW_NODE_A_PK'", "message":"Hello Mike!"}' -X POST  http://$SW_NODE_B:8000/message
$ curl --data  {'"recipient":"'$SW_NODE_B_PK'", "message":"Hello Joe!"}' -X POST  http://$SW_NODE_A:8000/message
$ curl --data  {'"recipient":"'$SW_NODE_A_PK'", "message":"System is working!"}' -X POST  http://$SW_NODE_B:8000/message
# Teardown
$ make stop && make docker-stop
```
