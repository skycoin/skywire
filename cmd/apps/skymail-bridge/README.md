# skymail-bridge

SMTP-aware proxy that routes recipient envelopes of the form
`user@<host>.<base32-pk>.skynet` (or `user@<base32-pk>.skynet`) to
the peer visor's exposed SMTP listener over skywire.

The PubKey is encoded as a single lowercase RFC 4648 base32 label
(53 chars, fits in a 63-octet DNS label per RFC 1035 §2.3.1). Hex
encoding would be 66 chars and overflow the limit, which strict
SMTP envelope parsers reject. The encoding helpers live in
`pkg/cipher` as `PubKey.DNSLabel()` and `ParseDNSLabel`.

## Two binaries, same protocol core

The bridge ships as two binaries that share `pkg/skymailbridge` for
the SMTP parser, recipient address parser, and peer-relay logic.
Only the **peer dial step** differs:

| Binary | Path | How it dials peers | When to use |
|---|---|---|---|
| Visor app | `cmd/apps/skymail-bridge` | `app.Client.Dial` over the visor's running session (skywire routing layer) | You already run a full visor — no second identity, no second dmsg connection |
| Standalone | `cmd/skymail-bridge` | `dmsg.Client.Dial` directly with its own keypair | No visor on this host — a tiny VPS that just bridges outbound mail onto dmsg |

Both speak identical SMTP on the inbound side, so the sender's
Postfix config is the same for either. Pick the variant that fits
your deployment.

## Address modes

The `--mode` flag chooses how RCPT TO is rewritten before forwarding:

  - **`b` (default)** — strip `.<pk>.skynet` from the recipient
    domain. `user@magnetosphere.net.<pk>.skynet` → the peer's
    Postfix sees `RCPT TO:<user@magnetosphere.net>`. Receiver keeps
    its existing domain identity; **no `mydestination` change**
    required on the receiver's Postfix.
  - **`a`** — forward verbatim. `user@<pk>.skynet` → peer's Postfix
    receives `RCPT TO:<user@<pk>.skynet>`. Receiver must add
    `<pk>.skynet` to `mydestination`.

Mode B is recommended for most deployments.

## Architecture

```
                  +-- public internet --+
                  |                     |
   MUA → submission → sender's Postfix
                       │
                       │  transport_map: .skynet  relay:[127.0.0.1]:1025
                       ▼
                  skymail-bridge (app OR standalone)
                       │
                       │  appCl.Dial (app)   OR   dmsg.Client.Dial (standalone)
                       │  → peer routing port 25
                       ▼
                  receiver's visor (skywire cli serve add 25 --to 127.0.0.1:25)
                       │
                       ▼
                  receiver's Postfix smtpd on 127.0.0.1:25
                       │
                       ▼
                  local delivery (Maildir / dovecot LMTP / etc.)
```

## Sender-side setup (visor-app flavor)

A fresh `skywire cli config gen` produces a `skymail-bridge` app
entry with `auto_start: false` and the documented default args.
To turn it on:

```sh
# Either toggle via the CLI (Args persist in the visor config JSON
# automatically, so the setting survives restart)…
skywire cli visor app args skymail-bridge "skymail-bridge --addr 127.0.0.1:1025 --mode b"
skywire cli visor app start skymail-bridge

# …or set auto_start=true in the config file directly:
#   "apps": [{ "name": "skymail-bridge", "auto_start": true, ... }]
```

The launcher persists every app's `Args []string` in the visor
config; no extra plumbing is needed for runtime changes to survive
across restarts.

Then add the Postfix transport_map line. In `main.cf`:

```
transport_maps = lmdb:/etc/postfix/transport
```

In `/etc/postfix/transport`:

```
.skynet  relay:[127.0.0.1]:1025
```

Then `postmap /etc/postfix/transport && systemctl reload postfix`.

## Sender-side setup (standalone flavor)

For hosts without a full visor, run the standalone binary directly:

```sh
# Provide a stable identity via env / file / flag (otherwise an
# ephemeral keypair is generated on each launch — fine for testing,
# bad for production because the peer's allowlist won't recognize
# the PK across restarts).
export SKYMAIL_SK="<hex-secret-key>"

skymail-bridge \
  --addr   127.0.0.1:1025 \
  --mode   b \
  --suffix .skynet \
  --remote-port 25
```

Postfix transport_map is identical to the app flavor.

## Receiver-side setup

The receiver doesn't run skymail-bridge at all — it just exposes
its local Postfix smtpd over the visor's skynet face:

```sh
skywire cli serve add 25 --to 127.0.0.1:25 --label skymail-bridge
```

Restrict who can connect (the spam gate) via the standard serve
whitelist:

```sh
skywire cli serve whitelist 25 03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c
```

In mode B no Postfix-side changes are required — the bridge has
already stripped `.<pk>.skynet` from RCPT TO, so the envelope hits
your existing virtual_alias chain as `user@magnetosphere.net` (or
whatever your domain is).

## What's not implemented yet

  - **STARTTLS on either hop.** The bridge is intended for
    localhost-only Postfix submission, so transport is plain TCP on
    the local side; the skywire layer encrypts the cross-wire hop.
  - **SMTP AUTH on the bridge inbound side.** Auth happens between
    the MUA and the sender's Postfix; the bridge sees post-auth
    traffic from loopback.
  - **Multi-peer envelopes.** A single envelope with RCPT TOs that
    resolve to different peer PKs is rejected with 451 on the
    second RCPT, so Postfix retries per-recipient.
  - **Bounce handling on the skywire side.** A 5xx from the peer is
    propagated verbatim back to the sender's Postfix.
