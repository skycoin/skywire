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

For the underlying rolling-release machinery that all three update paths track
(tracking `develop`, tagged releases, pinned commits, and the prebuilt-binary
pre-releases), see
[AUTO_UPDATE.md](https://github.com/skycoin/skywire/blob/develop/AUTO_UPDATE.md).
