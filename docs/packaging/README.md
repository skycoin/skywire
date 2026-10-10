# Packaging & Updates

How Skywire is packaged for Linux distributions, and how installed nodes keep
themselves up to date.

- [Auto-Update Mechanisms](auto-update.md) — the three independent ways a
  node updates itself: apt `unattended-upgrades` (via `skyrepo`), source
  rebuilds (`skywire-autoupdate` / `skywire-update.service`), and docker image
  auto-pull (`skywire-docker-autopull`). Includes how to tell which one you're
  running and how to enable/disable each.
- [AUR Packages](aur-packages.md) — the Arch Linux packages (`skywire-bin`,
  `skywire`, `skyrepo`) and the variant PKGBUILDs that also produce the
  auto-update packages and the Debian `.deb`s.

## Configuring a node interactively

`skywire autoconfig i` opens a form over `/etc/skywire.conf` (or `$SKYENV`)
with every autoconfig setting, its current value and its help text. Changed
fields are marked. Saving runs the same `skywire autoconfig --flag=value`
command you would type, so the file, config generation and service restart
behave exactly as they do from the command line. "Print command" shows that
command instead of running it.

`skywire autoconfig i --port 8080` serves the same form on a local web page.
It binds `127.0.0.1` unless `--addr` says otherwise and prints a one-time
token in the URL, which every request must carry.

For the underlying rolling-release machinery that all three update paths track
(tracking `develop`, tagged releases, pinned commits, and the prebuilt-binary
pre-releases), see
[auto-update.md](auto-update.md).
