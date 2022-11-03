[![Build Status](https://travis-ci.com/skycoin/skywire.svg?branch=master)](https://travis-ci.com/skycoin/skywire)

Skywire requires a Golang version of `1.16` or higher.

# Skywire

- [Skywire](#skywire)
- [Commands and Subcommands](#commands-and-subcommands)
- [Build Overview](#build-overview)
- [Dependencies](#dependencies)
- [Runtime Deps](#runtime-deps)
- [Build Deps](#build-deps)

	- [Prepare](#prepare)
	- [Build](#build)
    - [Configure Skywire](#configure-skywire)
        - [Expose hypervisorUI](#expose-hypervisorui)
        - [Add remote hypervisor](#add-remote-hypervisor)
    - [Run `skywire-visor`](#run-skywire-visor)
        - [Using the Skywire VPN](#using-the-skywire-vpn)
    - [Creating a GitHub release](#creating-a-github-release)
        - [How to create a GitHub release](#how-to-create-a-github-release)

## Commands and Subcommands

* [skywire-cli](cmd/skywire-cli/README.md)
* [skywire-visor](cmd/skywire-visor/README.md)

## App documentation

apps are not executed by the user, but hosted by the visor process

* [skychat](cmd/apps/skychat/README.md)
* [skysocks](cmd/apps/skysocks/README.md)
* [skysocks-client](cmd/apps/skysocks-client/README.md)
* [vpn-client](cmd/apps/vpn-client/README.md)
* [vpn-server](cmd/apps/vpn-server/README.md)

further documentation can be found in the [skywire wiki](https://github.com/skycoin/skywire/wiki)

## Installing Skywire

* [Windows installer](https://github.com/skycoin/skywire/releases/download/v1.2.1/skywire-installer-v1.2.1-windows-amd64.msi)
* [MacOS amd64 package](https://github.com/skycoin/skywire/releases/download/v1.2.1/skywire-installer-v1.2.1-darwin-amd64.pkg)
* [MacOS m1 / arm64](https://github.com/skycoin/skywire/releases/download/v1.2.1/skywire-installer-v1.2.1-darwin-arm64.pkg)
* [Debian Package Installation](github.com/skycoin/skywire/wiki/Skywire-Package-Installation)
* [Binary Releases](https://github.com/skycoin/skywire/releases)

## Build Overview

A high-level overview of the process for building skywire from source and the paths and files which comprise the package-based installation is contained in the [PKGBUILD](https://github.com/skycoin/AUR/blob/main/skywire/PKGBUILD)

this and other build variants can be built into a package with a single command, using `yay` on archlinux

installing [skywire-bin](https://aur.archlinux.org/packages/skywire-bin) will install the release binaries provided by the release section of this repo

```
yay -S skywire-bin
```

to build the debian packages using the release binaries

```
yay --mflags " -p cc.deb.PKGBUILD " -S skywire-bin
```

installing [skywire](https://aur.archlinux.org/packages/skywire) will compile binaries using the source archive for the latest version release

```
yay -S skywire
```

build from git sources to the develop branch

```
yay --mflags " -p git.PKGBUILD " -S skywire
```

## Dependencies

The systray app requires other build and runtime dependencies and further steps to compile. These are listed in [/docs/systray-builds.md](/docs/systray-builds.md)

### Runtime Deps

* ` glibc` or `libc6` _unless statically compiled_

### Build Deps

* `golang`
* `git` (optional)
* `musl` and `kernel-headers-musl` or equivalent - _for static compilation_

For more information on static compilation, see [docs/static-builds.md](docs/static-builds.md).

## Prepare

The standard procedure for building software with `go` uses the `GOPATH` which is conventionally `$HOME/go`

Software sources are cloned via `git` to a path such as `$HOME/go/src/github.com/skycoin/skywire`

Optionally, the source archive of a versioned release is downloaded from the [release section](https://github.com/skycoin/skywire/releases)

The binaries which are compiled may optionally be placed in the `GOBIN` and then `GOBIN` may be added to the `PATH` in order that any binaries placed in the `GOBIN` will then appear as commands available to execute in the shell or terminal.

basic setup of `go` is further described [here](https://github.com/skycoin/skycoin/blob/develop/INSTALLATION.md#setup-your-gopath)

Such a setup is optional, but it is documented below, and other examples will refer to this

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
export GOOS=linux
#use musl-gcc for static compilation
export CC=musl-gcc

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

The installation paths for the skywire as present in the skywire package are detailed below

Note that `make install-system-linux` will place the binaries into the system where the package would install them, and is best used for quickly testing changes to ensure they work with the autoconfig script included with the package

```
#to install directly into the system, use "/"
_pkgdir="/"

#use the appropriate systemd unit installation path for your linux distribution
_systemddir="usr/lib/systemd/system"

#the base path where skywire is installed
_skydir="opt/skywire"

#application binaries ; started by the visor process
_skyapps="${_skydir}/apps"

#main binaries go here
_skybin="${_skydir}/bin"

#scripts included
_skyscripts="${_skydir}/scripts"

#create the directories
mkdir -p "${_pkgdir}/usr/bin"
mkdir -p "${_pkgdir}/${_skydir}/bin"
mkdir -p "${_pkgdir}/${_skydir}/apps"
mkdir -p "${_pkgdir}/${_skydir}/scripts"
mkdir -p "${_pkgdir}/${_systemddir}"
#the local folder is otherwise produced by the visor on startup
mkdir -p "${_pkgdir}/${_skydir}/local"

# instal binaries - assumes nothing else was already in your GOBIN
 install -Dm755 "${GOBIN}"/* "${_pkgdir}/${_skybin}/"

#Symlink the binaries into the executable path
for _i in "${_pkgdir}/${_skybin}"/* ; do
	ln -rTsf "${_i}" "${_pkgdir}/usr/bin/${_i##*/}"
done

#install the app binaries
install -Dm755 "${_GOAPPS}"/* "${_pkgdir}/${_skyapps}/"
#it is not necessary to symlink these binaries to the executable path but may be useful for debugging
for _i in "${_pkgdir}/${_skyapps}"/* ; do
	ln -rTsf "${_i}" "${_pkgdir}/usr/bin/${_i##*/}"
done

#install the dmsghttp-config.json'
install -Dm644 "${HOME}"/go/src/${_pkggopath}/skywire/dmsghttp-config.json" "${_pkgdir}/${_skydir}/dmsghttp-config.json"
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
* detecting environmental variables

for more information regarding this script and the skywire package installation, refer to [this wiki article](https://github.com/skycoin/AUR/wiki/skywire-bin)

### Package tree
```
/
├── opt
│   └── skywire
│       ├── apps
│       │   ├── skychat
│       │   ├── skysocks
│       │   ├── skysocks-client
│       │   ├── vpn-client
│       │   └── vpn-server
│       ├── bin
│       │   ├── skywire-cli
│       │   └── skywire-visor
│       ├── dmsghttp-config.json
│       ├── local
│       └── scripts
│           └── skywire-autoconfig
└── usr
    ├── bin
    │   ├── skychat -> ../../opt/skywire/apps/skychat
    │   ├── skysocks -> ../../opt/skywire/apps/skysocks
    │   ├── skysocks-client -> ../../opt/skywire/apps/skysocks-client
    │   ├── skywire -> ../../opt/skywire/bin/skywire-visor
    │   ├── skywire-autoconfig -> ../../opt/skywire/scripts/skywire-autoconfig
    │   ├── skywire-cli -> ../../opt/skywire/bin/skywire-cli
    │   ├── skywire-visor -> ../../opt/skywire/bin/skywire-visor
    │   ├── vpn-client -> ../../opt/skywire/apps/vpn-client
    │   └── vpn-server -> ../../opt/skywire/apps/vpn-server
    ├── lib
    │   └── systemd
    │       └── system
    │           ├── skywire-autoconfig.service
    │           ├── skywire.service
    └── share
        ├── applications
        │   ├── skywire.desktop
        │   └── skywirevpn.desktop
        └── icons
            └── hicolor
                └── 48x48
                    └── apps
                        ├── skywire.png
                        └── skywirevpn.png

17 directories, 24 files
```

## Build with make

build the binaries and apps with `go build`
```
make build
```

output tree

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

install these executables to the `GOPATH`
```
make install
```


## Build docker image
```
$ ./ci_scripts/docker-push.sh -t $(git rev-parse --abbrev-ref HEAD) -b
```


## Run from source

Running from source as outlined here does not write the config to disk or explicitly compile any binaries. The config is piped from skywire-cli stdout to the visor stdin, and all are executed via `go run`.

```
 mkdir -p $HOME/go/src/github.com/skycoin && cd $HOME/go/src/github.com/skycoin
 git clone https://github.com/skycoin/skywire.git
 cd skywire
 make run-source
```

## Files and folders created by skywire

```
├──skywire-config.json
└─┬local
  ├──skychat
  ├──skysocks
  ├──apps-pid.txt
  ├──skychat_log.db
  ├──reward.txt
  ├──node-info.json
  └─┬transport_logs
    └──2022-11-12.csv
```

Some of these files are served via the [dmsghttp logserver](https://github.com/skycoin/skywire/wiki/DMSGHTTP-logserver)

## Configure Skywire

The skywire visor requires a config to run. This config is produced by `skywire-cli config gen`

The skywire-autoconfig script included with the skywire package handles config generation and updates for the user

Examples of config generation and command / flag documentation can be found in the [cmd/skywire-cli/README.md](cmd/skywire-cli/README.md) and [cmd/skywire-visor/README.md](cmd/skywire-visor/README.md)

The most important flags are noted below

### Expose hypervisorUI

In order to expose the hypervisor UI, generate a config file with `--is-hypervisor` or `-i` flag:

```
 skywire-cli config gen -i
```

Docker container will create config automatically for you, should you want to run it manually, you can do:

```
 docker run --rm -v <YOUR_CONFIG_DIR>:/opt/skywire \
  skycoin/skywire:test skywire-cli config gen -i
```

Docker container will create config automatically for you, should you want to run it manually, you can do:

```
$ docker run --rm -v <YOUR_CONFIG_DIR>:/opt/skywire \
  skycoin/skywire:latest skywire-cli visor gen-config --is-hypervisor
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

Or from docker image:

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
in [the docs](docs/windows-setup.md).

### Using the Skywire VPN

If you are interested in running the Skywire VPN as either a client or a server, please refer to the following guides:

- [Setup the Skywire VPN](https://github.com/skycoin/skywire/wiki/Skywire-VPN-Client)
- [Setup the Skywire VPN server](https://github.com/skycoin/skywire/wiki/Skywire-VPN-Server)
- [Package Installation Guide](https://github.com/skycoin/skywire/wiki/Skywire-Package-Installation)

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
6. [Issue a personal GitHub access token.](https://github.com/settings/tokens)
7. Run `GITHUB_TOKEN=your_token make github-release`
8. [Check the created GitHub release.](https://github.com/skycoin/skywire/releases/)
