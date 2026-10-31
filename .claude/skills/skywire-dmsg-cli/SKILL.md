---
name: skywire-dmsg-cli
description: >-
  The `skywire cli dmsg` and `skywire cli mdisc` command groups, dmsg-client
  control on a running visor, standalone dmsg tools (curl/cat/scp/iperf/chat/
  probe/sub/smb), and dmsg-discovery queries. Verified command + flag surface,
  what is read-only vs disruptive, and the relationship to `cli pty`. Load when
  working on dmsg CLI, dmsg discovery lookups, dmsg reachability/health, or the
  standalone dmsg bridges.
---

# skywire dmsg / mdisc CLI

Source: `cmd/skywire-cli/commands/dmsg/` and `cmd/skywire-cli/commands/mdisc/`.
Check any flag against `--help`, which is always current.

## TWO COMMAND TREES, `skywire cli dmsg` vs `skywire dmsg` (don't conflate)
This skill covers **`skywire cli dmsg`** (cmd/skywire-cli/commands/dmsg, visor
client control + hoisted standalone tools). There is a SEPARATE top-level
**`skywire dmsg`** tree (cmd/dmsg): `conf`, `curl`, `disc`, `http`, `ip`,
`self-ping`, `server`, `socks`, `web`. Notable there:
- **`skywire dmsg conf probe`**, fleet × carrier reachability MATRIX with real
  Noise sessions per advertised carrier (tcp/quic/ws-wss/wt). Flags `--server
  <pk>`, `--carrier tcp,quic,ws,wt`, `--embedded`, `--json`, `--timeout`,
  `--parallel`. Complements `cli mdisc check` (HTTP-level wss-front check) and
  `cli svc health --dmsg-server <pk> --carriers …` (one server). Retries the
  discovery all_servers fetch ×3 (202 flake); `--embedded` skips it.
- **`skywire dmsg conf pull`**, refresh deployment/services-config.json
  dmsg_servers from the live discovery over dmsg; now PRESERVES address_ws /
  address_udp / protocol (it used to strip them, the cause of the stale
  2-of-9-wss embedded snapshot). Follow with `go generate ./deployment/`.
- **`skywire dmsg self-ping --server pk@ip:port`**, dial own PK through one
  specific server (round-trip + noise handshake proof).
- **`skywire dmsg curl -S pk@ip:port <dmsg-url>`**, force the fetch through ONE
  dmsg server: the way to answer "can service X be reached VIA server Y", e.g.
  `skywire dmsg curl -S <srv-pk>@<ip:port> dmsg://<svc-pk>:80/health`.
Also: the visor config now takes `dmsg.carriers` (["tcp","quic","ws","wt"]) —
ordered carrier preference for ANY visor's dmsg client (#4400).

## Three families (say which one you mean)

1. **dmsg discovery**, query the discovery service itself: `cli mdisc …`.
   Read-only HTTP(S)/dmsg lookups; no visor needed for `https://` `--url`.
2. **this-visor dmsg client**, inspect/steer the dmsg client(s) inside a
   running visor over RPC: `cli dmsg sessions|converge|connect-all|
   set-sessions|port-hits|diag …`.
3. **standalone dmsg tools**, bootstrap their OWN dmsg client (work with no
   local visor; pass `--sk` for a stable identity): `cli dmsg curl|cat|scp|
   iperf|chat|probe|sub|smb`.

`dmsg` and `dmsg discovery` are RELAY, not routed. Remote visor terminals are
NOT here, they moved to `cli pty` (see `skywire-pty` skill); the `pty.go` file
still lives in this package but `PtyCmd` is re-parented onto `cli pty`.

## mdisc, dmsg discovery queries (all read-only)

`--url`, `--cdd` and `--cfa` are persistent on `mdisc` and apply
to every subcommand.
`--stats`/`-s` is only on bare `mdisc`, where it counts entries.

| Command | What | Notes |
|---|---|---|
| `mdisc` | list dmsg clients in discovery (freq view) | `--stats`/`-s` = just a count |
| `mdisc entry <pk>` | fetch one entry (client or server) | resolves RPC→dmsg→HTTP, so dmsg:// url works |
| `mdisc servers` | dmsg servers by load: `connected~`, `avail-sess`, and `async` (largest message/TTL when the server holds messages for offline clients, `-` otherwise) | reads `/all_servers`; `--json` ok |
| `mdisc check` | probe every server's advertised wss front (DNS/TLS/426) | exits nonzero on failure; `--warn-only` to always exit 0; `--timeout`; CI-safe over https `--url` |

`connected~` is inferred: `DefaultMaxSessions(2048) - available_sessions`; a
server with a custom `max_sessions` is off by the difference.

## dmsg, this-visor client control (needs a visor on `--rpc`)

