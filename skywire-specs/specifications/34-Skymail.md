# Skymail

| Status   | Implemented                                                            |
|----------|------------------------------------------------------------------------|
| Revision | 2026-11-01, skywire 9be7f473e                                          |
| Code     | `pkg/skymail`, `pkg/skymailbridge`, `cmd/apps/skymail`, `pkg/visor/skymail.go`, `pkg/app/appserver/mail_host.go` |

## Abstract

Skymail is mail between visors. Every visor runs a mailbox app that accepts
SMTP on port 25 over dmsg and skynet for addresses at its own public key,
and stores what it accepts in a Maildir. The transport authenticates the
sending visor's key, which replaces the domain, certificate, SPF, DKIM and
DMARC checks of internet mail. A sender that cannot reach a recipient keeps
the message and retries, and can hand a sealed copy to other visors that
deliver it on its behalf.

## Overview

The mailbox is the `skymail` app (`cmd/apps/skymail`). The visor starts it
through the app launcher like any other app, in process by default. The app
reaches the network only through the app client, and the visor reaches the
app only through the app's control surface.

```
 sender visor                                      recipient visor
 +-------------------+     SMTP over dmsg or      +-------------------+
 | skymail app       | ------ skynet, port 25 --> | skymail app       |
 |  Outbox, helpers  |                            |  Maildir          |
 +---------+---------+                            +---------+---------+
           | app RPC: Listen, Dial,                         |
           | MailSeal, MailOpen, MailHostInfo               | control surface
 +---------+---------+                            +---------+---------+
 | visor (secret key,|                            | visor mail API    |
 | hypervisor list)  |                            | CLI, hvui, phone  |
 +-------------------+                            +-------------------+
```

A host that runs its own mail server can hand port 25 to it with
`serve add 25`. The mailbox app then does not start, and the separate SMTP
bridge (`pkg/skymailbridge`, embedded in the visor) relays between that mail
server and other visors. The bridge is outside the scope of this document
apart from the recipient address syntax it shares.

## Addressing

An address is `<local>@[<host>.]<label><suffix>`.

- `<label>` is the 53 character base32 form of the recipient's 33 byte
  compressed secp256k1 public key (`cipher.PubKey.DNSLabel`,
  `cipher.PubKeyDNSLabelLen`). The 66 character hex form is longer than the
  63 octet DNS label limit (RFC 1035 section 2.3.4) that SMTP domains follow.
- `<suffix>` is `.skynet` or `.dmsg` (`skymail.Suffixes`). It selects the
  network the sender MUST use. A sender MUST NOT deliver a `.skynet` address
  over dmsg or a `.dmsg` address over skynet. A dmsg session may itself run
  over a skynet route, which is a choice of the dmsg client.
- `<local>` is any local part. Every local part at the same label reaches
  the same mailbox.
- `<host>` is optional and only meaningful to the SMTP bridge, which strips
  `.<label><suffix>` and forwards to `<local>@<host>` on its host's mail
  server. A mailbox matches only on `<label>` (`skymailbridge.ParseRecipient`).

The mailbox listens on port 25 (`skymail.Port`) on both networks. A visor
without skynet listens on dmsg only.

## SMTP Profile

The mailbox speaks the subset of SMTP (RFC 5321) implemented in
`skymailbridge.handleSession`.

- Greeting `220 <label>.skynet ESMTP skymail`.
- `EHLO` answers with the host name, `SIZE <limit>` (RFC 1870),
  `SKYMAIL-SEALED` when the mailbox opens sealed envelopes, and `8BITMIME`.
- `HELO`, `MAIL FROM`, `RCPT TO`, `DATA`, `RSET`, `NOOP` and `QUIT` are
  served. The null reverse path `<>` is accepted.
- One transaction addresses recipients at one public key. A second
  recipient at a different key is refused.

The sender of every connection is the remote public key that the transport
authenticated: the dmsg session's remote key, or the skynet route's remote
key (`skymail.Host.PeerPK`).

### Recipient checks

For each `RCPT TO` the mailbox replies as follows.

| Condition | Reply |
|-----------|-------|
| The address does not parse as a skymail address | `550 5.1.3` |
| The key is not this mailbox's, and the sender is not in this mailbox's relay-for list | `550 5.7.1` |
| The key is this mailbox's, the whitelist is non-empty, and neither the sender nor (for a mailbox that opens sealed envelopes) the key in `MAIL FROM` is in it | refused as not whitelisted |
| The mailbox holds `MaxTotalSize` or more | `452 4.2.2` |
| Otherwise | `250` |

An empty whitelist accepts every key.

### Delivery checks

After `DATA` the mailbox applies these in order.

1. A message for another key is carried (see Carrying). Only a sealed
   envelope whose recipient key equals every `RCPT TO` key is carried.
   Anything else gets `554 5.6.0` or `554 5.7.1`.
