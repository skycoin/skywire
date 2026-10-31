# dmsg-async: messages for clients that are offline

dmsg-async lets a dmsg client leave a small message on a dmsg server for a
client that is offline, and the recipient fetch it when it comes back. It is
the store-and-forward part of RFC #3005, over the TCP and yamux sessions dmsg
already has. It does not need the UDP transport or the peer overlay that RFC
also proposes.

It is **off on every server** unless the operator turns it on. A server that
has it on says so in its discovery entry, with its limits. A deployment whose
servers leave it off behaves exactly as before.

## Problem

A dmsg stream needs both ends online at the same time. A client that wants to
reach one that is offline has to keep trying itself, which fails when the
sender goes offline too: a browser tab that closes, a laptop that sleeps.
Skymail's Outbox and helper visors cover part of that. They still need the
sender, or a visor the sender owns, to be online when the recipient is.

## What it is not

- **Not a mailbox.** Messages are small (64 KiB by default, 1 MiB at most) and
  short-lived (72 hours by default). Large mail stays with the sender and its
  helpers.
- **Not on by default.** A public deployment holds nothing unless its operators
  choose to.
- **Not reliable delivery.** A server may expire, refuse or lose a message. The
  sender keeps its own copy and keeps trying. dmsg-async is one more path, not
  the only one.
- **Not private by itself.** The server stores the payload as given. A sender
  that needs secrecy seals it for the recipient first, as skymail does.

## Model

```
sender ──deposit(to, port, payload)──▶ async server A ◀──fetch / ack── recipient
       └─deposit──────────────────────▶ async server B ◀──fetch / ack──┘
```

Rendezvous hashing over the async servers in discovery picks which servers
hold a recipient's messages, so the sender and the recipient choose the same
ones without talking to each other.

## Protocol

A client opens a stream to the **server itself**, as it does to look up its
public IP. The first object is the usual signed `StreamRequest`, with
`DstAddr.PK` set to the server's key and the new `Async` flag set. The
session's Noise channel already authenticates and encrypts the stream, so the
server knows which client is asking and nothing else is needed.

After the request, the stream carries frames: a 4-byte big-endian length and
then the bytes. A control frame is JSON, at most 64 KiB. A payload frame is
raw bytes, at most the server's message limit.

| Operation | Client sends | Server answers |
|---|---|---|
| deposit | `{"op":"deposit","to":<pk>,"port":<n>,"size":<n>}`, then the payload frame | `{"ok":true,"id":...}` or an error |
| fetch | `{"op":"fetch","ports":[...]}` | `{"ok":true,"messages":[headers],"more":bool}`, then one payload frame per header |
| ack (after fetch) | `{"op":"ack","ids":[...]}` | `{"ok":true,"deleted":n}` |

- **One operation per stream.** The stream closes after the reply.
- **Sender is from the session.** A deposit records the session's own PK as the
  sender; the request cannot name another.
- **Fetch is for the asking PK.** A client only ever fetches its own messages,
  and only on the ports it lists. It lists the ports it has a handler for, so
  messages for a port it does not serve never block the rest.
- **Nothing is deleted without an ack.** A client acks only the messages its
  handlers took. The rest wait for the next fetch or expire.
- **Batches.** A fetch returns up to 32 messages or 1 MiB, oldest first, and
  `more` says if others wait.
- **Relayed requests are refused.** A request whose source is not the session's
  own key would let the relaying session spend another key's quota or fetch
  its mail.

Errors come back as a code, which the client maps to an error value:
`disabled`, `too_big`, `recipient_full`, `sender_limit`, `store_full`,
`bad_request`, `relayed`, `internal`.

### Compatibility

- An **older server** ignores the `Async` flag. It treats the request as one for
  its own key, which it refuses, so the client reports the server as not
  holding messages. Clients only use servers that advertise async, so this is
  a fallback.
- An **older client** never sets the flag and is unaffected. A newer client's
  ordinary requests carry the field in gob's type description, with no value
  when it is false. Gob ignores fields the receiver does not know, so older
  servers decode them as before.
- The discovery field `server.async` is `omitempty` and nil on every server
  without dmsg-async, so those entries are unchanged. A server **with** it
  needs a discovery that knows the field: an older discovery drops unknown
  fields, re-encodes the entry to check its signature, and rejects it. Update
  the discovery before turning dmsg-async on.

## Placement

`AsyncServersFor(entries, dst, k)` scores each async server by
`SHA-256(server PK | dst PK)` and takes the `k` highest (rendezvous or
highest-random-weight hashing, Thaler and Ravishankar 1996). `k` is 2 by
default.

- **No coordination.** Everyone with the same server list picks the same
  servers for a recipient, whatever the list's order.
- **Stable.** A server joining or leaving moves only the recipients it wins or
  held.
- **Two copies.** With two servers a message survives one of them being down,
  at the cost of the recipient seeing it twice. Handlers drop repeats; skymail
  does it by sender and Message-ID.

A recipient fetches from its own top `k`, plus every async server it already
has a session to. That covers a sender whose view of the server list was
briefly different.

## Server limits