| Command | R/W | What |
|---|---|---|
| `dmsg sessions` | read | dmsg servers each visor client (main / route_setup / transport_setup) is on |
| `dmsg port-hits` | read | inbound hits to ports with no listener (dmsg err 306) |
| `dmsg diag porter` | read | ephemeral-port reservation counts (`/16384`) |
| `dmsg diag porter-diag` | read | RSN porter breakdown — **FATALs "embedded RSN not running"** on a visor without RSN |
| `dmsg diag porter-reset` | **disruptive** | free all ephemeral ports (recover from exhaustion) |
| `dmsg diag reconnect` | **disruptive** | close+re-dial ALL dmsg sessions (~15s to recover) |
| `dmsg converge` | **disruptive** | re-dial each session onto its preferred carrier (`-c wt,ws,…`) |
| `dmsg connect-all` | **disruptive** | one-shot: open a session to every known server |
| `dmsg set-sessions` | **destructive** | persist `dmsg.sessions_count` (`-n 0` = all) + connect-all |

All take `--rpc` (env `SKYWIRE_RPC`) and honor global `--json`. The disruptive
ones tear down in-flight dmsg streams, never run them against a visor doing
real work (proxy/vpn/route demos).

## dmsg, standalone tools

| Command | What | Key flags |
|---|---|---|
| `dmsg curl <dmsg-url>` | HTTP-over-dmsg fetch | via visor by default; `--sk` = standalone client; `--wt`+`--disc` = WebTransport-only; `-d/-o/-r/-t/-w/-a`; `-v`+`--verbose-level` |
| `dmsg cat <pk>:<port>` / `cat listen <port>` | splice stdio with a peer | `--transport auto\|dmsg\|skynet`; `-t` timeout; `--routes` (skynet mux); `-s` sk; listen needs a visor (dmsgscp whitelist) |
| `dmsg scp <src> <dst>` | copy a file to/from a remote dmsgscp host | one arg carries `PK:path`; scheme-aware (`skynet://<pk>:f`); `--transport auto\|dmsg\|skynet` (tcp not supported); `--standalone`; `--visor-key`; `-p` port; `-t` timeout; `-s` sk |
| `dmsg iperf <pk>:<port>` / `iperf listen <port>` | throughput / RTT measurement | `-d` duration, `-i` interval, `--block-kb`, `--rtt`+`--rtt-rate` (peer needs `listen --echo`); standalone-dmsg only |
| `dmsg chat` | interactive 1:1 TUI over dmsg | `-s` sk, `-t` recipient pk, `-p` listen port (9001); needs a TTY |
| `dmsg probe <pk> <port>` | reachability (full noise handshake) | see below |
| `dmsg sub client\|server` | standalone UDP↔dmsg bridge | own vocabulary: `--sk` (hex, env `SKYUDP_SK`), `--log-level`, `--dmsg-disc`, `--peer/--remote-port/--listen/--target` |
| `dmsg smb` | standalone SMTP→dmsg bridge | own vocabulary: `--sk` (hex, env `SKYMAIL_SK`), `--log-level`, `--addr/--suffix/--mode/--helo/--remote-port` |

`sub`/`smb` are the SAME cobra definitions as the standalone `cmd/sub` and
`cmd/smb` binaries, hoisted in, do not edit them from the dmsg package.

### probe transports/identity
- default: dmsg via visor RPC. `-s`/`--standalone` = ephemeral standalone key;
  `--sk <hex>` = standalone with a specific key (no visor).
- `--server <srv-pk>` = force the dmsg probe through one server (reachability
  via that server). `--skynet` = over the router (RPC only). `--ports
  22,80,1000-1010` = multi-port sweep (`--parallel`).
- `--via tcp://<pk>@host:port` = direct noise-TCP probe. **This shadows the
  global `--via dmsg://<pk>`**, on `probe`, `--via` means the TCP target.
- `probe` honors `--json` in every mode (single, sweep, tcp).

## Flag-vocabulary quirks (backward-compat, don't "fix" by renaming)
- `-s` = `--sk` on curl/cat/scp/chat/iperf, but `-s` = `--standalone` on probe.
- `probe --sk` is a hex string; elsewhere `--sk` is a `cipher.SecKey` var.
- `sub`/`smb` use `--log-level` (not `--loglvl`) and `--sk` hex, they are
  standalone binaries with their own flag set.
- `dmsg curl --rpc` binds a package-local `rpcAddr` (its own RPC dialer), not
  the shared `clirpc.Addr` the other commands use; it now defaults to
  `clirpc.DefaultRPCAddr` so `SKYWIRE_RPC` is honored.

## Quick verified checks
```
skywire cli mdisc servers
skywire cli mdisc check
skywire cli dmsg sessions
skywire cli dmsg diag porter
skywire cli dmsg port-hits
skywire cli dmsg probe <pk> 80          # 80 dmsghttp, 136 route-setup, 22 pty
skywire cli dmsg probe <pk> --ports 7,8,22,80,136
skywire cli dmsg curl dmsg://<pk>:80/health
```

