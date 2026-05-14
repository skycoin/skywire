# skymail-bridge

Sender-side SMTP proxy that routes recipient envelopes of the form
`user@<host>.<base32-pk>.skynet` (or `user@<base32-pk>.skynet`) to
the peer visor's exposed SMTP listener over the skywire mesh.

The PubKey is encoded as a single lowercase RFC 4648 base32 label
(53 chars, fits in a 63-octet DNS label per RFC 1035 §2.3.1). Hex
encoding would be 66 chars and overflow the limit, which strict
SMTP envelope parsers reject. The encoding helpers live in
`pkg/cipher` as `PubKey.DNSLabel()` and `ParseDNSLabel`.

## Modes

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
                  skymail-bridge (this app, on sender's visor)
                       │
                       │  appCl.Dial(skynet, peerPK, port 25)
                       ▼
                  receiver's visor (skywire cli serve add 25 --to 127.0.0.1:25)
                       │
                       ▼
                  receiver's Postfix smtpd on 127.0.0.1:25
                       │
                       ▼
                  local delivery (Maildir / dovecot LMTP / etc.)
```

## Sender-side setup

1. **Run the bridge as a managed visor app.** It registers itself
   via `launcher.RegisterApp` so you just enable it in your visor
   config:

   ```json
   "apps": [
     {
       "name": "skymail-bridge",
       "binary": "skymail-bridge",
       "args": ["--addr", "127.0.0.1:1025", "--mode", "b"],
       "auto_start": true,
       "port": 25
     }
   ]
   ```

   Or at runtime: `skywire cli visor app args skymail-bridge "skymail-bridge --addr 127.0.0.1:1025 --mode b"; skywire cli visor app start skymail-bridge`.

2. **Add the transport_map line to Postfix.** In `main.cf`:

   ```
   transport_maps = lmdb:/etc/postfix/transport
   ```

   In `/etc/postfix/transport`:

   ```
   .skynet  relay:[127.0.0.1]:1025
   ```

   Then `postmap /etc/postfix/transport && systemctl reload postfix`.

3. **(Optional) Local user shortcut.** Add `/etc/aliases` entries
   so users can write to `peerName` and have Postfix rewrite to the
   long base32 form via `/etc/postfix/canonical` or a virtual map.

## Receiver-side setup

Expose Postfix smtpd over the visor's skynet face:

```sh
skywire cli serve add 25 --to 127.0.0.1:25 --label skymail-bridge
```

Optionally restrict who can connect (the spam gate):

```sh
skywire cli serve whitelist 25 03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c,...
```

No Postfix-side changes are required in mode B — the bridge has
already stripped `.<pk>.skynet` from RCPT TO, so the envelope hits
your existing virtual_alias chain as `user@magnetosphere.net` (or
whatever your domain is).

## What's not yet implemented

  - **STARTTLS.** The bridge is intended to live behind localhost-
    only Postfix submission, so transport on either side is plain
    TCP. Adding STARTTLS for the outbound (skywire-side) hop is a
    straightforward extension; it's deferred until there's a
    concrete deployment that wants it.
  - **SMTP AUTH.** Auth happens between the MUA and the sender's
    Postfix; the bridge sees post-auth traffic from loopback.
  - **Multi-peer envelopes.** A single envelope with RCPT TOs that
    resolve to different peer PKs is rejected with 451 on the
    second RCPT, so Postfix retries per-recipient. Splitting in
    the bridge would require splitting the DATA stream and is
    deferred.
  - **Bounce handling on the skywire side.** A 5xx from the peer is
    surfaced verbatim back to the sender's Postfix, which queues
    and retries per its normal schedule.
