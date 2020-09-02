[![Build Status](https://travis-ci.com/skycoin/skywire.svg?branch=master)](https://travis-ci.com/skycoin/skywire)

# Skywire Mainnet

- [Skywire Mainnet](#skywire)
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
    - [Windows](#windows)
  - [Apps](#Apps)
    - [App Programming API](#app-programming-api)
  - [Transports](#Transports)
  - [Creating a GitHub release](#creating-a-github-release)
    - [How to create a GitHub release](#how-to-create-a-github-release)

## Build and run

### Requirements

Skywire requires a version of [golang](https://golang.org/) 
with [go modules](https://github.com/golang/go/wiki/Modules) support.

### Build

```bash
# Clone.
$ git clone https://github.com/skycoin/skywire.git
$ cd skywire

# Build.
$ make build # installs all dependencies, build binaries and skywire apps

# Install skywire-visor, skywire-cli and app CLI execs.
$ make install
```

### Configure Skywire Visor

The configuration file provides the configuration for `skywire-visor`. It is a text file in JSON format.

You can generate a default configuration file by running:

```bash
$ skywire-cli visor gen-config
```

Additional options are displayed when `skywire-cli visor gen-config -h` is run.

If you are trying to test features from the develop branch, 
you should use the `-t ` flag when generating config files for `skywire-visor`. 

We will cover certain fields of the configuration file below.

#### `stcp` setup

With `stcp`, you can establish *skywire transports* to other skywire visors with the `tcp` protocol.

As visors are identified with public keys and not IP addresses, 
we need to directly define the associations between IP address and public keys. 
This is done via the configuration file for `skywire-visor`.

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
- The field `stcp.local_address` should only be specified if you want the visor in question to listen for incoming 
`stcp` connection.

#### `hypervisor` setup

Every node can be controlled by one or more hypervisors. The hypervisor allows controlling and configuring multiple visors. 
In order to allow a hypervisor to access a visor, 
the address and PubKey of the hypervisor needs to be configured first on the visor. Here is an example configuration: 

```json
{
  "hypervisors": [{
    "public_key":"02b72766f0ebade8e06d6969b5aeedaff8bf8efd7867f362bb4a63135ab6009775"
  }]
}
```

### Run `skywire-visor`

`skywire-visor` hosts apps, proxies app's requests to remote visors and exposes communication API 
that apps can use to implement communication protocols. 
App binaries are spawned by the visor, 
communication between visor and app is performed via unix pipes provided on app startup.

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

In order to run the visor UI, generate a visor config file with `--hypervisor` flag 

```bash
$ skywire-cli visor gen-config --hypervisor
```

Then visor will start visor UI when it is run:

```bash
$ skywire-visor 
```

You can open up the visor UI on `localhost:8000`. 

### Windows

Skywire node may be run on Windows, but this process requires some manual operations at the moment.

In order to run the skywire visor on Windows, you will need to manually build it. Do not forget the `.exe` extensions.

`go build -o ./skywire-visor.exe ./cmd/skywire-visor`

Apps may be built the same way:

`go build -o ./apps/vpn-client.exe ./cmd/apps/vpn-client`

Apps should be stated in the config without `.exe` extension, as usual.

Some log lines may seem strange due to the Windows encoding in terminal. To make life easier you may change encoding to UTF-8 executing:
```
CHCP 65001
```

#### Not Supported Features

- `dmsgpty`
- `syslog`

Trying to run these will result in failure.

#### Running VPN

Running VPN on Windows requires `wintun` driver to be installed. This may be achieved by installing the driver itself, or simply installing `Wireguard` which installs this driver too.

VPN client requires `local system` user rights to be run. This may be achieved by downloading `PsExec`: https://docs.microsoft.com/en-us/sysinternals/downloads/psexec . Then you may run terminal with the `local system` rights like this:
```
PsExec.exe -i -s C:\Windows\system32\cmd.exe
```

And then simply run skywire from the opened terminal.

### Apps

After `skywire-visor` is up and running with default environment, 
default apps are run with the configuration specified in `skywire-config.json`. 
Refer to the following for usage of the apps:

- [Skychat](/cmd/apps/skychat)
- [Skysocks](/cmd/apps/skysocks) ([Client](/cmd/apps/skysocks-client))

### App Programming API

Skywire supports building custom apps. In order for visor to run a custom app, app binary should be put into the correct directory. This directory is specified in the visor config as `apps_path`. Each app has a list of parameters:
- `app` (required) - contains application name. This should be equal to the binary name stored in the `apps_path` directory to be correctly resolved by the visor;
- `auto_start` (defaults to false) - boolean value, indicates if app should be run on the visor start;
- `port` (required) - port app binds to. Port shouldn't clash with one of the reserved ports of standard Skywire apps (list of such ports is defined below);
- `args` - array of additional arguments to be passed to the app binary. May be totally omitted.

Example part of visor config:
```json5
{
  "apps_path": "./apps",
  "apps": [
    {
      "app": "custom_app",
      "auto_start": true,
      "port": 15,
      "args": ["-c", "./custom_app_config.json"]
    }
  ],
}
```

This way, binary will be run by the visor like this:
```bash
$ ./apps/custom_app -c ./custom_app_config.json
```

#### Reserved App Ports

- `0` - Router
- `1` - Skychat
- `3` - Skysocks

List may be updated.

#### App initialization

Besides list of additional arguments, visor passes 3 environmental variables to each running app. It goes as follows:
- `APP_KEY` - used to authenticate app RPC calls (explained in details below);
- `APP_SERVER_ADDR` - address of RPC server for app to communicate with the visor;
- `VISOR_PK` - pub key of the visor running the app.

These values may be obtained and examined from the environment by any suitable means. For developers working with Go, there is a function `app.ClientConfigFromEnv` which does all the job (may be found [here](./pkg/app/client.go)).

#### App-Visor communication

Visor has RPC gateway to communicate with the apps. Address and the authentication key are passed in the environmental variables as described above. App key is used a prefix to all RPC calls, so the server may distinguish apps and authenticate calls. So, if app needs to call `Dial` method for example, it should call `APP_KEY.Dial`, where `APP_KEY` is the actual key taken from the corresponding environmental variable.

Basically, apps on different visors communicate with each other through Skywire network. App performs an RPC call to its visor, visor communicates with the remote visor, then the remote visor passes data to its app. That's why visor's RPC gateway for the apps mostly contains methods for networking.

#### Visor RPC API

Full info on each call input and output may be found in the [corresponding file](./pkg/app/appserver/rpc_gateway.go). Here will be just a list of minor details. If you're coming from the language different than Go, you'll have to communicate with the RPC gateway directly. 2 important concepts are listener ID and connection ID. Server gives these in response for some of the app's calls. Each time connection is being created (as a result of `Dial` and `Accept` calls for example) server will return connection ID to the app. In order to communicate over this connection, its ID must be passed with the needed RPC call input. This is also true for the listener ID. Each time listener is created (result of `Listen` call), listener ID is being returned to the client. This ID may be used to `Accept` connections for example. For developers working with go, there is a client available which may be constructed by `app.NewClient` call. For details you may consult any of Skywire standard apps and [client code](./pkg/app/client.go). Each connection obtained from this client should be treated as a connection between the current app instance and the remote app.

### Transports

In order for a local Skywire App to communicate with an App running on a remote Skywire visor, 
a transport to that remote Skywire visor needs to be established.

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
4. Create a `git` tag with desired release version and release name: `git tag -a 0.1.0 -m "First release"`, 
where `0.1.0` is release version and `First release` is release name.
5. Push the created tag to the repository: `git push origin 0.1.0`, where `0.1.0` is release version.
6. [Issue a personal GitHub access token.](https://github.com/settings/tokens)
7. Run `GITHUB_TOKEN=your_token make github-release` 
8. [Check the created GitHub release.](https://github.com/skycoin/skywire/releases/)