2. A sealed envelope for this mailbox is opened. The signer becomes the
   sender, and the connection's key becomes the relay when it differs. An
   envelope that does not open gets `554 5.7.5`.
3. The sender MUST be allowed by the whitelist.
4. A message larger than `MaxMessageSize` gets `552 5.3.4`.
5. A message whose sender and `Message-ID` were filed in the last
   30 days (`seenTTL`) is accepted and dropped. A message without a
   `Message-ID` is never treated as a repeat.

## Data Formats

### Stored message

The mailbox stores each accepted message verbatim in Maildir format, with
these header lines written above it.

| Line | Present | Meaning |
|------|---------|---------|
| `X-Skymail-Peer: <hex key>` | always, first line | the sender key the transport or the seal authenticated (`skymail.PeerHeader`) |
| `X-Skymail-Relay: <hex key>` | sealed envelope delivered by another visor, second line | the delivering visor (`skymail.RelayHeader`) |
| `Received: from <label>.skynet by <label>.skynet with ESMTP (skymail[ sealed]); <RFC 1123Z date>` | always | trace |

Only the header in those exact positions counts. A header of the same name
that the sender wrote further down is ignored. A message is shown as
verified when its `From` address names the key in `X-Skymail-Peer`.

Folders are `INBOX` (the Maildir root), `Sent` (`.Sent`) and `Outbox`
(`skymail.FolderInbox`, `FolderSent`, `FolderOutbox`).

### Sealed envelope

A sealed envelope carries one message to one recipient through any visor.
Only the recipient can open it, and it names the sender by signature.
Defined in `pkg/skymail/sealed.go`.

It is an RFC 5322 message with headers `MIME-Version: 1.0`,
`Content-Type: application/vnd.skywire.skymail-sealed`
(`skymail.SealedContentType`) and `Content-Transfer-Encoding: base64`. The
body is base64 (RFC 4648 section 4) in lines of 76 characters, of

| Offset | Length | Field |
|--------|--------|-------|
| 0 | 4 | magic `SKM1` |
| 4 | 33 | ephemeral public key |
| 37 | 33 | recipient public key |
| 70 | 12 | nonce |
| 82 | rest | ciphertext |

The ciphertext is ChaCha20-Poly1305 (RFC 8439) with bytes 0 to 69 as
additional data, over

| Length | Field |
|--------|-------|
| 33 | signer public key |
| 65 | signature |
| rest | the message |

- The key is HKDF-SHA256 (RFC 5869) with input ECDH(ephemeral secret key,
  recipient public key), salt ephemeral key followed by recipient key, info
  `skymail sealed envelope v1`, 32 bytes.
- The signature is the signer's secp256k1 signature over
  `skymail sealed envelope v1` followed by the recipient key and
  SHA-256(message). Binding the recipient stops a recipient from resealing
  a signed message to a third party as if it had been sent to them.

A sender MUST seal for a recipient only when it delivers through another
visor, and a carrier MUST deliver a sealed envelope only to a mailbox that
advertises `SKYMAIL-SEALED`.

### Settings file

`settings.json` in the mail directory (`skymail.SettingsFile`,
`skymail.Settings`) holds settings changed at runtime. It overrides the
limits the app was started with. A missing field is unset.

| JSON name | Type | Meaning |
|-----------|------|---------|
| `enable` | bool | whether the visor starts the app |
| `max_message_size` | int64 | bytes, 0 default, negative no limit |
| `max_total_size` | int64 | bytes over every folder, 0 default, negative no limit |
| `max_age` | string or int64 | Go duration text, or nanoseconds, 0 default, negative never |
| `helpers` | array of hex keys | helpers, absent for the default, empty for none |
| `relay_for` | array of hex keys | relay-for list, absent for the default, empty for none |

## Interfaces

### App RPC

The app calls these on its visor's app server (`appserver.RPCIngressGateway`,
`pkg/app/appserver/mail_host.go`). The visor answers them only for the app
named `skymail`, and returns `ErrNoMailHost` to any other.

| Method | Request | Response | Meaning |
|--------|---------|----------|---------|
| `MailSeal` | `MailSealReq{Msg []byte, Rcpt PubKey}` | `[]byte` | seal Msg for Rcpt with the visor's key |
| `MailOpen` | `[]byte` | `MailOpenResp{Msg []byte, Signer PubKey}` | open an envelope sealed to the visor |
| `MailHostInfo` | none | `MailHostInfo{Managed, Hypervisors []PubKey, PortServed bool}` | the visors this visor manages as a hypervisor, its own hypervisors, and whether `serve add 25` holds port 25 |

The app also uses the app server's `Listen`, `Dial` and `Notify` calls like
any app.

### Control surface

The app serves JSON over HTTP (`skymail.Service.Handler`, client
`skymail.Control`). An app in the visor's process publishes the handler to
the launcher (`launcher.RegisterHTTPHandler("skymail", …)`). An app started
with `--addr` also serves it on that TCP address. A request over TCP MUST
carry `X-Skymail-Token` with the contents of `control-token` in the mail
directory, which the app writes with mode 0600 when it binds the address,
or it gets 403.

