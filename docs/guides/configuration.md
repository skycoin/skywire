# Skywire Configuration

The skywire visor runs from a JSON config file. There are two ways to
produce one, and which you should use depends on how skywire was
installed.

## Which one: `autoconfig` or `config gen`

**On a packaged install, use `skywire autoconfig`.** It is the single
entry point that (re)generates the config and (re)starts the service,
and the shape of what it writes — paths, owner, systemd unit — is
driven entirely by the SKYENV file, `/etc/skywire.conf`.

That file is the durable record, not the generated JSON. Every package
update runs `skywire autoconfig` again, which rebuilds the config from
`/etc/skywire.conf` — so anything you configured by running
`skywire cli config gen` by hand, or by editing the JSON, is gone at
the next update. Configure a packaged install by setting the variable
in `/etc/skywire.conf`, or by passing the flag to `skywire autoconfig`,
which records it there for you:

```
skywire autoconfig --ishv --transport-port 7773
```

`autoconfig --help` lists the flags; each one it accepts corresponds to
a line it writes into `/etc/skywire.conf`.

**`skywire cli config gen` is the lower-level tool.** Reach for it when
there is no package and no SKYENV file — a checkout, a container, a
one-off config written somewhere specific — or when you want a config
without touching the system's. Nothing regenerates it for you, which is
exactly the property you want there and exactly the trap on a packaged
install.

The full reference for both is [config-gen.md](config-gen.md) — how the JSON
is produced, and every SKYENV variable with its default. Per-flag help is
generated from the live command tree at
[cli/config](../skywire/cli/config/README.md) and
[visor](../skywire/visor/README.md). The most important flags are noted below.

## Config gen

To generate a config in a checkout:

```
skywire cli config gen -irx
```
* **service protocol** — deployment services are reached over **dmsg only**. `-d --dmsghttp` is the default and a no-op kept for back-compat; the former `--http` and `--dual` modes are gone.
* `-i --ishv` create a local hypervisor configuration (optional)
* `-r --regen` regenerate a config which may already exist, retaining the keys
* `-x --retainhv` retain any remote hypervisors set in the config (optional)

More options are displayed with `skywire cli config gen --all`, and the SKYENV
template behind a packaged install with `skywire cli config gen -q` — see
[config-gen.md](config-gen.md).

NOTE: If you have installed skywire as a package or via the windows .msi
or mac installer, prefer `skywire autoconfig` as above. If you do run
`skywire cli config gen` there, include the `-p` flag and run it as
root — and expect the next package update to replace the result.

## Where the rest went

This page is about producing a config. The things you configure with one have
their own pages:

- [hypervisor.md](hypervisor.md) — the hypervisor web UI and its password,
  pairing a desk tab, the terminal UI, and adding a remote hypervisor
- [config-gen.md](config-gen.md) — every SKYENV variable, with its default
- [config-runtime.md](config-runtime.md) — changing a running visor without
  editing the config
- [dmsg-tools.md](dmsg-tools.md#run-a-dmsg-server-inside-the-visor) — folding
  a public dmsg server into the visor process
- [network-visualizer.md](network-visualizer.md) — the transport-graph UI
  (`cli tp viz`), which is not the hypervisor interface
