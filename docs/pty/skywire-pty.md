# Mode 1 — the visor-hosted pty (`skywire cli pty`)

When you run `skywire visor`, it launches the pty as a managed
**Internal app** (`RestartPolicy=Always`; RFC #2775 Phase 3.3). This is
the interface most people mean by "the skywire pty": no separate daemon,
no SSH keys — any peer whose PK is whitelisted can open a shell, run a
command, mount the filesystem, or open the web terminal, keyed by public
key over noise-XK, **over skynet or dmsg using the visor's own key**.

The visor-hosted mode is deliberately **not** exposed under
`skywire app pty` — you drive it with **`skywire cli pty`**. For hosts
that run *without* a visor, see [standalone-pty.md](standalone-pty.md).

## What the visor brings up

On start the visor's `pty` Internal app opens, per its config:

- a **dmsg listener** on `pty.dmsg_port` (default `22`) — the
  dmsg-overlay entry point, reached through dmsg discovery;
- a **skynet/route listener** in parallel, so the visor's MultiDialer can
  reach peers over skywire routes as well as dmsg (skynet-first, dmsg
  fallback);
- a **local CLI control socket** on `pty.cli_network` + `pty.cli_address`
  (default `unix` at `/tmp/pty.sock`) — used by same-host tooling and the
  web UI;
- a **direct-TCP listener** on `pty.ssh_listen` **only if that field is
  set** (e.g. `:2022`) — see the note below;
- a **web terminal** at `/pty` on the visor's local HTTP (logserver)
  endpoint, gated by the same whitelist.

Teardown is automatic on `skywire cli visor halt`.

> **`pty.ssh_listen` vs. mode 3.** Setting `pty.ssh_listen` makes the
> visor *itself* serve the TCP entry point — but it shares the visor's
> process lifecycle. If you want a TCP pty that **survives visor
> restarts** (e.g. to update skywire), run a *separate* mode-3 process
> instead — see [standalone-pty.md](standalone-pty.md).

## Configuration (`pty` section of `skywire-config.json`)

The legacy key `dmsgpty` is still accepted on load.

```json
{
  "pty": {
    "dmsg_port": 22,
    "cli_network": "unix",
    "cli_address": "/tmp/pty.sock",
    "whitelist": [
      "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c"
    ],
    "ssh_listen": ":2022"
  }
}
```

| JSON key | Meaning |
|----------|---------|
| `dmsg_port` | dmsg-overlay listener port. Default `22`. |
| `cli_network` / `cli_address` | local control socket. Default `unix` / `/tmp/pty.sock`. |
| `whitelist` | client PKs allowed to connect (in addition to the implicit ones below). |
| `ssh_listen` | optional direct-TCP entry point (`:2022`, `0.0.0.0:2022`, …). **Empty (default) = dmsg/skynet only.** |

### Who is authorized

The effective whitelist is assembled at start from:

1. the visor's **own PK**;
2. every PK in the visor's **`hypervisors`** list;
3. every PK in **`pty.whitelist`**.

One whitelist gates the dmsg path, the skynet path, the `ssh_listen` TCP
path, and the `/pty` web terminal.

#### Changing the whitelist at runtime

There is no `cli pty` whitelist verb; runtime changes go through the
host's **control socket** (so run these *on the visor's host*, pointed at
`pty.cli_address`):

```bash
skywire app pty exec whitelist-add    <pk> [<pk>…] --cliaddr /tmp/pty.sock
skywire app pty exec whitelist-remove <pk> [<pk>…] --cliaddr /tmp/pty.sock
skywire app pty exec whitelist                      --cliaddr /tmp/pty.sock   # list
# (hidden-alias equivalents: `dmsg pty cli whitelist-add`, etc.)
```

To persist across restarts, also add the PK to `pty.whitelist` in the
config.

## Reaching the host

### Through your local visor (the common path)

Your CLI talks to your **local** visor over RPC (`localhost:3435`); the
visor dials the remote host over its best transport.

```bash
# one-shot exec — captures stdout/stderr/exit (agent-friendly)
skywire cli pty exec <remote-pk> -- systemctl status skywire

# interactive shell
skywire cli pty start <remote-pk>

# peers the local visor can see
skywire cli pty list
```

`cli pty exec` mirrors the remote exit code locally (`0` ok, remote code
on failure, `124` on timeout, `1` on RPC-layer failure). Host-side
command cap is **5m**; favor bounded commands and use `start` for
streaming. Up to 16 MiB each of stdout/stderr is captured.

**Pin the transport** (default tries skynet, then dmsg):

```bash
skywire cli pty exec <remote-pk> --scheme dmsg   -- uname -a
skywire cli pty exec <remote-pk> --scheme skynet -- uname -a
```

### Direct TCP (ssh-shaped) — needs `ssh_listen` (or a mode-3 host)

```bash
skywire cli pty shell <remote-pk>@<host-ip>:2022                 # interactive
skywire cli pty shell <remote-pk>@<host-ip>:2022 -- uptime       # one-shot
skywire cli pty exec  <remote-pk> --via tcp://<remote-pk>@<host-ip>:2022 -- hostname
```

The client borrows the **local visor's SK** as its identity by default
(`--sk <hex>` to pin, `--no-visor-key` for a random one). Default port
`2022` (`--port`).

### Remote filesystem — `fs` (the `sshfs` equivalent)

Linux + FUSE only:

```bash
skywire cli pty fs mount <remote-pk>@<host-ip>:2022 ~/mnt/peer
skywire cli pty fs umount ~/mnt/peer
```

### Web terminal

```bash
skywire cli pty url                 # print the URL
skywire cli pty ui                  # open it
skywire cli pty ui  --visor <pk>    # a specific remote visor, via your hypervisor
```

`-p/--pkg` reads identity from `/opt/skywire/skywire.json`; `-i/--input`
from a named config. (Unlike the standalone `app pty http`, the visor UI
is whitelist-gated and can reach *remote* visors per-PK.)

## Operational notes

- **Detached long-running work.** A `pty exec` is bounded (5m cap, and the
  transport can drop). For anything that must outlive the call — a package
  upgrade, a long build — launch it as a detached unit so it survives both
  the pty session dropping *and* the controlling visor restarting:

  ```bash
  skywire cli pty exec <pk> -- systemd-run --unit=myjob --collect \
    --service-type=oneshot --property=TimeoutStartSec=4h --no-block \
    /usr/local/sbin/myjob.sh
  skywire cli pty exec <pk> -- systemctl is-active myjob          # poll
  ```

  (`--no-block` so `systemd-run` returns immediately instead of blocking
  your `exec` until the oneshot finishes.)

- **A package upgrade can restart the remote visor** — which restarts its
  pty host and drops your in-flight `exec`. A `systemd-run` unit owned by
  the remote's PID 1 is unaffected. (This is exactly why **mode 3** — a
  TCP pty independent of the visor lifecycle — is the right tool for
  updating skywire itself.)

## See also

- [README.md](README.md) — the model, all modes, command map
- [standalone-pty.md](standalone-pty.md) — modes 2 & 3, http/exec clients
