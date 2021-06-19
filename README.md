[![Build Status](https://travis-ci.com/skycoin/skywire.svg?branch=master)](https://travis-ci.com/skycoin/skywire)

# Skywire

- [Skywire](#skywire)
    - [Build](#build)
    - [Configure Skywire](#configure-skywire)
        - [Expose hypervisorUI](#expose-hypervisorui)
        - [Add remote hypervisor](#add-remote-hypervisor)
    - [Run `skywire-visor`](#run-skywire-visor)
        - [Using the Skywire VPN](#using-the-skywire-vpn)
    - [Creating a GitHub release](#creating-a-github-release)
        - [How to create a GitHub release](#how-to-create-a-github-release)

## Build

Skywire requires a Golang version of `1.16` or higher.

```bash
# Clone.
$ git clone https://github.com/skycoin/skywire.git
$ cd skywire

# Build and Install
$ make build; make install

# OR build docker image
$ ./ci_scripts/docker-push.sh -t $(git rev-parse --abbrev-ref HEAD) -b
```

Skywire can be statically built. For instructions check [the docs](docs/static-builds.md).

## Configure Skywire

### Expose hypervisorUI

In order to expose the hypervisor UI, generate a config file with `--is-hypervisor` flag:

```bash
$ skywire-cli visor gen-config --is-hypervisor
```

Docker container will create config automatically for you, should you want to run it manually, you can do:

```bash
$ docker run --rm -v <YOUR_CONFIG_DIR>:/opt/skywire \
  skycoin/skywire:test skywire-cli gen-config --is-hypervisor
```

After starting up the visor, the UI will be exposed by default on `localhost:8000`.

### Add remote hypervisor

Every visor can be controlled by one or more hypervisors. To allow a hypervisor to access a visor, the PubKey of the
hypervisor needs to be specified in the configuration file. You can add a remote hypervisor to the config with:

```bash
$ skywire-cli visor update-config --hypervisor-pks <public-key>
```

Or from docker image:

```bash
$ docker run --rm -v <YOUR_CONFIG_DIR>:/opt/skywire \
  skycoin/skywire:test skywire-cli update-config hypervisor-pks <public-key>
```

## Run `skywire-visor`

`skywire-visor` hosts apps and is an applications gateway to the Skywire network.

`skywire-visor` requires a valid configuration to be provided. If you want to run a VPN client locally, run the visor
as `sudo`.

```bash
$ sudo skywire-visor -c skywire-config.json
```

Or from docker image:

```bash
# with custom config mounted on docker volume
$ docker run --rm -p 8000:8000 -v <YOUR_CONFIG_DIR>:/opt/skywire --name=skywire skycoin/skywire:test skywire-visor -c /opt/skywire/<YOUR_CONFIG_NAME>.json
# without custom config (config is automatically generated)
$ docker run --rm -p 8000:8000 --name=skywire skycoin/skywire:test skywire-visor
```

`skywire-visor` can be run on Windows. The setup requires additional setup steps that are specified
in [the docs](docs/windows-setup.md).

### Using the Skywire VPN

If you are interested in running the Skywire VPN as either a client or a server, please refer to the following guides:

- [Setup the Skywire VPN](https://github.com/skycoin/skywire/wiki/Setting-up-Skywire-VPN)
- [Setup the Skywire VPN server](https://github.com/skycoin/skywire/wiki/Setting-up-Skywire-VPN-server)

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