| Method and path | Request | Response |
|-----------------|---------|----------|
| `GET /status` | none | `skymail.Status` |
| `GET /list?folder=` | none | `[]skymail.Summary`, newest first |
| `GET /read?folder=&id=` | none | `skymail.Rendered`, and marks the message read |
| `GET /raw?folder=&id=` | none | the stored bytes, base64 in JSON |
| `GET /attachment?folder=&id=&n=` | none | `skymail.AttachmentData` |
| `POST /send` | `skymail.Outgoing` | `skymail.SendReply{result, error}` |
| `POST /delete` | `{"folder","id"}` | `null` |
| `POST /retry` | `{"id"}`, empty for every queued message | `null` |
| `POST /whitelist` | array of hex keys | `null` |
| `POST /reload` | none | `null`, after applying `settings.json` |

An error is `{"error": "<text>"}` with status 404 for an unknown message, 400
for a malformed request and 500 otherwise. `POST /send` answers 200 with
`error` set when it fails after reaching recipients, so the per-recipient
reasons in `result` are kept.

### Visor mail API

The visor's `visorapi.Mail` methods (`MailStatus`, `MailList`, `MailRead`,
`MailRaw`, `MailAttachment`, `MailSend`, `MailDelete`, `MailRetry`,
`MailSetWhitelist`, `MailSetSettings`) call the control surface. The CLI
(`skywire cli mail`), the hypervisor and the phone app use them.
`MailSetSettings` writes `settings.json`, then starts or stops the app for
`enable`, or calls `POST /reload`.

## Behaviour

### App lifecycle

The visor adds a `skymail` app entry when the config does not list one. It
starts in process with `--dir <absolute mail dir>` and the limits of the
config's `skymail` section as flags, and it starts automatically unless
`enable` is false in `settings.json` or the config. The default mail
directory is `mail` beside the visor's local path. On start the app asks
`MailHostInfo` and stays down when `PortServed` is set.

### Sending

`Send` groups recipients by key and delivers to each key's port 25 over the
network its suffix names. Recipients at the mailbox's own key are filed
directly. A copy goes to `Sent` when any delivery succeeds.

A recipient that cannot take the message now, because it cannot be reached
or answers 4xx, is queued in the Outbox. A 5xx answer fails that recipient
at once. Queued mail is retried after the delays below, at visor start, and
at once when the recipient connects to this mailbox. When `MaxAge` passes,
the Outbox gives up and files a bounce in the inbox with the original
attached. Mail carried for another visor is dropped without a bounce. The phone apps build the app without the Outbox, so a send to an
unreachable visor fails at once there.

### Helpers and carrying

A mailbox with an Outbox hands each queued message to its helpers as one
sealed envelope per recipient key. A helper that is itself the recipient is
skipped. By default the helpers are the visors this visor manages as a
hypervisor, and a mailbox carries mail from its own hypervisors. Either
list can be set explicitly in `settings.json`.

A carried envelope is queued in the carrier's Outbox and delivered the same
way as its own mail. The sender keeps its copy and keeps retrying. The
recipient's repeat check keeps one copy.

### Limits

| Constant | Value | Identifier |
|----------|-------|------------|
| Default largest message | 10 MiB | `skymail.DefaultMaxMessageSize` |
| Default mailbox size, every folder | 64 MiB | `skymail.DefaultMaxTotalSize` |
| Default age at which mail is deleted | 7 days | `skymail.DefaultMaxAge` |
| Retry delays | 1, 5, 15 minutes, then hourly | `skymail.retryDelays` |
| Repeat window | 30 days | `skymail.seenTTL` |

## Security Considerations

- The sender of every message is the key the transport authenticated, or
  the signer of a sealed envelope. A forged `X-Skymail-Peer` or
  `X-Skymail-Relay` in a message is ignored by position.
- The visor's secret key never reaches the app. Any app can already send
  mail as its visor, because the transport authenticates the visor's key,
  so `MailSeal` grants nothing new. `MailOpen` reads mail sealed to the
  visor, so it is served to the `skymail` app only.
- A carrier sees the recipient key, the envelope size and the time of a
  sealed message, but not the sender or the content.
- The mailbox is open to every key by default. The size and age limits
  bound the storage that other visors can use.
- The control surface over TCP is as private as the mail directory, since
  its token is stored there.

## References

- RFC 2119, RFC 8174, key words
- RFC 1035, domain names
- RFC 1870, SMTP SIZE extension
- RFC 4648, base64
- RFC 5321, SMTP
- RFC 5322, Internet Message Format
- RFC 5869, HKDF
- RFC 8439, ChaCha20-Poly1305
- [App Server](12-App_Server.md)
- [Dmsg](05-Messaging_System.md)
