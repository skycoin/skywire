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

## Hypervisor web UI

In order to expose the hypervisor UI, generate a config file with the `-i` / `--ishv` flag:

```
skywire cli config gen -i
```

After starting up the visor, the UI will be exposed by default on `localhost:8000`.

From another device on the same LAN, use the machine's mDNS name:
`http://<hostname>.local:8000/`. That name comes from the OS (Avahi on
Linux, Bonjour on macOS/Windows); the visor advertises nothing itself.

The UI password gate is on for a freshly generated hypervisor config. The
first visit creates the account, and the username is always `admin` — it is
fixed in the hypervisor, not a choice, and any other name is refused with
`name not allowed` without saying what would be accepted. The password needs
6–64 characters with at least one upper, one lower, one digit and one special.

To change a password you know, use `skywire cli visor hv passwd --old <old>
--new <new>` on the machine itself.

**If you have forgotten it**, the same command resets it — that is what
`--force` is for: it sets `--new` without asking for the old one (it is also
how you set the first password non-interactively).

```
skywire cli visor hv passwd --force --new <new-password>
```

It changes the web login and nothing else: every paired tab and every managed
visor is untouched. The account lives in the file named by `hypervisor.db_path`
in the visor config — `/opt/skywire/users.db` on a Linux package install,
`/Library/Application Support/Skywire/users.db` on macOS,
`C:/Program Files/Skywire/users.db` on Windows, and `~/.skywire/users.db` for a
userspace install. You should not need to touch it; deleting it also works, and
is the bigger hammer.

### Pairing a desk tab

A desk tab opened from that page runs its own visor and asks to drive the
host as a hypervisor. Approve it on the machine with
`skywire cli visor hv pair` (lists pending tabs by fingerprint, then
`hv pair <fingerprint>`), or mint a one-time code with
`skywire cli visor hv pair --code` and type it into the tab's Pair window.
Approval is written to the visor config and, on a package install, mirrored
into `HYPERVISORPKS` in `/etc/skywire.conf`, so it survives the next
`skywire autoconfig` run (every package update performs one).

Note which way that points: approval makes the TAB the hypervisor and the
machine the thing it drives. The tab does not appear in the machine's
`hv ls`; the machine appears in the tab's. Checking the wrong end and
concluding the pairing failed is an easy mistake — the tab is the manager.

### Resetting a tab's identity

A desk tab's visor has its own keypair, and it keeps it in the browser: in
the page's IndexedDB under `skywire-desk`, as part of the filesystem snapshot
the tab restores on load. It is NOT in `localStorage`, so clearing that alone
changes nothing.

Discarding it gives the tab a new public key, which the machine then sees as
a new pending peer to pair. The previous approval stays behind, against a key
nothing holds any more; `skywire cli visor hv rm <old-pk>` removes it. (That
command is not in `hv --help`'s list but it is there.)

The exec worker holds the database open while the visor runs, so a delete
attempted from the running page is refused as blocked. Navigate the tab away
first — the browser's own "clear site data" does this for you, or from the
page's console:

```js
location.href = 'about:blank';           // releases the worker's handle
indexedDB.deleteDatabase('skywire-desk'); // then this succeeds
location.href = '/';                      // back; a new key is generated
```

Run those one at a time, waiting for each. The delete reports success
whether or not there was anything to delete, so confirm by the key changing
rather than by the result: `skywire cli visor pk` in the tab's terminal
before and after.

The desk is the default hypervisor UI. To serve the legacy Angular dashboard
at the root instead, with no desk and no wasm visor in the page, set
`LEGACYHVUI=true` in `/etc/skywire.conf` (or `config gen --legacy-hv-ui`),
or switch a running hypervisor with `skywire cli visor hv enable --legacy -w`
(`--legacy=false` switches back). The change applies on the next page load.

## Hypervisor terminal UI

A terminal-based hypervisor that mirrors the web UI's read and write actions
without requiring a browser:

```
skywire cli visor hv tui
```

Lists all visors connected to the running hypervisor and (when one is selected)
shows transports, apps, route groups with full multi-hop paths, DMSG servers,
and a row of hotkey-driven actions: start/stop apps, set min_hops/mux_routes,
manage transports/routes, toggle resolving proxies, register skynet/forwarded
ports, run dmsg connect-all, view services-health, reload or shutdown.

When a hypervisor has another hypervisor in its `hypervisors` config, the
parent hypervisor transparently sees and manages the child's connected visors —
write actions are routed through the child automatically.

## Add remote hypervisor

Every visor can be controlled by one or more hypervisors. To allow a hypervisor
to access a visor, the PubKey of the hypervisor needs to be specified in the
configuration file. You can add a remote hypervisor to the config with:

```
skywire cli config update hv --add-pks <public-key>
```
OR:
```
skywire cli config gen --hvpks <public-key>
```
OR, on a running visor:
```
skywire cli visor hv add <public-key>
```
This connects out at once and admits the key for RPC and pty at once. It is
written to skywire-config.json and, on a package install, mirrored into
`HYPERVISORPKS` in `/etc/skywire.conf`, so it survives the `skywire
autoconfig` run every package update performs. `hv rm <public-key>` undoes
all of it. A desk tab pairs the same way through `hv pair` (see above).

A paired hypervisor that is offline is not dialed aggressively: once its
dmsg entry is gone the visor doubles its wait up to one minute between
attempts, and connects within seconds when the peer comes back over a direct
transport (a re-opened tab).

## Run a dmsg server inside the visor

A host that runs a public dmsg server as a separate unit
(`skywire dmsg server start /etc/skywire-dmsg.json`) can fold it into the
visor process and save the second Go runtime. Stop and disable that unit,
drop it from `RESTART_SERVICES`, then set in `/etc/skywire.conf`:

```
DMSGSERVERCONF='/etc/skywire-dmsg.json'
```

and run `skywire autoconfig`. The server keeps its own key, ports, wss domain
and health endpoint from that file; it appears in `skywire cli mdisc servers`
as before. In the visor config this is `dmsg.server.config_path`.

A listed hypervisor PK is also what authorizes full remote CLI control
of the visor (`skywire cli --via dmsg://<pk> …`) — the trust model,
the remote-diagnosis workflow, and the mobile-visor specifics are in
[remote-visor-cli.md](remote-visor-cli.md).

## Network Visualization UI

Skywire includes a network visualization and visor control interface that can run in two modes.

**Visor-embedded mode** (recommended when visor is running):
```
skywire cli tp viz --visor
```
This starts the UI as part of the visor, with direct access to local transport and route data.

**Standalone mode** (network visualization only):
```
skywire cli tp viz
```
This runs a standalone visualization server using transport discovery data.

The web UI (default `localhost:8080`) provides:

* **Real-time network graph**: Visual representation of visors and their connections in the Skywire network
* **Transport information**: View active transports with details on type (STCPR, SUDPH, DMSG), remote public keys, and connection status
* **Geographic clustering**: Visors are grouped by country and IP subnet for easier network topology understanding
* **Click-to-copy**: Easily copy public keys by clicking on nodes in the graph
* **Visor control** (visor mode): Direct interface for managing the local visor

Note: This is a separate UI from the hypervisor interface and caches transport data locally.