A zero field takes the default. A negative one removes the bound, which only
a private deployment that trusts its clients should do. The message size is
the exception: a fetch must carry at least one whole message, so it is capped
at 1 MiB.

| Limit | Config key | Default |
|---|---|---|
| Largest message | `max_message_size` | 64 KiB (at most 1 MiB) |
| Messages per recipient | `max_per_recipient` | 100 |
| Bytes per recipient | `max_recipient_bytes` | 1 MiB |
| Deposits per sender per hour | `max_sender_per_hour` | 60 |
| Whole store | `max_total_size` | 64 MiB |
| Time to live | `ttl` | 72h |

Expired messages are dropped every 10 minutes. With `dir` set, each message is
a file `<dir>/<recipient hex>/<id>`, written whole and renamed into place, so
messages survive a restart. At start the server drops expired, half-written
and unreadable files and rebuilds its quota counts. Without `dir`, messages
live in memory and a restart loses them.

## Security and privacy

- **Authenticated sender.** The session authenticates the sender, so a server
  cannot be told a message came from someone else. The recipient still learns
  the sender only from the server. Skymail does not rely on that: the payload
  is signed by the sender and checked by the recipient.
- **What the server sees.** It sees the sender PK, the recipient PK, the port,
  the size and the time. It does not see the payload if the sender sealed it.
- **Abuse.** It is bounded per sender, per recipient and in total. A sender can
  only fill a recipient's share on one server up to the per-recipient limit,
  which is also why that limit is low. An operator who expects abuse can lower
  the sender rate or leave dmsg-async off.
- **Dishonest servers.** A server may drop, delay, refuse or replay messages. It
  cannot forge a sealed payload or read one. Replays reach the handler again;
  skymail files a repeat once.
- **Linkability.** Fetching from the same two servers on every start shows them
  that this PK is online. That is no more than discovery already shows.

## Configuration

In the dmsg server's config (standalone, or the dmsg-server block of
`services.json`):

```json
"async": {
  "enable": true,
  "dir": "async",
  "max_message_size": 65536,
  "max_per_recipient": 100,
  "max_recipient_bytes": 1048576,
  "max_sender_per_hour": 60,
  "max_total_size": 67108864,
  "ttl": "72h"
}
```

`dir` is relative to the config file's directory. Only `enable` is needed; the
rest take the defaults above. `skywire cli mdisc servers` shows which servers
hold messages and their limits.

## Client API (`pkg/dmsg/dmsg`)

- `Config.Async` or `Client.EnableAsync(AsyncClientConfig{})` turns fetching
  on. The client fetches at start, on each new server session, every 10
  minutes, and when `FetchAsyncSoon` asks.
- `HandleAsync(port, handler)` sets the handler for a port. A nil error from the
  handler acks the message; an error keeps it on the server.
- `Deposit(ctx, dst, port, payload)` leaves a payload with the recipient's
  servers and returns the ones that took it. It fails only when none did.
  Depositing does not need fetching turned on.
- `FetchAsync(ctx)` fetches now and reports how many messages were handled.
- `AsyncServersFor(entries, dst, k)` is the placement, for callers that want to
  show it.

## Use in skymail

When the Outbox queues mail for a visor that cannot take it, it also seals the
message for that visor and deposits it on port 25. The recipient's visor
fetches it when it comes online and files it through the same checks as mail
over SMTP: the signer must be allowed, it must fit, and a repeat is answered
as delivered. The server that held it shows as the relay. This reaches a
recipient even when the sender and all its helpers are offline by then.

- Mail too big for every chosen server is not offered again.
- Parking backs off while no server in discovery holds messages, so on a
  deployment without dmsg-async it costs one discovery lookup per backoff.
- A fetched envelope that can never be filed (not sealed for this visor,
  broken, from a refused sender) is acked and dropped. One that may be filed
  later, such as into a full mailbox, stays on the server.

The desktop and wasm visors use it. The phone apps do not yet, as with the
rest of offline mail.

## Limitations and next steps

- **No push.** A recipient learns of a message only when it fetches. The fetch
  on each new session covers coming online, which is the case that matters.
- **No receipt.** The sender does not learn that the recipient fetched it. It
  keeps its own copy and its normal retries, and the recipient drops the
  repeat.
- **Placement drift.** Two clients with different server lists may disagree for
  a while. The recipient's fetch from every server it is connected to narrows
  that, and the sender's own retries cover the rest.
- **Per-server quotas.** Limits are per server, not per deployment.

## Testing

- `pkg/dmsg/dmsg/async_store_test.go` covers each limit, ordering, the port
  filter, ack ownership, expiry, persistence across a restart (including
  corrupt and half-written files), batching, frames and placement.
- `pkg/dmsg/dmsgtest/async_test.go` runs real dmsg servers and clients: the
  advertised limits, deposit then fetch when the recipient appears, a handler
  error keeping a message, port filtering, size and quota errors reaching the
  sender, two replicas, and no async server at all.
- `pkg/skymail/parked_test.go` and `pkg/visor/skymail_test.go` cover parking
  mail, fetching it into the mailbox, repeats, and the refusal cases. The visor
  test does it end to end over real dmsg.
