# AUR Packages (Arch Linux)

Skywire is packaged for Arch Linux through the AUR. There are **two AUR
pkgbases** — `skywire-bin` (prebuilt binary) and `skywire` (from source) —
plus a repository/provisioning package, `skyrepo`. Each pkgbase's git tree
also carries *variant* `*.PKGBUILD` recipes that produce the auto-update
packages and the Debian `.deb`s published to the apt repo, so the AUR trees
are the single source of packaging truth for both distros.

All are maintained by Moses Narrow. Note that every recipe builds/installs
from the upstream module path `github.com/skycoin/skywire` regardless of which
fork the working tree is checked out from.

## Quick pick

| You want… | Install |
|-----------|---------|
| The stable release, fastest install (no compiler) | `skywire-bin` |
| A locally-compiled build (musl-static, release tag) | `skywire` |
| A build tracking the `develop` branch | `skywire` via `git.PKGBUILD` |
| Automatic daily source rebuilds | `skywire-autoupdate` (see [Auto-Update](auto-update.md)) |
| The Skycoin apt repo + provisioning helpers | `skyrepo` |

---

## pkgbase `skywire-bin` — prebuilt binary

Installs the **prebuilt release binary** downloaded from the GitHub release
(`releases/download/v<ver>/skywire-v<ver>-linux-<arch>.tar.xz`, per-arch
`source_*`). No Go toolchain needed. `provides`/`conflicts` = `skywire`, so it
is interchangeable with the from-source package. Binary lands at
`/opt/skywire/bin/skywire` with `/usr/bin` symlinks. optdepends: redis,
postgresql, jq.

It installs only the **runtime** systemd units — `skywire.service`,
`skywire-autoconfig.service`, the deployment-service group
(`skywire-{sn,ar,rf,tpd,dmsgd,dmsg,sd}.service`), `dmsgpty-tcp.socket` +
`dmsgpty-tcp@.service`, a user unit, plus `sysusers.d`/`tmpfiles.d` entries. It
does **not** install any auto-update units — those come from the separate
`skywire-autoupdate` / `skywire-docker-autopull` packages.

Variant recipes in the same tree:

- **`PKGBUILD`** → `skywire-bin` (the above).
- **`cc.deb.PKGBUILD`** → repackages the release tarballs into per-arch `.deb`s
  (amd64/arm64/armhf/armel/riscv64/i386) for the apt repo. Shared deb scripts
  do sysusers/tmpfiles setup, `setcap`, generate `/etc/skywire.conf`, run
  `skywire autoconfig`, and try-restart the service.
- **`autoupdate.PKGBUILD`** / **`autoupdate.deb.PKGBUILD`** →
  the `skywire-autoupdate` package.
- **`docker-autopull.PKGBUILD`** / **`docker-autopull.deb.PKGBUILD`** →
  the `skywire-docker-autopull` package.

---

## pkgbase `skywire` — from source

Builds skywire **from source** with a musl static link:
`go install github.com/skycoin/skywire/cmd/skywire@v<ver>` with `CC=musl-gcc`
and `-linkmode external -extldflags '-static'`.
`makedepends=(git go>=1.24 musl kernel-headers-musl)` (plus `npm` when
`REBUILDUI=1`). It pulls the service/desktop/icon files from a nested
`skywire-bin` AUR checkout, so it installs the **same runtime units** as
`skywire-bin`. It also installs the `skywire-autoconfig` script to
`/opt/skywire/scripts` and `/usr/bin`.

Variant recipes in the same tree:

- **`PKGBUILD`** → `skywire`, from the release tag.
- **`git.PKGBUILD`** → develop-branch build; `pkgver()` derived from
  `go list -m github.com/skycoin/skywire@develop`, builds `cmd/skywire@develop`.
- **`deb.PKGBUILD`** → from-source Debian `.deb`.
- **`dev.PKGBUILD`** → develop-branch `.deb` (sources `deb.PKGBUILD` +
  `git.PKGBUILD`).
- **`systray-git.PKGBUILD`** → the systray build variant.

---

## Auto-update packages (from the `skywire-bin` tree)

These are produced by variant recipes above but are worth calling out as
distinct installable packages. See [Auto-Update Mechanisms](auto-update.md)
for what their timers do.

- **`skywire-autoupdate`** — `depends=(skywire-bin go)`. Installs
  `/usr/bin/skywire-update` + `/usr/bin/skywire-docker-update` and their
  service/timer pairs. Its `.install` creates the unprivileged
  `skywire-build` user and enables `skywire-update.timer`.
- **`skywire-docker-autopull`** — its **own** package, `depends=(docker
  docker-compose)`. Installs `skywire-docker-autopull.service` + `.timer`
  (every 5 min) that pulls `skycoin/skywire:test` and recreates the compose
  stack.

---

## pkgbase `skyrepo` — apt repo + provisioning

`arch=('any')`. Registers the Skycoin **apt repository** source list and GPG
key (`48F19E5157BE6014D80A47328D6D51BC4AD7AE64`), ships the opt-in
[unattended-upgrades example files](auto-update.md#1-unattended-upgrades-debian-apt),
and a first-boot `install-skywire.service` (one-shot `apt reinstall
skywire-bin`). It also absorbs the former skybian provisioning payload
(`skymanager`, `skybian-reset`, `skyenv`, MOTD snippets) and therefore
`Replaces`/`Conflicts`/`Provides` `skybian`.

Despite a lockstep-versioning comment in the PKGBUILD, the three pkgbases are
versioned independently in practice (e.g. `skywire-bin` and the from-source
`skywire` track different points, and `skyrepo` its own).

---

## Relationship to the Debian apt repo

The apt repo build scripts (in the `apt-repo` project) drive the
`*.deb.PKGBUILD` variants above with `makepkg` and publish the resulting
`.deb`s via `reprepro` — one publish script per package
(`updskywirebin.sh`, `updskywire.sh`, `updskywireautoupdate.sh`,
`updskywiredockerautopull.sh`). So the AUR PKGBUILD trees are also the
upstream of the Debian packages; a packaging change made once in the AUR tree
flows to both Arch and Debian users.
