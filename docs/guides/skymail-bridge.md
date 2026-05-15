# Email over skywire — operator setup

End-to-end recipe for running e-mail over `.skynet` / `.dmsg`
addresses using a regular Postfix install backed by the visor's
embedded SMTP bridge (`pkg/visor/embedded_skymail_bridge.go`,
introduced in PR #2598).

The shape is `user@<vhost>.<base32-pk>.<skynet|dmsg>`. The bridge
strips `.<base32-pk>.<skynet|dmsg>` from RCPT TO before forwarding
to the receiver's Postfix, so the receiver's existing
`virtual_alias_maps` chain delivers to the unmodified
`user@<vhost>` mailbox.

The base32-PK form is required because RFC 1035 caps DNS labels at
63 octets and SMTP inherits that — the 66-character hex form
breaks SMTP parsing. The base32 encoding of the 33-byte
secp256k1-compressed PK is 52 characters and fits one label.
Translate via `skywire cli mail addr <pk>`.

## Receiver side

The receiving host runs the visor + Postfix smtpd. Expose port 25
on the visor's skywire face and whitelist the sender PKs you
accept mail from:

```sh
skywire cli serve add 25 --to 127.0.0.1:25 --label skymail-bridge
skywire cli serve whitelist 25 <sender-pk-1>,<sender-pk-2>
```

That's the entire receiver-side skywire config. The visor now
accepts inbound SMTP streams on `<self-pk>:25` over both skynet
and dmsg, and forwards them to localhost:25 (Postfix's smtpd).

## Sender side

The sending host runs the visor + Postfix. Enable the bridge and
wire Postfix's `transport_maps` to route `.skynet` / `.dmsg`
recipients through the bridge:

```sh
# 1. Start the in-process bridge (listens on 127.0.0.1:1025 by default)
skywire cli mail up

# 2. Persist across visor restarts by adding to your skywire config:
#    "skymail_bridge": { "enable": true, "addr": "127.0.0.1:1025", "mode": "b" }

# 3. Tell Postfix where to send .skynet / .dmsg envelopes:
cat <<EOF | sudo tee -a /etc/postfix/transport
.skynet  relay:[127.0.0.1]:1025
.dmsg    relay:[127.0.0.1]:1025
EOF
sudo postmap /etc/postfix/transport
sudo postconf -e 'transport_maps = lmdb:/etc/postfix/transport'
sudo systemctl reload postfix
```

The `.skynet` and `.dmsg` suffixes both terminate at the bridge,
which then picks the underlying transport based on the suffix —
`.skynet` uses the skywire router (works over arbitrary visor
transports including stcpr / sudph); `.dmsg` uses dmsg directly.

## Required Postfix overrides for synthetic domains

Stock Postfix rejects mail addressed to or from a domain that
doesn't resolve in DNS. `.skynet` and `.dmsg` are synthetic — they
exist only inside the bridge — so both checks need exemptions on
the receiving and sending sides respectively.

### Sender domain check (sender side)

When you send mail **from** a `.skynet` / `.dmsg` identity (e.g.
via Roundcube, Thunderbird, `mail` on the command line), Postfix
runs `reject_unknown_sender_domain` and rejects with:

```
SMTP Error (450): 4.1.8 <user@host.base32pk.skynet>: Sender
address rejected: Domain not found
```

Fix: whitelist `.skynet` and `.dmsg` sender suffixes via a regexp
map that runs **before** the DNS check.

```sh
sudo tee /etc/postfix/sender_access_skynet >/dev/null <<'EOF'
/\.skynet$/  OK
/\.dmsg$/    OK
EOF
sudo postconf -e \
  'smtpd_sender_restrictions = reject_non_fqdn_sender, check_sender_access regexp:/etc/postfix/sender_access_skynet, reject_unknown_sender_domain'
sudo systemctl reload postfix
```

### STARTTLS requirement (receiver side)

The bridge dials the receiver's `127.0.0.1:25` after the skywire
hop. If the receiver's Postfix has
`smtpd_tls_security_level = encrypt`, the bridge gets `530 5.7.0
Must issue a STARTTLS command first` and mail stays deferred
(visible in `mailq` with a `-` prefix on the queue ID).

The bridge doesn't speak STARTTLS by design — skywire's noise
tunnel already provides end-to-end encryption on the wire, and
the bridge→smtpd hop is loopback only. The TLS layer between them
is redundant.

Fix: relax the receiver smtpd's TLS requirement. The Postfix
default `may` allows TLS when offered but does not require it:

```sh
sudo postconf -e 'smtpd_tls_security_level = may'
sudo systemctl reload postfix
```

Security tradeoff: this also stops requiring TLS for **other**
inbound smtpd connections. The mitigations to be aware of:

  - The `cli serve` whitelist on port 25 already restricts who
    on the skywire side can reach the smtpd.
  - The bridge→smtpd hop is loopback; non-skywire access to
    localhost:25 should be restricted at the host level via
    firewall or `mynetworks_style = host`.
  - If you also accept legitimate Internet-side SMTP on port 25,
    a per-listener override in `master.cf` is the right shape:
    keep the public smtpd on `smtpd_tls_security_level=encrypt`,
    add a 127.0.0.1-only smtpd entry with the relaxed setting.
    See "Per-listener TLS override" below.

### Per-listener TLS override (recommended for mixed deployments)

If the host also receives mail from the Internet, leave the
public smtpd on encrypt and add a separate loopback-only smtpd
for the bridge:

```
# /etc/postfix/master.cf
127.0.0.1:smtp inet n - n - - smtpd
  -o smtpd_tls_security_level=may
  -o smtpd_relay_restrictions=permit_mynetworks,reject
  -o smtpd_sender_restrictions=reject_non_fqdn_sender,check_sender_access regexp:/etc/postfix/sender_access_skynet,reject_unknown_sender_domain
```

Then point the bridge at this listener instead of the global
`:25`:

```sh
skywire cli serve add 25 --to 127.0.0.1:25 --label skymail-bridge
# (still points at localhost:25; the listener above replaces the
#  default :25 with a relaxed-TLS variant)
```

## Smoke test

From the sender host, send a message to yourself on the receiver:

```sh
echo "hello over skywire" | mail -s "skymail smoke test" \
  d0mo@magnetosphere.net.<receiver-base32-pk>.skynet
```

Watch the receiver's `journalctl -u postfix -f`. You should see:

```
postfix/relay/smtp[...]: ...
  to=<d0mo@magnetosphere.net.<receiver-base32-pk>.skynet>,
  relay=127.0.0.1[127.0.0.1]:1025,
  status=sent (250 2.0.0 Ok: queued via skymail-bridge)

postfix/local[...]: ...
  to=<d0mo@magnetosphere.net>,
  relay=local,
  status=sent (delivered to maildir)
```

The first log line is Postfix handing the envelope to the bridge
on `127.0.0.1:1025`. The second is the receiver Postfix delivering
the stripped-form recipient to local Maildir via `virtual_alias`.
Cross-host pipeline runs the same way; the only difference is
that the bridge dials a remote visor PK over skywire instead of
looping back through the same node.

If the message stays in `mailq` with a `-` prefix, check both
overrides above — sender-side regexp whitelist and receiver-side
smtpd TLS. The Postfix journal tells you which check rejected.

## Address shape and mode B vs mode A

Two operator modes:

- **Mode B (default, recommended)**: addresses use the
  `user@<vhost>.<base32-pk>.<skynet|dmsg>` form. The bridge
  strips `.<base32-pk>.<skynet|dmsg>` from RCPT TO before
  forwarding so the receiver's Postfix sees a plain
  `user@<vhost>` and dispatches via `virtual_alias_maps` as
  usual. **No receiver-Postfix changes needed beyond
  `virtual_alias_domains`.**

- **Mode A**: addresses use the verbatim `user@<base32-pk>.skynet`
  form. The bridge forwards verbatim. Receiver must add
  `<base32-pk>.skynet` to `mydestination`. Useful only when you
  don't want a vhost layer.

Set the mode in the visor config:

```jsonc
"skymail_bridge": {
  "enable": true,
  "addr": "127.0.0.1:1025",
  "mode": "b"  // or "a"
}
```

## Where the code lives

| Component | Location |
|---|---|
| In-process bridge (visor-embedded) | `pkg/visor/embedded_skymail_bridge.go` |
| Bridge SMTP core | `pkg/skymailbridge/server.go` |
| Standalone bridge binary (no-visor) | `cmd/smb/` |
| Visor RPC + lifecycle (Start/Stop/IsRunning) | `pkg/visor/embedded_skymail_bridge.go` |
| CLI: `skywire cli mail` | `cmd/skywire-cli/commands/mail/mail.go` |
| Config section | `visorconfig.SkymailBridgeConfig` in `pkg/visor/visorconfig/v1.go` |
