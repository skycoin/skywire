# Auto-Update Mechanisms

Skywire can keep itself up to date in three independent ways. They are
**not** mutually exclusive layers of one system — they are three separate
mechanisms shipped by three separate packages, each appropriate to a
different deployment style. Pick the one that matches how you run skywire;
running more than one at once is redundant and can fight over the binary.

| Mechanism | Package | Unit(s) | Cadence | Updates by | Best for |
|-----------|---------|---------|---------|-----------|----------|
| **1. unattended-upgrades** | `skyrepo` | apt's `apt-daily-upgrade.timer` | apt periodic (≈daily) | reinstalling the `skywire-bin` **`.deb`** from the Skycoin apt repo | Debian/Armbian boards installed from the apt repo |
| **2. source auto-update** | `skywire-autoupdate` | `skywire-update.service` + `.timer` | daily (`+` up to 1 h jitter) | `go install github.com/skycoin/skywire@<commit>` → `/opt/skywire/bin/skywire` | nodes that build from source and track the rolling release |
| **3. docker auto-pull** | `skywire-docker-autopull` | `skywire-docker-autopull.service` + `.timer` | every 5 min | `docker pull` + `docker compose up -d --force-recreate` | container deployments |

All three ultimately track the same **rolling release**: CI advances the
`skywire-commit` branch to the latest commit on `develop` for which every
platform's tests passed, and warms the Go module proxy so that commit is
installable worldwide. See [Skywire Auto-Update (rolling release / CI)](https://github.com/skycoin/skywire/blob/develop/AUTO_UPDATE.md)
for how the `skywire-commit` branch and the `:test` Docker tag are produced.

---

## 1. unattended-upgrades (Debian / apt)

This is the lightest-touch option: it uses stock Debian
[`unattended-upgrades`](https://wiki.debian.org/UnattendedUpgrades) to
reinstall the `skywire-bin` package whenever a newer `.deb` is published to
the Skycoin apt repository. Nothing skywire-specific runs — apt does the work
on its normal daily schedule.

Everything here is shipped by the **`skyrepo`** package, which registers the
apt repository and drops in the opt-in configuration.

### What `skyrepo` installs

`skyrepo` writes the apt source list to `/etc/apt/sources.list.d/skycoin.list`
and installs the signing key. The repository is mirrored at three hostnames
(any one works):

```
deb http://deb.skywire.skycoin.com   sid main
deb http://deb.theskywirenetwork.net sid main
deb http://deb.skywire.dev           sid main
```

It also ships **example** opt-in files under
`/usr/share/doc/skyrepo/examples/` (they are *not* copied into
`/etc/apt/apt.conf.d/` automatically — you opt in):

`52unattended-upgrades-skycoin` — extends unattended-upgrades to cover the
Skycoin origin:

```
// Extend unattended-upgrades to cover the Skycoin apt repository.
// Merges with Allowed-Origins from /etc/apt/apt.conf.d/50unattended-upgrades.
// Origin metadata (per `apt-cache policy`):
//   o=skycoin, l=skycoin, n=sid, c=main
Unattended-Upgrade::Origins-Pattern {
    "origin=skycoin,codename=sid";
};
```

`99skycoin-periodic` — turns apt's periodic timers on (needed on Armbian,
whose `02-armbian-periodic` disables them by default):

```
APT::Periodic::Enable "1";
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::Download-Upgradeable-Packages "1";
```

`skycoin-refresh-before-upgrade.conf` — a systemd drop-in that forces an
`apt-get update` immediately before the daily upgrade so a freshly-published
`.deb` isn't missed:

```
[Service]
ExecStartPre=-/usr/bin/apt-get update
```

The `skyrepo` post-install script activates that drop-in (copying it under
`/etc/systemd/system/apt-daily-upgrade.service.d/` and running
`systemctl daemon-reload`) **only if** you have opted in by placing
`52unattended-upgrades-skycoin` in `/etc/apt/apt.conf.d/`.

### Enable it

```bash
sudo apt install skyrepo unattended-upgrades
sudo cp /usr/share/doc/skyrepo/examples/52unattended-upgrades-skycoin /etc/apt/apt.conf.d/
sudo cp /usr/share/doc/skyrepo/examples/99skycoin-periodic          /etc/apt/apt.conf.d/
sudo systemctl restart apt-daily-upgrade.timer   # re-reads the drop-in
```

Verify the origin is covered:

```bash
apt-cache policy skywire-bin
sudo unattended-upgrade --dry-run --debug 2>&1 | grep -i skycoin
```

!!! note "First-boot installer, not the updater"
    `skyrepo` also ships `install-skywire.service` (a one-shot that runs
    `apt reinstall skywire-bin` on first boot, then disables itself). That is a
    provisioning helper — it is separate from the recurring unattended-upgrades
    path described here.

---

## 2. Source auto-update — `skywire-autoupdate`

The **`skywire-autoupdate`** package (depends on `skywire-bin` and `go`)
rebuilds skywire from source on a daily timer, following the rolling release.
It installs `/usr/bin/skywire-update` plus a service and timer.

`skywire-update.service`:

```ini
[Unit]
Description=Skywire auto-updater
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/bin/skywire-update
```

`skywire-update.timer`:

```ini
[Timer]
OnCalendar=daily
RandomizedDelaySec=3600
Persistent=true
```

### What `/usr/bin/skywire-update` does

1. Sources `/etc/skywire.conf` for its settings (`SKYENV`).
2. Resolves the target commit from `UPDATE_CHANNEL` (default `stable` = the
   latest CI-tested commit, resolved with
   `go run github.com/skycoin/skywire/cmd/skywire-commit@skywire-commit`
   using `GOPROXY=direct`; also accepts `develop`, `latest`, or an explicit
   `<commit-hash>`).
3. Compares that target to the running binary (`skywire -b`) and exits early
   if nothing changed.
4. Builds as the unprivileged `skywire-build` user (home
   `/var/lib/skywire-build`) with `go install github.com/skycoin/skywire@<target>`
   (`GOFLAGS=-trimpath`), installs the result to `/opt/skywire/bin/skywire`.
5. Runs `skywire autoconfig`, then `systemctl restart skywire` (plus anything
   in `RESTART_SERVICES`) if the service is active.
6. Prunes the Go build/module caches on exit.

Tunable knobs in `/etc/skywire.conf`: `UPDATE_CHANNEL`, `GOPROXY_MODE`,
`MODCACHE_CAP_MB`, `RESTART_SERVICES`.

### Enable it

The package's install hook already creates the `skywire-build` user (adding it
to the `docker` group if present) and runs
`systemctl enable --now skywire-update.timer`. To manage it:

