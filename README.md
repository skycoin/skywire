[![Build Status](https://travis-ci.com/SkycoinProject/skywire-mainnet.svg?branch=master)](https://travis-ci.com/SkycoinProject/skywire-mainnet)

# Skywire Mainnet

- [Skywire Mainnet](#skywire-mainnet)
  - [Build and run](#build-and-run)
    - [Requirements](#requirements)
    - [Build](#build)
    - [Configure](#configure)
      - [`stcp` setup](#stcp-setup)
      - [`dmsgpty` setup](#dmsgpty-setup)
      - [`hypervisor` setup](#hypervisor-setup)
    - [Run `skywire-visor`](#run-skywire-visor)
    - [Run `skywire-cli`](#run-skywire-cli)
    - [Run `hypervisor`](#run-hypervisor)
    - [Apps](#apps)
    - [Transports](#transports)
  - [App programming API](#app-programming-api)
  - [Testing](#testing)
    - [Testing with default settings](#testing-with-default-settings)
    - [Customization with environment variables](#customization-with-environment-variables)
      - [$TEST_OPTS](#test_opts)
      - [$TEST_LOGGING_LEVEL](#test_logging_level)
      - [$SYSLOG_OPTS](#syslog_opts)
  - [Running skywire in docker containers](#running-skywire-in-docker-containers)
    - [Run dockerized `skywire-visor`](#run-dockerized-skywire-visor)
      - [Structure of `./node`](#structure-of-node)
    - [Refresh and restart `SKY01`](#refresh-and-restart-sky01)
    - [Customization of dockers](#customization-of-dockers)
      - [1. DOCKER_IMAGE](#1-docker_image)
      - [2. DOCKER_NETWORK](#2-docker_network)
      - [3. DOCKER_NODE](#3-docker_node)
      - [4. DOCKER_OPTS](#4-docker_opts)
    - [Dockerized `skywire-visor` recipes](#dockerized-skywire-visor-recipes)
      - [1. Get Public Key of docker-node](#1-get-public-key-of-docker-node)
      - [2. Get an IP of node](#2-get-an-ip-of-node)
      - [3. Open in browser containerized `skychat` application](#3-open-in-browser-containerized-skychat-application)
      - [4. Create new dockerized `skywire-visor`s](#4-create-new-dockerized-skywire-visors)
      - [5. Env-vars for development-/testing- purposes](#5-env-vars-for-development-testing--purposes)
      - [6. "Hello-Mike-Hello-Joe" test](#6-hello-mike-hello-joe-test)
  - [Creating a GitHub release](#creating-a-github-release)
    - [How to create a GitHub release](#how-to-create-a-github-release)

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

# Install skywire-visor, skywire-cli, hypervisor and app CLI execs.
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
$ skywire-cli visor gen-config
```

Additional options are displayed when `skywire-cli visor gen-config -h` is run.

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

#### `hypervisor` setup

Every node can be controlled by one or more hypervisors. The hypervisor allows to control and configure multiple visors. In order to allow a hypervisor to access a visor, the address and PubKey of the hypervisor needs to be configured first on the visor. Here is an example configuration: 

```json
  "hypervisors":[{
		"public_key":"02b72766f0ebade8e06d6969b5aeedaff8bf8efd7867f362bb4a63135ab6009775",
	       	"address":"127.0.0.1:7080"
	}],
```

### Run `skywire-visor`

`skywire-visor` hosts apps, proxies app's requests to remote visors and exposes communication API that apps can use to implement communication protocols. App binaries are spawned by the visor, communication between visor and app is performed via unix pipes provided on app startup.

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

### Run `hypervisor`

In order to run the hypervisor, generate a hypervisor config file with 

```bash
$ hypervisor gen-config
```

Then you can start a hypervisor with:

```bash
$ hypervisor 
```

### Apps

After `skywire-visor` is up and running with default environment, default apps are run with the configuration specified in `skywire-config.json`. Refer to the following for usage of the default apps:

- [Chat](/cmd/apps/skychat)
- [Hello World](/cmd/apps/helloworld)
- [Sky Socks](/cmd/apps/skysocks) ([Client](/cmd/apps/skysocks-client))

### Transports

In order for a local Skywire App to communicate with an App running on a remote Skywire visor, a transport to that remote Skywire visor needs to be established.

Transports can be established via the `skywire-cli`.

```bash
# Establish transport to `0276ad1c5e77d7945ad6343a3c36a8014f463653b3375b6e02ebeaa3a21d89e881`.
$ skywire-cli visor add-tp 0276ad1c5e77d7945ad6343a3c36a8014f463653b3375b6e02ebeaa3a21d89e881

# List established transports.
$ skywire-cli visor ls-tp
```

## App programming API

App is a generic binary that can be executed by the visor. On app
startup visor will open pair of unix pipes that will be used for
communication between app and visor. `app` packages exposes
communication API over the pipe.

```golang
// Config defines configuration parameters for App
&app.Config{AppName: "helloworld", ProtocolVersion: "0.0.1"}
// Setup setups app using default pair of pipes
func Setup(config *Config) (*App, error) {}

// Accept awaits for incoming route group confirmation request from a Visor and
// returns net.Conn for a received route group.
func (app *App) Accept() (net.Conn, error) {}

// Addr implements net.Addr for App connections.
&Addr{PubKey: pk, Port: 12}
// Dial sends create route group request to a Visor and returns net.Conn for created route group.
func (app *App) Dial(raddr *Addr) (net.Conn, error) {}

// Close implements io.Closer for App.
func (app *App) Close() error {}
```

## Creating a GitHub release

To maintain actual `skywire-visor` state on users' Skywire nodes we have a mechanism for updating `skywire-visor` binaries. 
Binaries for each version are uploaded to [GitHub releases](https://github.com/SkycoinProject/skywire-mainnet/releases/).
We use [goreleaser](https://goreleaser.com) for creating them.

### How to create a GitHub release

1. Make sure that `git` and [goreleaser](https://goreleaser.com/install) are installed.
2. Checkout to a commit you would like to create a release against.
3. Make sure that `git status` is in clean state.
4. Create a `git` tag with desired release version and release name: `git tag -a 0.1.0 -m "First release"`, where `0.1.0` is release version and `First release` is release name.
5. Push the created tag to the repository: `git push origin 0.1.0`, where `0.1.0` is release version.
6. [Issue a personal GitHub access token.](https://github.com/settings/tokens)
7. Run `GITHUB_TOKEN=your_token make github-release` 
8. [Check the created GitHub release.](https://github.com/SkycoinProject/skywire-mainnet/releases/)
