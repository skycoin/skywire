# The hypervisor

A hypervisor is a visor that manages other visors: it serves the web UI, and
it holds the keys of the visors it is allowed to drive. Which visors those are
is decided by pairing, and the direction of that relationship is the thing
most often got backwards — see [Pairing a desk tab](#pairing-a-desk-tab).

To generate a config with the local hypervisor enabled, see
[configuration.md](configuration.md).

## Hypervisor web UI

In order to expose the hypervisor UI, generate a config file with the `-i` / `--ishv` flag:

```
skywire cli config gen -i
```

After starting up the visor, the dashboard is on `localhost:8000` and the
desk — the wasm-visor hypervisor UI, with the dashboard as a tab inside it —
on `localhost:8002`. See [the two ports](#the-two-ports) below.

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

### The two ports

The desk — the wasm-visor hypervisor UI — and the Angular dashboard are both
served, on two ports of the same hypervisor: the dashboard at the root of
`hypervisor.addr` (`:8000`) and the desk at the root of
`hypervisor.desk_addr` (`:8002`). Everything behind them is the same — one
API, one login, every page on both — and the desk's browser opens the
dashboard as its first tab. Set the desk address with `HVDESKADDR` in
`/etc/skywire.conf` or `config gen --hvdeskaddr`. A build with no skywire
command module has nothing to host a desk out of and serves only the
dashboard.

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


A listed hypervisor PK is also what authorizes full remote CLI control of the
visor (`skywire cli --via dmsg://<pk> …`) — the trust model, the
remote-diagnosis workflow, and the mobile-visor specifics are in
[remote-visor-cli.md](remote-visor-cli.md).
