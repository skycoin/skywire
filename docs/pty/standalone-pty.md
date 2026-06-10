# Modes 2 & 3 — the standalone pty (`skywire app pty`)

The visor-hosted pty ([skywire-pty.md](skywire-pty.md)) is launched and
owned by the visor. The **standalone** modes run the pty as its own
process via `skywire app pty …`. Two server modes, plus the `http` and
`exec` clients:

| | Mode 2 — **standalone dmsg** | Mode 3 — **standalone TCP** |
|--|------------------------------|-----------------------------|
| Command | `app pty dmsg` (≡ `dmsg pty host`) | `app pty tcp` (≡ `dmsg pty host --no-dmsg --tcp-listen`); or `cli pty host` |
| Identity | its **own** keypair — **cannot** borrow the visor's | **can use the visor's PK** (`--sk-from-visor`) |
| Transport | dmsg (+ optional TCP via `--tcp-listen`) | direct TCP only, noise-XK |
| Best for | a box with no visor, or one that mustn't contend with the visor's dmsg-disc entry | a process **independent of the visor lifecycle** — survives restarts, so it's how you **update skywire itself**; plain ssh-equivalent |

Both serve the identical protocol and the same noise-XK + whitelist trust
model as the visor-hosted host. Reach either from a client with
`cli pty shell` / `cli pty exec --via` ([skywire-pty.md](skywire-pty.md))
or the standalone `app pty exec` client (below).

## Mode 2 — standalone dmsg host

### Config

Standalone hosts are config-driven and use their **own** config shape
(note: `dmsgport`/`clinet`/`cliaddr`/`wl` — *not* the visor's
`dmsg_port`/`cli_network`/`cli_address`/`whitelist`). Scaffold one:

```bash
skywire app pty dmsg confgen > ./pty-host-config.json     # ≡ dmsg pty host confgen
```

```json
{
  "sk": "<32-byte hex>",
  "pk": "<derived>",
  "dmsgdisc": "dmsg://022e607e...:80",
  "dmsgsessions": 1,
  "dmsgport": 22,
  "wl": ["<peer-pk-1>", "<peer-pk-2>"],
  "clinet": "unix",
  "cliaddr": "/tmp/pty.sock",
  "ssh_listen": ""
}
```

| Key | Meaning |
|-----|---------|
| `sk` / `pk` | the host's **own** identity (a standalone dmsg host cannot share the visor's key). |
| `dmsgdisc` / `dmsgsessions` | dmsg discovery + min sessions. |
| `dmsgport` | dmsg-side listener port. Default `22`. |
| `wl` | whitelist — **empty rejects all incoming requests.** The host's own PK is implicitly allowed. |
| `clinet` / `cliaddr` | local control socket (drives the host locally; also what `app pty http` bridges to). Default `unix` / `/tmp/pty.sock`. |
| `ssh_listen` | optional direct-TCP entry point (same as `--tcplisten`); empty disables it. |

### Run it

```bash
# dmsg-only
skywire app pty dmsg -c ./pty-host-config.json --dmsgport 22

# dmsg + a direct-TCP entry point (whitelist-gated, mirrors pty.ssh_listen)
skywire app pty dmsg -c ./pty-host-config.json --dmsgport 22 --tcplisten :2022
```

Peers reach it exactly as they would a visor (their PK must be in `wl`):

```bash
skywire cli pty exec  <host-pk> -- hostname        # via their visor, over dmsg
skywire cli pty start <host-pk>
```