```bash
systemctl status  skywire-update.timer     # is it scheduled?
systemctl start   skywire-update.service    # force an update now
journalctl -u     skywire-update.service -f # watch a run
systemctl disable --now skywire-update.timer # stop auto-updating
```

!!! info "Companion docker updater in the same package"
    `skywire-autoupdate` **also** ships `skywire-docker-update.service` /
    `.timer` (every 30 min), which resolves the CI-tested commit, does
    `docker pull skycoin/skywire:<sha>`, retags it `:test`, and recreates the
    compose stack. It is **not enabled by default**. Do not confuse it with the
    separately-packaged `skywire-docker-autopull` below — they are two
    different docker auto-updaters.

---

## 3. Docker auto-pull — `skywire-docker-autopull`

For container deployments, the **`skywire-docker-autopull`** package (its own
package — depends on `docker` and `docker-compose`) watches the published
image and recreates the stack when it changes. The source unit
`docker-autopull.service` is installed **renamed** as
`skywire-docker-autopull.service`:

```ini
[Unit]
Description=Skywire Docker image auto-pull
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/bin/skywire-docker-autopull
StandardOutput=journal
StandardError=journal
```

`skywire-docker-autopull.timer` runs every five minutes:

```ini
[Timer]
OnCalendar=*:0/5
Persistent=true
```

### What `/usr/bin/skywire-docker-autopull` does

1. `IMAGE="${DOCKER_IMAGE:-skycoin/skywire:test}"`; compose stack lives in
   `DEPLOY_DIR` / `COMPOSE_DIR`.
2. `docker pull "$IMAGE"` and compare the image ID to what is running — exit if
   unchanged.
3. Smoke-test the new image (`docker run --rm "$IMAGE" --help`), then
   `docker compose up -d --force-recreate` and `docker image prune -f`.

### Enable it

The package's post-install runs
`systemctl enable --now skywire-docker-autopull.timer`. To manage it:

```bash
systemctl status skywire-docker-autopull.timer
systemctl start  skywire-docker-autopull.service    # force a pull now
journalctl -u    skywire-docker-autopull.service -f
systemctl disable --now skywire-docker-autopull.timer
```

---

## Which one am I running?

```bash
# apt-based (mechanism 1)
systemctl status apt-daily-upgrade.timer
ls /etc/apt/apt.conf.d/ | grep -i skycoin

# source auto-update (mechanism 2)
systemctl status skywire-update.timer

# docker auto-pull (mechanism 3)
systemctl status skywire-docker-autopull.timer
```

If more than one timer is enabled and active, disable all but the one that
matches your install method — otherwise an apt reinstall, a source rebuild,
and a container recreate can each clobber the others' binary.
