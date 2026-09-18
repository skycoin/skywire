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

## Refreshing a config from its SKYENV file

`SKYENV=<file> skywire autoconfig --no-restart` is the way to re-derive a
JSON config from a conf file without touching systemd. Both autoconfig and
`cli config gen` read `OUTPUT` from the conf file and write there — an `-o`
on the command line still wins, and a relative `OUTPUT` resolves against the
current working directory, exactly as a relative `-o` does — so a
checkout that keeps its own `skywire.conf` and `skywire-config.json` side by
side does not need the system paths at all. `--no-restart` leaves the service
alone, which is what you want whenever something other than systemd owns the
visor process — a dev loop, a container entrypoint, or an update applied over
the visor's own dmsgpty session. The regen retains the keys, the app list, the
resolver settings and the maps the visor persists itself
(`routing.router_settings`, `launcher.app_settings`), so it is safe to run on
every start. `scripts/dev-visor-loop.sh` does exactly that before each visor
start, which makes `./skywire.conf` the source of truth for the dev visor:
edit it, let the loop restart, and the change is in the JSON.

TODO: the CI e2e visors still build their configs with `config gen` directly
and so do not pick up conf-file changes the same way; they need the same
refresh step.

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
