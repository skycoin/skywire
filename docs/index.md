# Skywire documentation

Skywire is a peer-to-peer privacy-focused networking suite developed
by Skycoin. Visors are reachable over two encrypted networks — Skywire
(direct routing) and DMSG (relay) — addressed by 33-byte public keys.

This site collects everything an operator or developer needs to run,
extend, or understand a Skywire deployment.

## Where to start

<div class="grid cards" markdown>

- :material-rocket-launch: **[Guides](guides/)**

    Install, configure, and operate a visor. VPN, SOCKS5, SkyNet
    port forwarding, manual routing, hypervisor UI.

- :material-console: **[Command Reference](skywire/)**

    Every `skywire <subcommand>` page with flags, usage, examples,
    and live sample output. Generated from the cobra tree on every
    commit to `develop`.

- :material-book-open-page-variant: **[Specs](specs/)**

    Protocol-level specifications: transports, packets, routing,
    setup-node, hypervisor architecture, address-resolver privacy.

- :material-trophy-outline: **[Rewards](rewards/)**

    Eligibility rules for the Skywire reward distribution.

</div>

!!! note "Per-app and per-service docs"

    Deep documentation for visor-hosted apps (skychat, skysocks, skynet, vpn)
    and for the deployment services (transport-discovery, route-finder,
    service-discovery, address-resolver, uptime-tracker) currently lives
    alongside the source under
    [`cmd/apps/`](https://github.com/skycoin/skywire/tree/develop/cmd/apps)
    and [`cmd/svc/`](https://github.com/skycoin/skywire/tree/develop/cmd/svc).
    They'll be integrated into this site in a follow-up once their
    cmd/-relative links are rewritten to live under `docs/`.

## Other resources

- [Skycoin Blog](https://blog.theskywirenetwork.net) — release notes,
  protocol updates, and ecosystem news
  ([mirror: skycoin.github.io/blog](https://skycoin.github.io/blog))
- [Source on GitHub](https://github.com/skycoin/skywire)
- [Telegram](https://t.me/skywire)
- [Skywire Wiki](https://github.com/skycoin/skywire/wiki) — operator
  walkthroughs, troubleshooting, package install guides
- [skywire-deployment](https://github.com/skycoin/skywire-deployment) —
  run your own deployment of the discovery / route-finder / uptime
  services

## Regenerating this site

The command-reference subtree (`docs/skywire/`) is generated from the
live cobra tree by the hidden `skywire doc` subcommand. Regenerate
after CLI changes:

```
make doc-gen
```

The site itself builds via the `Docs` GitHub Actions workflow on every
push to `develop`, deploying to the `gh-pages` branch. For local
preview:

```
make docs-serve
```
