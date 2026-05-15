# sub — standalone UDP→dmsg bridge (sky UDP bridge)

Standalone, dmsg-only UDP→skywire bridge. Frames UDP datagrams
with a `uint16` length prefix and ferries them over a dmsg stream;
the peer end unframes and replays them as UDP onto a local target.

This is **Plan B** for UDP-over-skynet — TCP-shaped reliable +
in-order transport. Suitable for **non-realtime** UDP:

  - DNS (53)
  - NTP (123)
  - SNMP (161/162)
  - MQTT-SN
  - WireGuard control-plane handshakes (not bulk data)

For media-class UDP (RTP, VoIP, WebRTC media, real-time game
protocols) use **Plan A** — true packet-level UDP at the
route-group layer (RFC #2607). The TCP wrap in this bridge adds
head-of-line blocking, which is fine for DNS/NTP/SNMP (the app
retries anyway) and wrong for voice/video (one lost packet stalls
the whole stream).

Reachable two ways:

  - As a standalone binary built from `cmd/sub/` — invoke `sub …`.
  - As a subcommand of the unified skywire binary — invoke
    `skywire dmsg sub …`. Both share the same `cobra.Command`.

## Wire format

A single frame:

```
+--------+----------------+
| u16 BE | payload (≤64K) |
+--------+----------------+
```

One frame per UDP datagram. `MaxFrameSize = 65535`. Per-source UDP
flows get their own stream (per (ip, port) tuple on the client),
held open until idle (`--idle`, default 60s). Replies flow back
over the same stream/socket pair so stateful protocols (DNS req +
reply on the same socket) work.

## Usage

Two subcommands; deploy one of each (one per side of the bridge):

### Client side (host with the UDP app)

```sh
export SKYUDP_SK=<hex-secret-key>     # persistent identity
sub client \
  --listen      127.0.0.1:5353 \
  --peer        <peer-visor-pk-hex> \
  --remote-port 53
```

Then point the local app at `127.0.0.1:5353` (e.g. `dig
@127.0.0.1 -p 5353 example.com`).

### Server side (host with the UDP target)

```sh
export SKYUDP_SK=<hex-secret-key>
sub server \
  --dmsg-port 53 \
  --target    127.0.0.1:53
```

The peer dmsg port (`--dmsg-port` on server, `--remote-port` on
client) is arbitrary but must match. `--target` is the local UDP
endpoint the unframed datagrams hit (e.g. a local DNS resolver).

### Keypair sourcing

`SKYUDP_SK` env var, `--sk <hex>`, `--sk-file <path>`, or — if
nothing is provided — an ephemeral keypair generated per launch.
Use a persistent key in production so the server's allowlist
(future work via a wrapping `cli serve whitelist`) remains stable
across restarts.

## When to reach for `sub` vs the alternatives

| Need | Use |
| --- | --- |
| DNS / NTP / SNMP / MQTT-SN over skywire | `sub` (this) |
| WireGuard control plane over skywire | `sub` |
| RTP / VoIP / WebRTC media / real-time game | Plan A (RFC #2607) |
| Plain TCP over skywire | `skywire cli serve` + a TCP forwarder |
| SMTP over skywire | `smb` / `skywire cli mail` |

## Out of scope

  - DTLS / per-frame auth: dmsg already provides E2E encryption +
    PK-bound identity. Adding DTLS on top would be redundant.
  - Path MTU discovery: every frame fits in `[0, 65535]` bytes
    (UDP payload limit). Apps that fragment at IP layer are
    re-fragmented by the receiver's UDP stack as usual.
  - Source-address spoofing across the bridge: per-source flows
    are keyed by the *local* sender's (ip, port) — the peer end
    always sees the bridge's UDP source on `--target`.
