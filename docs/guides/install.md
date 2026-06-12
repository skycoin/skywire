# Installing Skywire

Skywire can be installed by package, downloaded as a release binary, or
compiled from source via `go install` / `go run`.

## `go install` or `go run` Skywire (go1.26+)

Skywire commands can be executed via `go run`:

```
$ go run github.com/skycoin/skywire@develop
┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
└─┐├┴┐└┬┘││││├┬┘├┤
└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘
v1.3.67
built with go1.26.3

Available Commands:
  visor     Skywire Visor
  cli       Command Line Interface for skywire
  svc       Skywire services
  dmsg      DMSG services & utilities
  app       skywire native applications

Flags:
  -b, --bv        print runtime/debug.BuildInfo.Main.Version
  -d, --info      print runtime/debug.BuildInfo
  -v, --version   version for skywire
```

The skywire visor can now (as of v1.3.32) run directly with `go run` when using the default in-process visor native applications configuration

```
go run github.com/skycoin/skywire@develop cli config gen -br #print the config & output to skywire-config.json
go run github.com/skycoin/skywire@develop visor #uses skywire-config.json by default
```

By default the bundled apps (vpn, skysocks, skychat, …) run **in-process** —
there are no separate app binaries to install or manage, and `config gen`
produces this config automatically. For most users nothing else is needed.

> Want the visor to launch apps as **separate executables** instead — e.g. to
> run a custom app? See
> [Running apps as external binaries](#running-apps-as-external-binaries) at the
> end of this page.

## Installing Skywire from Release

Releases for Windows & macOS are available from the [release section](https://github.com/skycoin/skywire/releases/).

Install as a package on Debian or Arch Linux: [Package Installation Guide](https://github.com/skycoin/skywire/wiki/Skywire-Package-Installation).

[Binary Releases](https://github.com/skycoin/skywire/releases) for many platforms and architectures are provided if none of the other installation methods are preferred.

## Linux Packages

All Linux packages provide a virtually identical installation, helper scripts, and systemd services regardless of the linux distro.

Consider the [skywire PKGBUILD](https://github.com/skycoin/AUR/blob/main/skywire/PKGBUILD) as a reference for building and installing skywire on any linux distribution.

### Debian packages

Debian packages are maintained for skywire, as well as several build variants for archlinux.

It's recommended to install the debian packages from the apt repo - see the instructions here:

https://deb.skywire.skycoin.com/

### Arch Linux AUR packages

Installing [skywire-bin](https://aur.archlinux.org/packages/skywire-bin) from the AUR will install the release binaries provided by the release section of this repository:
```
yay -S skywire-bin
```

**To build the debian packages using the release binaries:**
```
yay --mflags " -p cc.deb.PKGBUILD " -S skywire-bin
```

Installing [skywire](https://aur.archlinux.org/packages/skywire) from the AUR will compile binaries using the source archive for the latest version release:
```
yay -S skywire
```

Build the skywire Arch Linux package from git sources to the latest commits on the develop branch:
```
yay --mflags " -p git.PKGBUILD " -S skywire
```

### NixOS / Nix flake

Two derivations under [`/nix/`](../extras/nix.md) — same flavors as the AUR
packages: `skywire` (source build, static-musl, mirrors
`make build-static`) and `skywire-bin` (the upstream release
tarball).

From a checkout:
```
cd nix
nix build .#skywire        # source, static musl
nix build .#skywire-bin    # prebuilt tarball
nix run   .#skywire -- --bv
```

Or as a flake input from elsewhere:
```nix
inputs.skywire.url = "github:skycoin/skywire?dir=nix";
# ...
environment.systemPackages = [ skywire.packages.${system}.skywire ];
```

See the [nix details page](../extras/nix.md) for the per-arch hash-fill
flow on `skywire-bin`, the visor's `--apps-dir` integration, and
the static-binary sanity check.

## Running apps as external binaries

*Advanced — most users should use the default in-process apps described above.*

By default each app entry in `skywire-config.json` just names the app and its
args, and the visor runs it inside its own process:

```json
{ "name": "skysocks", "auto_start": true, "port": 3 }
```

To launch an app as a **separate executable** instead, the entry adds a
`"binary"` field and prepends `app <app-name>` to its args:

```json
{ "name": "skysocks", "binary": "skywire", "args": ["app", "skysocks"], "auto_start": true, "port": 3 }
```

This requires the app binaries to be reachable. Install skywire:

```
go install github.com/skycoin/skywire@develop
```

then either run the visor from the directory containing the binary, or set
`"bin_path"` in `skywire-config.json` to the directory holding the app
binaries. A custom app must have its binary present in `bin_path`.

## Docker

For docker-specific documentation, see the [Docker guide](../extras/docker.md).