Useful flags (`app pty dmsg --help` for the full list): `--no-dmsg`
(drop the dmsg side — that's mode 3), `--sk-from-visor <skywire.json>`,
`--listen-fd N` (systemd socket-activation, one conn per process),
`--confstdin`.

## Mode 3 — standalone TCP host (ssh-equivalent)

TCP-only, no dmsg client, no discovery entry. **Its key point is process
independence:** because it's a separate process from the visor, it keeps
serving across visor restarts — so it's the right surface for **updating
skywire itself** or as a plain low-latency ssh replacement on a known IP.

Two ways to start it:

```bash
# operator-friendly (recommended): cli pty host
skywire cli pty host --listen :2022 --allow <client-pk>,<client-pk> \
  --sk-from-visor /opt/skywire/skywire.json

# app-framework form
skywire app pty tcp --addr :2022          # ≡ app pty dmsg --no-dmsg --tcp-listen :2022
```

`cli pty host` flags (the `sshd`-equivalent surface):

| Flag | Meaning |
|------|---------|
| `-l, --listen` | TCP bind (`:2022` default; `0.0.0.0:2022`, `127.0.0.1:2022`, IPv6 ok). |
| `--sk-from-visor` | use a `skywire-config.json`'s `.sk` as identity — **this is how mode 3 runs under the visor's PK**; empty tries `/opt/skywire/skywire.json`. |
| `--allow` | client PKs allowed (the `authorized_keys` equivalent); merged with `--conf`. |
| `-c, --conf` | optional standalone config for whitelist + identity. |

### Client side (any mode-2/3 host)

From a peer, the ssh-shaped client lives under `cli pty`:

```bash
skywire cli pty shell <host-pk>@<host-ip>:2022                  # interactive
skywire cli pty shell <host-pk>@<host-ip>:2022 -- systemctl status skywire
skywire cli pty fs mount <host-pk>@<host-ip>:2022 ~/mnt/peer    # sshfs equivalent
```

The client borrows the local visor's SK by default (`--sk` to pin,
`--no-visor-key` for random).

## Exposing a TCP entry point outside the LAN

`--listen :2022` (and `pty.ssh_listen ":2022"`) bind all interfaces; to
accept from outside the LAN you must route the port in:

- **Home router:** forward external TCP `2022` → `<host-lan-ip>:2022`.
- **VPS / cloud:** open the port in the provider firewall **and** the host
  firewall (`ufw allow 2022/tcp`, `firewall-cmd --add-port=2022/tcp
  --permanent`).
- **Mesh (Tailscale/WireGuard/ZeroTier):** bind the mesh interface —
  `--listen 100.x.y.z:2022` — and don't expose publicly.
- **systemd socket-activation:** `--listen-fd N` and front the `.socket`
  unit however you like.

## The `app pty http` browser client

`app pty http` (≡ `dmsg pty ui`) serves a WebSocket browser terminal — but
note what it actually does:

```bash
skywire app pty http --addr :8080 --hnet unix --haddr /tmp/pty.sock
```

- It dials a **local control socket** (`--hnet`/`--haddr`, default
  `unix`/`/tmp/pty.sock`) — the *same* socket a mode-1/2/3 host exposes as
  its `cliaddr`. So it gives a browser shell **on that local host**; it
  does **not** traverse dmsg/TCP/skynet to remote peers (that's the
  visor's `/pty` UI, `cli pty ui --visor <pk>`).
- It serves **without a PK gate** (`Handler(nil)`). Secure it by **not
  exposing `:8080`** (bind localhost / firewall) — unlike the dmsg/TCP
  paths, which are whitelist-gated.
- **Socket collision:** all hosts default `cliaddr` to `/tmp/pty.sock`, so
  on one machine only one owns it — set distinct `cliaddr`s to bridge a
  specific host.

In short: a local browser convenience for a standalone host; for the
visor you'd normally use its built-in `/pty` UI instead.

## The `app pty exec` standalone client

When you don't want to route through a visor, `app pty exec`
(≡ `dmsg pty cli`) is the standalone client:

```bash
skywire app pty exec                                   # interactive (local or --addr <pk:port>)
skywire app pty exec exec <pk> -- <cmd>                # one-shot (≡ dmsg pty cli exec)
skywire app pty exec whitelist                         # list a host's whitelist
skywire app pty exec whitelist-add    <pk> [<pk>…]     # mutate via its control socket
skywire app pty exec whitelist-remove <pk> [<pk>…]
```

`--addr <pk>:<port>` targets a remote host; without it the session is
local. `--cliaddr`/`--clinet` select the control socket for the
whitelist verbs.

## Verifying

```bash
ls -la /tmp/pty.sock                                   # control socket up?
skywire cli pty exec  <host-pk> -- hostname            # dmsg round-trip (mode 2)
nc -zv <host-ip> 2022                                  # TCP reachable (mode 3)
skywire cli pty shell <host-pk>@<host-ip>:2022 -- hostname
```

## See also

- [README.md](README.md) — the model, all modes, redundancy table
- [skywire-pty.md](skywire-pty.md) — mode 1 (visor-hosted) + `cli pty`
- `skywire app pty <mode> --help` — current per-mode flags
