[![Build Status](https://travis-ci.com/skycoin/skywire.svg?branch=master)](https://travis-ci.com/skycoin/skywire)

Skywire requires a Golang version of `1.16` or higher.

# Skywire

  * [Commands and Subcommands](#commands-and-subcommands)
  * [App documentation](#app-documentation)
  * [Installing Skywire](#installing-skywire)
  * [Dependencies](#dependencies)
    * [Build Deps](#build-deps)
    * [Runtime Deps](#runtime-deps)
    * [Testing Deps](#testing-deps)
  * [Testing](#testing)
  * [Development](#development)
  * [Run from source](#run-from-source)
  * [Build docker image](#build-docker-image)
  * [Files and folders created by skywire at runtime](#files-and-folders-created-by-skywire-at-runtime)
  * [Skywire Configuration](#skywire-configuration)
    * [Expose hypervisorUI](#expose-hypervisorui)
    * [Add remote hypervisor](#add-remote-hypervisor)
  * [Run skywire\-visor](#run-skywire-visor)
    * [Using Skywire forwarding \- http server over skywire](#using-skywire-forwarding---http-server-over-skywire)
    * [Transport setup](#transport-setup)
    * [Routing Rules](#routing-rules)
    * [Using the Skywire VPN](#using-the-skywire-vpn)
    * [Using the Skywire SOCKS5 client](#using-the-skywire-socks5-client)
  * [Package Build Overview](#package-build-overview)
  * [Prepare \- path setup](#prepare---path-setup)
  * [Build](#build)
  * [Install](#install)
  * [Skywire\-autoconfig](#skywire-autoconfig)
    * [Package tree](#package-tree)
  * [Creating a GitHub release](#creating-a-github-release)
    * [How to create a GitHub release](#how-to-create-a-github-release)

## Commands and Subcommands

Documentation on skywire-cli interface as well as available flags for skywire-visor

* [skywire-cli](cmd/skywire-cli/README.md)
* [skywire-visor](cmd/skywire-visor/README.md)

## App documentation

Apps are not executed by the user, but hosted by the visor process

* [API](docs/skywire_app_api.md)
* [skychat](cmd/apps/skychat/README.md)
* [skysocks](cmd/apps/skysocks/README.md)
* [skysocks-client](cmd/apps/skysocks-client/README.md)
* [vpn-client](cmd/apps/vpn-client/README.md)
* [vpn-server](cmd/apps/vpn-server/README.md)
* [example-server-app](example/example-server-app/README.md)
* [example-client-app](example/example-client-app/README.md)

further documentation can be found in the [skywire wiki](https://github.com/skycoin/skywire/wiki)

## Installing Skywire

Pre-compiled resouces

* [Windows installer](https://github.com/skycoin/skywire/releases/download/v1.3.6/skywire-installer-v1.3.6-windows-amd64.msi)
* [MacOS amd64 package](https://github.com/skycoin/skywire/releases/download/v1.3.6/skywire-installer-v1.3.6-darwin-amd64.pkg)
* [MacOS m1 / arm64](https://github.com/skycoin/skywire/releases/download/v1.3.6/skywire-installer-v1.3.6-darwin-arm64.pkg)
* [Debian Package Installation Guide](https://github.com/skycoin/skywire/wiki/Skywire-Package-Installation)
* [Binary Releases](https://github.com/skycoin/skywire/releases)

## Dependencies

### Build Deps

* `golang`
* `git` (optional)

basic setup of `go` is further described [here](https://github.com/skycoin/skycoin/blob/develop/INSTALLATION.md#setup-your-gopath)

* `musl` and `kernel-headers-musl` or equivalent - _for static compilation_

For more information on static compilation, see [docs/static-builds.md](docs/static-builds.md).

### Runtime Deps

* ` glibc` or `libc6` _unless statically compiled_

### Testing Deps

* `golangci-lint`
* `goimports-reviser` from github.com/incu6us/goimports-reviser/v2
* `goimports` from golang.org/x/tools/cmd/goimports

## Testing

Before pushing commits to a pull request, its customary in case of edits to any of the golang source code to run the following:

```
make format check
```

`make check` will run `make test` as well. To explicitly run tests, use `make test`

## Development

To compile skywire directly from this repository for local testing and development

```
git clone https://github.com/skycoin/skywire
cd skywire
#for the latest commits, check out the develop branch
git checkout develop
make build
```
`make build` builds the binaries and apps with `go build`

`skywire-cli` and `skywire-visor` binaries will populate in the current directory; app binaries will populate the `apps` directory

Build output:

```
├──skywire-cli
└─┬skywire-visor
  └─┬apps
    ├──skychat
    ├──skysocks
    ├──skysocks-client
    ├──vpn-client
    ├──vpn-server
    └──skychat
```

'install' these executables to the `GOPATH`
```
make install
```

To run skywire from this point, first generate a config

```
./skywire-cli config gen -birx
```
`-b --bestproto` use the best protocol (dmsg | direct) to connect to the skywire production deployment
`-i --ishv` create a  local hypervisor configuration
`-r --regen` regenerate a config which may already exist, retaining the keys
`-x --retainhv` retain any remote hypervisors which are set in the config

The visor can then be started with
```
sudo ./skywire-visor
```

__Note: root permissions are currently required for vpn client and server applications__

## Run from source

Running from source as outlined in this section does not write the config to disk or explicitly compile any binaries. The config is piped from skywire-cli stdout to the visor stdin, and all are executed via `go run`.

```
 git clone https://github.com/skycoin/skywire.git
 cd skywire
 #for the latest commits, check out the develop branch
 git checkout develop
 make run-source
```

## Build docker image
```
$ ./ci_scripts/docker-push.sh -t $(git rev-parse --abbrev-ref HEAD) -b
```

## Files and folders created by skywire at runtime
note not all of these files will be created by default
```
├──skywire-config.json
└─┬local
  ├── apps-pid.txt
  ├── node-info.json
  ├── node-info.sha
  ├── reward.txt
  ├── skychat
  ├── skychat_log.db
  ├── skysocks
  ├── skysocks-client
  ├── skysocks-client_log.db
  ├── skysocks_log.db
  └── transport_logs
      ├── 2023-03-06.csv
      ├── 2023-03-07.csv
      ├── 2023-03-08.csv
      ├── 2023-03-09.csv
      └── 2023-03-10.csv
```

Some of these files are served via the [dmsghttp logserver](https://github.com/skycoin/skywire/wiki/DMSGHTTP-logserver)

## Skywire Configuration

The skywire visor requires a config file to run. This config is a json-formatted file produced by `skywire-cli config gen`

The `skywire-autoconfig` script included with the skywire package handles config generation and updates for the user who has installed the package.

Examples of config generation and command / flag documentation can be found in the [cmd/skywire-cli/README.md](cmd/skywire-cli/README.md) and [cmd/skywire-visor/README.md](cmd/skywire-visor/README.md)

The most important flags are noted below

### Expose hypervisorUI

In order to expose the hypervisor UI, generate a config file with `--is-hypervisor` or `-i` flag:

```
 skywire-cli config gen -i
```

Docker container will create config automatically for you. To run it manually:

```
 docker run --rm -v <YOUR_CONFIG_DIR>:/opt/skywire \
  skycoin/skywire:test skywire-cli config gen -i
```

After starting up the visor, the UI will be exposed by default on `localhost:8000`.

### Add remote hypervisor

Every visor can be controlled by one or more hypervisors. To allow a hypervisor to access a visor, the PubKey of the
hypervisor needs to be specified in the configuration file. You can add a remote hypervisor to the config with:

```
skywire-cli config update --hypervisor-pks <public-key>
```
or
```
skywire-cli config gen --hvpk <public-key>
```

alternatively, this can be done with the skywire-autoconfg script
```
skywire-autoconfig <public-key>
```

Or from docker image:

```
docker run --rm -v <YOUR_CONFIG_DIR>:/opt/skywire \
  skycoin/skywire:test skywire-cli config update hypervisor-pks <public-key>
```

Or from docker image:/* #nosec */

```
docker run --rm -v <YOUR_CONFIG_DIR>:/opt/skywire \
  skycoin/skywire:latest skywire-cli update-config hypervisor-pks <public-key>
```

## Run `skywire-visor`

`skywire-visor` hosts apps and is an applications gateway to the Skywire network.

`skywire-visor` requires a valid configuration to be provided. If you want to run a VPN client locally, run the visor
as `sudo`.

```
 sudo skywire-visor -c skywire-config.json
```
if the default `skywire-config.json` exists in the current dir, this can be shortened to
```
 sudo skywire-visor
```

Or from docker image:

```
# with custom config mounted on docker volume
docker run --rm -p 8000:8000 -v <YOUR_CONFIG_DIR>:/opt/skywire --name=skywire skycoin/skywire:test skywire-visor -c /opt/skywire/<YOUR_CONFIG_NAME>.json
# without custom config (config is automatically generated)
docker run --rm -p 8000:8000 --name=skywire skycoin/skywire:test skywire-visor
```

`skywire-visor` can be run on Windows. The setup requires additional setup steps that are specified
in [the docs](docs/windows-setup.md) if not using the windows .msi

### Using Skywire forwarding - http server over skywire

The skywire-cli subcommand `skywire-cli fwd` is used to register and connect to http servers over the skywire connection

- [skywire forwarding](docs/skywire_forwarding.md)

assuming that the local application you wish to forward is running on port `8080`
```
skywire-cli fwd -p 8080
```

list forwarded ports
```
skywire-cli fwd -l
```

deregister a port / turn off forwarding
```
skywire-cli fwd -d 8080
```

To consume the skyfwd connection / reverse proxy back to localhost use `skywire-cli rev`
```
skywire-cli rev -p 8080 -r 8080 -k <public-key>
```

list existing connections
```
skywire-cli rev -l
```

remove a configured connection
```
skywire-cli rev -d <id>
```

### Transport setup

A Transport represents a bidirectional line of communication between two Skywire Visors
- [Transports](https://github.com/skycoin/skywire/wiki/Transports)

Transports are automatically established when a client application connects to a server application.
Their creation is attempted in the following order:
- stcpr
- sudph
- dmsg

Transports can be manually created. Existing suitable transports will be automatically used by client applications when they are started.

To create a transport, first copy the public key of an online visor from the uptime tracker (or service discovery endpoints):
https://ut.skywire.skycoin.com/uptimes

```
skywire-cli visor tp add -t <transport-type> <public-key>
```

view established transports
```
skywire-cli visor tp ls
```

remove a transport
```
skywire-cli visor tp rm <transport-id>
```

### Routing Rules

In the current era of internet connectivity, certain direct connections between servers in different countries are throttled or may drop intermittently. It is advantageous, in these instances, to establish an indirect or multi-hop route.

Establishing skywire routing rules brings the advantage of an anonymizing overlay to the connection. The intermediate visor handling a certain packet only knows the previous and next hop of that packet. All packets through skywire are of uniform size, stripped of their headers and fuzzed to appear no differently from noise.

__disclaimer: this process is pending improvements & revisions!__

To create a route, first copy the public key of an online visor from the uptime tracker (or service discovery endpoints):
https://ut.skywire.skycoin.com/uptimes

```
skywire-cli visor route add-rule app <route-id> $(skywire-cli visor pk) <local-port> <public-key> <remote-port>
```

to understand these arguments, observe the help menu for `skywire-cli visor route add-rule`
```
Usage:
  skywire-cli visor route add-rule app \
               <route-id> \
               <local-pk> \
               <local-port> \
               <remote-pk> \
               <remote-port> \
               ||  [flags]

Flags:
  -i, --rid string   route id
  -l, --lpk string   local public key
  -m, --lpt string   local port
  -p, --rpk string   remote pk
  -q, --rpt string   remote port

Global Flags:
      --keep-alive duration   timeout for rule expiration (default 30s)
```

<local-port> <remote-port> and <route-id> are all just integers. it's suggested to create the first route with id 1, unless another route exists with that id

the port numbers are similarly inconsequential.

__note: the skywire router is pending refactorization__

### Using the Skywire VPN

The following documentation exists for vpn server / client setup and usage:
- [Setup the Skywire VPN](https://github.com/skycoin/skywire/wiki/Skywire-VPN-Client)
- [Setup the Skywire VPN server](https://github.com/skycoin/skywire/wiki/Skywire-VPN-Server)
- [Package Installation Guide](https://github.com/skycoin/skywire/wiki/Skywire-Package-Installation)

an example using the vpn with `skywire-cli`

```
skywire-cli vpn list
```
this will query the service discovery for a list of vpn server public keys.
https://sd.skycoin.com/api/services?type=vpn

sample output:
```
02836f9a39e38120f338dbc98c96ee2b1ffd73420259d1fb134a2d0a15c8b66ceb | NL
0289a464f485ce9036f6267db10e5b6eaabd3972a25a7c2387f92b187d313aaf5e | GB
03cad59c029fc2394e564d0d328e35db17f79feee50c33980f3ab31869dc05217b | ID
02cf90f3b3001971cfb2b2df597200da525d359f4cf9828dca667ffe07f59f8225 | IT
03e540ddb3ac61385d6be64b38eeef806d8de9273d29d7eabb8daccaf4cee945ab | US
...
```

select a key and start the vpn with
```
skywire-cli vpn start <public-key>
```

view the status of the vpn
```
skywire-cli vpn status
```

Check your ip address with ip.skywire.dev
__note: ip.skycoin.com will only show your real ip address, not the ip address of the vpn connection__

stop the vpn
```
skywire-cli vpn stop
```

### Using the Skywire SOCKS5 client


The following wiki documentation exists on the SOCKS5 proxy
- [Skywire SOCKS5 Proxy User Guide](https://github.com/skycoin/skywire/wiki/Skywire-SOCKS5-Proxy-User-Guide)
- [SSH over SOCKS5 Proxy](https://github.com/skycoin/skywire/wiki/SSH-over-SOCKS5-Proxy)

The main difference between the vpn and the socks5 proxy is that the proxy is configured __per application__ while the vpn wraps the connections for the whole machine

The socks client usage (from `skywire-cli`) is similar to the vpn, though the `skywire-cli` subcommands and flags do not currently match from the one application to the other. This will be rectified.

To use the SOCKS5 proxy client via `skywire-cli`

first, select a public key with a running socks5 proxy server from the service discovery here:
[sd.skycoin.com/api/services?type=proxy](https://sd.skycoin.com/api/services?type=proxy)

you can also filter by country code in the query, for example:
[sd.skycoin.com/api/services?type=proxy&country=US](https://sd.skycoin.com/api/services?type=proxy&country=US)

start the socks5 client (starts on port 1080 by default)
```
skywire-cli skysocksc start --pk <public-key>
```

view the skysocks-client app status
```
skywire-cli skysocksc status
```

The connection may be consumed in a web browser via direct proxy configuration in browsers which support it, or using such extensions as `foxyproxy`.

The connection may also be consumed in the terminal by setting `ALL_PROXY` environmental variable, or via the specific method used by a certain application.

For example, to use `curl` via the socks5 proxy connection:
```
curl -Lx socks5h://127.0.0.1:1080 http://ip.skycoin.com/ | jq
```

examples of `ssh` over the socks5 proxy:

using `openbsd-netcat`
```
 ssh user@host -p 22 -o "ProxyCommand=nc -X 5 -x 127.0.0.1:1080 %h %p"
```

using `ncat` from `nmap`
```
ssh user@host -p 22 -o "ProxyCommand=ncat --proxy-type socks5 --proxy 127.0.0.1:1080 %h %p"
```

stop the socks5 proxy client
```
skywire-cli skysocksc stop
```

## Package Build Overview

A high-level overview of the process for building skywire from source and the paths and files which comprise the package-based installation is contained in the [PKGBUILD](https://github.com/skycoin/AUR/blob/main/skywire/PKGBUILD)

this and other build variants, including the debian packages, can be built into a package with a single command, using `yay` on archlinux

installing [skywire-bin](https://aur.archlinux.org/packages/skywire-bin) from the AUR will install the release binaries provided by the release section of this repo
```
yay -S skywire-bin
```

to build the debian packages using the release binaries
```
yay --mflags " -p cc.deb.PKGBUILD " -S skywire-bin
```

installing [skywire](https://aur.archlinux.org/packages/skywire) from the AUR will compile binaries using the source archive for the latest version release
```
yay -S skywire
```

build from git sources to the develop branch
```
yay --mflags " -p git.PKGBUILD " -S skywire
```

## Prepare - path setup

The standard procedure for building software with `go` uses the `GOPATH` which is conventionally `$HOME/go`

Software sources are cloned via `git` to a path such as `$HOME/go/src/github.com/skycoin/skywire`

Optionally, the source archive of a versioned release is downloaded from the [release section](https://github.com/skycoin/skywire/releases)

The binaries which are compiled may optionally be placed in the `GOBIN` and then `GOBIN` may be added to the `PATH` in order that any binaries placed in the `GOBIN` will then become available as commands available to execute in the shell or terminal.

**This setup is optional** but it is documented below, and other examples will refer to this

```
mkdir -p "${HOME}/go/src/github.com/skycoin/" "${HOME}"/go/bin "${HOME}"/go/apps || true
cd "${HOME}/go/src/src/github.com/skycoin/"
git clone https://github.com/skycoin/skywire
#optionally checkout any branch
git checkout develop
```

## Build

the code below is a rough approximation of `make install` which is used in the build function of the skywire packages

```
export GOPATH="${HOME}/go"
export GOBIN="${GOPATH}/bin"
export _GOAPPS="${GOPATH}/apps"
#optionally, use musl-gcc for static compilation
#export CC=musl-gcc

cd "${HOME}/go/src/github.com/skycoin/skywire"

#binary versioning
local _version=$(make version)
DMSG_BASE="github.com/skycoin/dmsg"
BUILDINFO_PATH="${DMSG_BASE}/buildinfo"
BUILDINFO_VERSION="${BUILDINFO_PATH}.version=${_version}"
BUILDINFO="${BUILDINFO_VERSION} ${BUILDINFO_DATE} ${BUILDINFO_COMMIT}"

#create the skywire binaries
_pkggopath=github.com/skycoin/skywire
cd "${HOME}"/go/src/${_pkggopath}
_cmddir="${HOME}"/go/src/${_pkggopath}/cmd
cd "${_cmddir}"/apps
_app="$(ls)"
for _i in ${_app}; do
echo "building ${_i} binary"
cd "${_cmddir}/apps/${_i}"
go build -trimpath --ldflags="" --ldflags "${BUILDINFO} -s -w -linkmode external -extldflags '-static' -buildid=" -o $_GOAPPS .
done
echo "building skywire-visor binary"
cd "${_cmddir}"/skywire-visor
go build -trimpath --ldflags="" --ldflags "${BUILDINFO} -s -w -linkmode external -extldflags '-static' -buildid=" -o $GOBIN .
echo "building skywire-cli binary"
cd "${_cmddir}"/skywire-cli
go build -trimpath --ldflags="" --ldflags "${BUILDINFO} -s -w -linkmode external -extldflags '-static' -buildid=" -o $GOBIN .
```

## Install

The installation paths for the skywire as present in the skywire linux packages are detailed below

Note that `make install-system-linux` will place the binaries into the system where the package would install them, and is best used for quickly testing changes to ensure they work with the autoconfig script included with the package

```
#to install directly into the system, use "/"
_pkgdir="/"

#use the appropriate systemd unit installation path for your linux distribution (i.e. etc/systemd/system for .deb distros)
_systemddir="usr/lib/systemd/system"

#the base path where skywire is installed
_dir="opt/skywire"

#application binaries ; started by the visor process
_apps="${_dir}/apps"

#main binaries go here
_bin="${_dir}/bin"

#scripts included
_scripts="${_dir}/scripts"

#create the directories
mkdir -p "${_pkgdir}/usr/bin"
mkdir -p "${_pkgdir}/${_dir}/bin"
mkdir -p "${_pkgdir}/${_dir}/apps"
mkdir -p "${_pkgdir}/${_dir}/scripts"
mkdir -p "${_pkgdir}/${_systemddir}"
#the local folder is otherwise produced by the visor on startup
mkdir -p "${_pkgdir}/${_dir}/local"

# instal binaries - assumes nothing else was already in your GOBIN
 install -Dm755 "${GOBIN}"/* "${_pkgdir}/${_bin}/"

#Symlink the binaries into the executable path
for _i in "${_pkgdir}/${_bin}"/* ; do
	ln -rTsf "${_i}" "${_pkgdir}/usr/bin/${_i##*/}"
done

#install the app binaries
install -Dm755 "${_GOAPPS}"/* "${_pkgdir}/${_skyapps}/"
#it is not necessary to symlink these binaries to the executable path but may be useful for debugging
for _i in "${_pkgdir}/${_apps}"/* ; do
	ln -rTsf "${_i}" "${_pkgdir}/usr/bin/${_i##*/}"
done

#install the dmsghttp-config.json'
install -Dm644 "${HOME}"/go/src/${_pkggopath}/skywire/dmsghttp-config.json" "${_pkgdir}/${_dir}/dmsghttp-config.json"
```

Desktop integration, maintenance scripts, and systemd service files are included in the skywire package from the [skywire AUR](https://aur.archlinux.org/cgit/aur.git/tree/?h=skywire-bin) which is managed as a subtree from [github.com/skycoin/aur](https://github.com/skycoin/AUR/tree/main/skywire-bin)

`${srcdir}` in the code below is in reference to the directory containing these other source files

```
#Install scripts
install -Dm755 "${srcdir}"/skywire-autoconfig "${_pkgdir}/${_skyscripts}/"
ln -rTsf "${_pkgdir}/${_skyscripts}/skywire-autoconfig" "${_pkgdir}"/usr/bin/skywire-autoconfig

#Installing systemd services'
install -Dm644 "${srcdir}"/*.service "${_pkgdir}/${_systemddir}/"

# instal desktop files and icons
mkdir -p "${_pkgdir}"/usr/share/applications/ "${_pkgdir}"/usr/share/icons/hicolor/48x48/apps/
install -Dm644 "${srcdir}"/*.desktop "${_pkgdir}"/usr/share/applications/
install -Dm644 "${srcdir}"/*.png "${_pkgdir}"/usr/share/icons/hicolor/48x48/apps/
```

## Skywire-autoconfig

[skywire-autoconfig](https://github.com/skycoin/AUR/blob/main/skywire/skywire-autoconfig) is a script is provided with the package which is executed as part of the postinstall process. It serves as a utility for automating:

* config management and updates
* setting remote hypervisors
* restarting the skywire process (via systemd)
* printing helpful text for the user to know what is happening
* generating configuration on boot (for skybian images / chroot installations of skywire)
* setting config defaults from environmental variables
* re-establishing skyfwd and skyrev connections (pending)

for more information regarding this script and the skywire package installation, refer to [this wiki article](https://github.com/skycoin/AUR/wiki/skywire-bin)

### Package tree
```
/
├── etc
│   └── skel
│       └── .config
│           └── systemd
│               └── user
│                   ├── skywire-autoconfig.service
│                   └── skywire.service
├── opt
│   └── skywire
│       ├── apps
│       │   ├── skychat
│       │   ├── skysocks
│       │   ├── skysocks-client
│       │   ├── vpn-client
│       │   └── vpn-server
│       ├── bin
│       │   ├── skywire-cli
│       │   └── skywire-visor
│       ├── dmsghttp-config.json
│       ├── local
│       │   └── custom
│       ├── scripts
│       │   └── skywire-autoconfig
│       └── skycoin.asc
└── usr
    ├── bin
    │   ├── skychat -> ../../opt/skywire/apps/skychat
    │   ├── skysocks -> ../../opt/skywire/apps/skysocks
    │   ├── skysocks-client -> ../../opt/skywire/apps/skysocks-client
    │   ├── skywire -> ../../opt/skywire/bin/skywire-visor
    │   ├── skywire-autoconfig -> ../../opt/skywire/scripts/skywire-autoconfig
    │   ├── skywire-cli -> ../../opt/skywire/bin/skywire-cli
    │   ├── skywire-visor -> ../../opt/skywire/bin/skywire-visor
    │   ├── vpn-client -> ../../opt/skywire/apps/vpn-client
    │   └── vpn-server -> ../../opt/skywire/apps/vpn-server
    ├── lib
    │   └── systemd
    │       └── system
    │           ├── skywire-autoconfig.service
    │           └── skywire.service
    └── share
        ├── applications
        │   ├── skywire.desktop
        │   └── skywirevpn.desktop
        └── icons
            └── hicolor
                └── 48x48
                    └── apps
                        ├── skywire.png
                        └── skywirevpn.png

24 directories, 27 files
```

## Creating a GitHub release

To maintain actual `skywire-visor` state on users' Skywire nodes we have a mechanism for updating `skywire-visor`
binaries. Binaries for each version are uploaded to [GitHub releases](https://github.com/skycoin/skywire/releases/). We
use [goreleaser](https://goreleaser.com) for creating them.

### How to create a GitHub release

1. Make sure that `git` and [goreleaser](https://goreleaser.com/install) are installed.
2. Checkout to a commit you would like to create a release against.
3. Run `go mod vendor` and `go mod tidy`.
4. Make sure that `git status` is in clean state. Commit all vendor changes and source code changes.
5. Uncomment `draft: true` in `.goreleaser.yml` if this is a test release.
6. Create a `git` tag with desired release version and release name: `git tag -a 0.1.0 -m "First release"`,
   where `0.1.0` is release version and `First release` is release name.
5. Push the created tag to the repository: `git push origin 0.1.0`, where `0.1.0` is release version.
6. [ ̶I̶s̶s̶u̶e̶ ̶a̶ ̶p̶e̶r̶s̶o̶n̶a̶l̶ ̶G̶i̶t̶H̶u̶b̶ ̶a̶c̶c̶e̶s̶s̶ ̶t̶o̶k̶e̶n̶.̶](https://github.com/settings/tokens)
7.  ̶R̶u̶n̶ ̶`̶G̶I̶T̶H̶U̶B̶_̶T̶O̶K̶E̶N̶=̶y̶o̶u̶r̶_̶t̶o̶k̶e̶n̶ ̶m̶a̶k̶e̶ ̶g̶i̶t̶h̶u̶b̶-̶r̶e̶l̶e̶a̶s̶e̶`̶
8. [Check the created GitHub release.](https://github.com/skycoin/skywire/releases/)
