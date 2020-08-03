[![Build Status](https://travis-ci.com/skycoin/skywire.svg?branch=master)](https://travis-ci.com/skycoin/skywire)

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
  - [Apps](#Apps)
  - [Transports](#Transports)
  - [Creating a GitHub release](#creating-a-github-release)
    - [How to create a GitHub release](#how-to-create-a-github-release)

## Build and run

### Requirements

Skywire requires a version of [golang](https://golang.org/) with [go modules](https://github.com/golang/go/wiki/Modules) support.

### Build

```bash
# Clone.
$ git clone https://github.com/skycoin/skywire.git
$ cd skywire

# Build.
$ make build # installs all dependencies, build binaries and skywire apps

# Install skywire-visor, skywire-cli, hypervisor and app CLI execs.
$ make install
```

### Configure Skywire Visor

The configuration file provides the configuration for `skywire-visor`. It is a text file in JSON format.

You can generate a default configuration file by running:

```bash
$ skywire-cli visor gen-config
```

Additional options are displayed when `skywire-cli visor gen-config -h` is run.

If you are trying to test features from the develop branch, you should use the `-t ` flag when generating config files for either `skywire-visor` or `hypervisor`. 

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

You can open up the hypervisor UI on `localhost:8000`. 

### Apps

After `skywire-visor` is up and running with default environment, default apps are run with the configuration specified in `skywire-config.json`. Refer to the following for usage of the apps:

- [Skychat](/cmd/apps/skychat)
- [Skysocks](/cmd/apps/skysocks) ([Client](/cmd/apps/skysocks-client))

### Transports

In order for a local Skywire App to communicate with an App running on a remote Skywire visor, a transport to that remote Skywire visor needs to be established.

Transports can be established via the `skywire-cli`.

```bash
# Establish transport to `0276ad1c5e77d7945ad6343a3c36a8014f463653b3375b6e02ebeaa3a21d89e881`.
$ skywire-cli visor add-tp 0276ad1c5e77d7945ad6343a3c36a8014f463653b3375b6e02ebeaa3a21d89e881

# List established transports.
$ skywire-cli visor ls-tp
```

## Creating a GitHub release

To maintain actual `skywire-visor` state on users' Skywire nodes we have a mechanism for updating `skywire-visor` binaries. 
Binaries for each version are uploaded to [GitHub releases](https://github.com/skycoin/skywire/releases/).
We use [goreleaser](https://goreleaser.com) for creating them.

### How to create a GitHub release

1. Make sure that `git` and [goreleaser](https://goreleaser.com/install) are installed.
2. Checkout to a commit you would like to create a release against.
3. Make sure that `git status` is in clean state.
4. Create a `git` tag with desired release version and release name: `git tag -a 0.1.0 -m "First release"`, where `0.1.0` is release version and `First release` is release name.
5. Push the created tag to the repository: `git push origin 0.1.0`, where `0.1.0` is release version.
6. [Issue a personal GitHub access token.](https://github.com/settings/tokens)
7. Run `GITHUB_TOKEN=your_token make github-release` 
8. [Check the created GitHub release.](https://github.com/skycoin/skywire/releases/)
