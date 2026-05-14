# Installing Skywire

Skywire can be installed by package, downloaded as a release binary, or
compiled from source via `go install` / `go run`.

## `go install` or `go run` Skywire (go1.25+)

Skywire commands can be executed via `go run`:

```
$ go run github.com/skycoin/skywire@develop
┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
└─┐├┴┐└┬┘││││├┬┘├┤
└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘
v1.3.50
built with go1.25.6

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

The new config omits the `"binary"` field and the first two arguments `app <app-name>` :

```
[
  {
    "name": "vpn-client",
    "args": [
      "--dns",
      "1.1.1.1"
    ],
    "auto_start": false,
    "port": 43
  },
  {
    "name": "skychat",
    "args": [
      "--addr",
      ":8001"
    ],
    "auto_start": true,
    "port": 1
  },
  {
    "name": "skysocks",
    "auto_start": true,
    "port": 3
  },
  {
    "name": "skysocks-client",
    "args": [
      "--addr",
      ":1080"
    ],
    "auto_start": false,
    "port": 13
  },
  {
    "name": "vpn-server",
    "auto_start": false,
    "port": 44
  }
]
```

For comparison, the external apps launcher config:

```
[
  {
    "name": "vpn-client",
    "binary": "skywire",
    "args": [
      "app",
      "vpn-client",
      "--dns",
      "1.1.1.1"
    ],
    "auto_start": false,
    "port": 43
  },
  {
    "name": "skychat",
    "binary": "skywire",
    "args": [
      "app",
      "skychat",
      "--addr",
      ":8001"
    ],
    "auto_start": true,
    "port": 1
  },
  {
    "name": "skysocks",
    "binary": "skywire",
    "args": [
      "app",
      "skysocks"
    ],
    "auto_start": true,
    "port": 3
  },
  {
    "name": "skysocks-client",
    "binary": "skywire",
    "args": [
      "app",
      "skysocks-client",
      "--addr",
      ":1080"
    ],
    "auto_start": false,
    "port": 13
  },
  {
    "name": "vpn-server",
    "binary": "skywire",
    "args": [
      "app",
      "vpn-server"
    ],
    "auto_start": false,
    "port": 44
  }
]
```

To run the visor native applications with _external_ apps configuration, one must first have the skywire binary explicitly installed:
```
go install github.com/skycoin/skywire@develop
```
Then, either:
* place the binary in the current working directory where skywire is running
OR
* set the`bin_path` in the skywire-config.json to the directory containing the skywire binary

Running a custom skywire visor app requires that the binary for that app exists in the directory specified by `"bin_path"` in the visor's config

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

Two derivations under [`/nix/`](../../nix/) — same flavors as the AUR
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

See [`nix/README.md`](../../nix/README.md) for the per-arch hash-fill
flow on `skywire-bin`, the visor's `--apps-dir` integration, and
the static-binary sanity check.

## Docker

For docker-specific documentation, see [DOCKER.md](../../DOCKER.md).
