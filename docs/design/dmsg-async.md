# dmsg-async: store and forward for offline keys

Status: proposed. Nothing here is implemented yet. Once it is, the behaviour
moves into a `skywire-specs` document and this note records the reasoning.

## Problem

A dmsg stream needs both ends online at once. A client that wants to reach an
offline key has to keep trying on its own, and that fails when the sender
goes offline too: a browser tab that closes, a phone that sleeps, a laptop
that is shut.

Skymail covers part of this with its Outbox and helpers. The sender's own
visors carry queued mail and retry. That still needs some visor on the
sender's side to be online when the recipient is. Skychat 1:1 messages, file
offers and group join requests have nothing at all.

RFC #3005 asked for store and forward in dmsg. #5946 implemented it on the
dmsg servers and was closed, for three reasons.

1. Every message sat in the memory of shared deployment servers, even with a
   storage directory set.
2. Anyone could deposit for any key. That got around the recipient's
   whitelist, because the server took the message whether or not the
   recipient would.
3. The servers were picked by hashing the recipient's key over every server
   that offered the feature. The recipient never chose who held its mail.

This design keeps the parts of #5946 that worked (the framed protocol, the
fetch and ack model, the limits and the client API) and changes who holds the
messages and who decides what they accept.

## Model

```
                 deposit(R, port, sealed payload)
   sender S ───────────────────────────────────────▶ inbox host H
      │                                                  ▲   │
      │ look up R's inbox record                         │   │ fetch / ack
      ▼                                         register │   ▼
   contacts cache, then dmsg discovery ◀──── post ── recipient R
```

- **Recipient R** chooses one or more **inbox hosts**. It signs an **inbox
  record** naming them, registers with each host, and fetches from them when
  it comes online.
- **Inbox host H** is an ordinary dmsg client that serves the async protocol
  on dmsg port 85. It is normally a visor R owns that stays online, such as
  its hypervisor or a home server. It holds messages only for keys that
  registered with it, and only from senders their policy allows.
- **Sender S** finds R's inbox record, seals the payload for R and deposits it
  with R's hosts when R itself cannot be reached.

The dmsg servers only relay the streams, as they do for every other port.
They store nothing. Store and forward becomes a property of the dmsg address
space, but the storage lives on hardware the recipient controls.

## What it is not

- **Not a mailbox.** Payloads are small (64 KiB by default) and short-lived
  (7 days by default). Large mail stays with the sender and its helpers.
- **Not reliable delivery.** A host may be down, full or slow. The sender
  keeps its copy and its own retries. dmsg-async is one more path.
- **Not anonymous.** The host sees who sends to whom. It does not see sealed
  payloads.
- **Not open on the deployment.** A deployment may run a host under a service
  key for users who have no second visor. It uses the disk store and holds
  only for keys that registered with it, exactly as any other host does.

## Inbox record

The recipient signs the record with its visor key. It says where to deposit,
not who may deposit, so it reveals R's hosts but not R's contacts.

| Field | Type | Meaning |
|-------|------|---------|
| `pk` | hex key | the recipient |
| `hosts` | array of hex keys | the inbox hosts, in the order senders try them, at most 4 |
| `max_message` | integer | the largest payload every host accepts, in bytes |
| `seq` | integer | increases on every change, so an older record never replaces a newer one |
| `expires` | RFC 3339 time | at most 30 days after signing |
| `sig` | hex | signature over the canonical JSON of the other fields |

R re-signs the record when its hosts change and before it expires. A record
with an empty `hosts` list withdraws dmsg-async for R.

### Where senders find it

1. **Contacts.** A sender keeps the newest record it has seen for each
   contact. A sender R allows is usually one R talks to, so this covers most
   deposits without a lookup.
2. **dmsg discovery.** R posts the record to a new endpoint,
   `POST /dmsg-discovery/inbox/{pk}`, and senders read it with
   `GET /dmsg-discovery/inbox/{pk}`. Discovery checks the signature and the
   sequence and keeps the record until `expires`. This is separate from the
   client entry because a client entry expires 60 minutes after its last
   registration, which is exactly when R is offline. A separate endpoint also
   leaves the entry format and its signature check untouched.
3. **The host.** A sender that already knows one host can ask it for R's
   current record.

A recipient MAY skip step 2 to keep its hosts private. Only senders that
already have its record can then deposit.

## Registration and policy

R registers with each host over the async protocol. The registration carries
the signed inbox record and R's **sender policy**, which only the host sees.

| Policy field | Meaning |
|--------------|---------|
| `allow` | sender keys that may deposit. Empty means any key |
| `deny` | sender keys that may never deposit |
| `max_per_sender` | messages one sender may have waiting, default 20 |

The host accepts a registration only when both hold.

1. **The host agrees to hold for R.** R is the host's own key, a visor it
   manages as a hypervisor, or a key in its `hosted` list. A host never holds
   for a key it was not configured to accept.
2. **R names the host.** The record is valid, unexpired, signed by R and lists
   the host.

Skymail sets `allow` from its own whitelist, so a key that the mailbox would
refuse over SMTP cannot park mail for it either. Other consumers that need a
narrower policy than skymail's use their own port filter on fetch.

## Protocol

The host serves dmsg port 85 (`skyenv.DmsgAsyncPort`). Every dmsg stream
runs a Noise KK handshake between the two client keys
(`Stream.prepareFields` in `pkg/dmsg/dmsg/stream.go`), so the host knows
which key opened the stream whatever server or relay carried it, and the
request needs no signature of its own.

The stream carries frames: a 4-byte big-endian length and then the bytes. A
control frame is JSON, at most 64 KiB. A payload frame is raw bytes, at most
`max_message`. One operation runs per stream, and the stream closes after the
reply.

| Operation | Who | Request | Reply |
|-----------|-----|---------|-------|
| `register` | R | `{"op":"register","record":{...},"policy":{...}}` | `{"ok":true}` or an error |
| `unregister` | R | `{"op":"unregister"}` | `{"ok":true,"dropped":n}` |
| `record` | anyone | `{"op":"record","pk":<hex>}` | `{"ok":true,"record":{...}}` |
| `deposit` | S | `{"op":"deposit","to":<hex>,"port":n,"size":n,"ref":<hex>}`, then the payload frame | `{"ok":true,"id":<hex>}` or an error |
| `fetch` | R | `{"op":"fetch","ports":[n,...]}` | `{"ok":true,"messages":[header,...],"more":bool}`, then one payload frame per header |
| `ack` | R | `{"op":"ack","ids":[<hex>,...]}` | `{"ok":true,"deleted":n}` |

- **The sender comes from the session.** A deposit records the stream's
  remote key as the sender. The request cannot name another.
- **`ref` makes a deposit idempotent.** It is 16 bytes the sender picks per
  message. A second deposit with the same sender, recipient and `ref` returns
  the first `id` and stores nothing, so a sender that retries does not fill
  the recipient's quota with copies.
- **Fetch and ack are for the asking key only.** R fetches its own messages
  on the ports it lists, oldest first, up to 32 messages or 1 MiB per reply.
- **Nothing is deleted without an ack.** R acks only what its handlers took.
  A handler error leaves the message for the next fetch or until it expires.

A header is `{"id","from","port","size","deposited"}`. Errors come back as
`{"ok":false,"code":...}` with one of `not_hosted`, `not_allowed`, `too_big`,
`recipient_full`, `sender_full`, `store_full`, `bad_record`, `bad_request`,
`internal`.

## Host storage and limits

The host has two stores, chosen by `store`.

- **`disk`**, the default. Each message is a file
  `<dir>/<recipient hex>/<id>`, written whole and renamed into place. The
  host keeps only the headers in memory and reads a payload from disk when a
  fetch sends it. At start it drops expired, half-written and unreadable
  files and rebuilds its counts. A deployment host and any host where memory
  is tight use this store.
- **`memory`**. Payloads are held in memory and a restart loses them. It
  suits a host with memory to spare and no disk to write to, and tests.
  `max_total_size` bounds it the same way.

Expired messages are removed every 10 minutes in both.

| Limit | Setting | Default |
|-------|---------|---------|
| Largest payload | `max_message` | 64 KiB, at most 1 MiB |
| Messages per recipient | `max_per_recipient` | 200 |
| Bytes per recipient | `max_recipient_bytes` | 8 MiB |
| Messages per sender per recipient | the recipient's `max_per_sender` | 20 |
| Whole store | `max_total_size` | 256 MiB |
| Time to live | `ttl` | 7 days |

A zero value takes the default. The host's limits bound what a registration
may ask for. A recipient's `max_message` above the host's own is refused.

## Recipient behaviour

- R fetches from each host at start, whenever it opens a new dmsg session to
  a host, every 10 minutes while online, and when a consumer asks.
- R re-registers with each host whenever its record or policy changes and at
  least once a day, so a host that lost its state recovers.
- A host is usually the recipient's own visor, so it is often online when R
  is. The fetch on each new session is what delivers mail after R comes back.

## Sender behaviour

A consumer calls `Send(ctx, R, port, payload)`.

1. Try a direct dmsg stream to R on the port, as today.
2. If R cannot be reached, find R's inbox record. With none, the send fails
   as it does today and the consumer's own retries apply.
3. Seal the payload for R unless the consumer sealed it already.
4. Deposit on R's hosts in order until one accepts. Depositing on a second
   host is optional and costs a duplicate, which the recipient's consumer
   drops.

Sealing uses the skymail envelope (`skymail.Seal` in `pkg/skymail/sealed.go`,
or a `Sealer` in an app that has no secret key): the payload is encrypted to
R's key and signed by S. The recipient's consumer checks the
signature, so it never has to trust the host's `from` header.

## Consumers

| Consumer | Port | Use |
|----------|------|-----|
| Skymail | 25 | the Outbox deposits queued mail for R once it is past the first retry, and R files fetched mail through the same delivery checks as SMTP |
| Skychat 1:1 | 1 | a message to an offline contact is deposited, and fetched messages enter history as if received live |
| Group join requests and admissions | 8060 | a request waits for an offline admin, and the admission, which carries the group key, waits for a joiner who went offline |
| Contact and pairing requests | 1 | a request waits for the other visor instead of failing |
| Missed call and file offer notices | 62, 64 | a short notice waits. The call or the file itself still needs both ends online |
| Hypervisor to managed visor | 46 | a config change or update request waits for a managed visor that is offline |

Every consumer sends small messages that only need to arrive eventually.
Large or live traffic (file contents, voice, VPN, proxy) stays on direct
streams.

The first implementation is the library, the host, and skymail and skychat
1:1 as its first consumers. The others follow.

## Configuration

In the visor config, with live changes saved in `dmsg-async/settings.json`
beside the local path so a wasm visor keeps them across a reload.

```json
"dmsg_async": {
  "host": {
    "enable": true,
    "store": "disk",
    "dir": "dmsg-async",
    "hosted": [],
    "max_message": 65536,
    "max_total_size": 268435456,
    "ttl": "168h"
  },
  "inbox": {
    "hosts": [],
    "publish": true
  }
}
```

- **`host.enable`** is off by default except on a visor that manages other
  visors as a hypervisor, where it is on and hosts those visors. That is the
  case skymail already covers with carrying, and the wasm tab's hypervisor is
  the natural inbox for the tab.
- **`inbox.hosts`** defaults to this visor's hypervisors when it has any, and
  is otherwise empty, which turns dmsg-async off for this key.
- **`inbox.publish`** posts the record to dmsg discovery.

The CLI gets `skywire cli async` with `status`, `inbox`, `hosts`, `register`
and `fetch`, and the same settings as `skywire cli mail settings`.

## Compatibility

- **No change to dmsg sessions, stream requests or discovery entries.** The
  protocol runs on a new port over ordinary streams, so older servers relay it
  and older clients never see it.
- **Older dmsg discovery** returns 404 for the inbox endpoint. Senders then
  rely on contacts and hosts, and recipients post again later.
- **Older visors** do not serve port 85. A recipient lists only hosts that
  answered its `register`.

## Security considerations

- **Whitelist.** The host applies the recipient's sender policy to the
  session-authenticated sender key. A key the recipient refuses cannot
  deposit.
- **Who holds what.** A host holds only for keys it was configured to accept
  and that named it in a signed record. It cannot be made to hold for an
  arbitrary key.
- **What a host sees.** Sender key, recipient key, port, size and time. Not a
  sealed payload. A recipient that uses a host it does not own trusts that
  host with this metadata.
- **What discovery shows.** A published record links R to its hosts, usually
  R's own other visors. A recipient that wants to hide this sets
  `inbox.publish` to false and gives its record to contacts directly.
- **Dishonest hosts.** A host may drop, delay or replay messages. It cannot
  forge or read a sealed payload. Consumers drop repeats, skymail by sender
  and `Message-ID`.
- **Abuse.** Bounded per sender, per recipient and in total, on each host.
  Only senders the recipient allows can use a recipient's share at all.
- **Stale records.** `seq` stops an old record from replacing a newer one, and
  `expires` bounds how long a withdrawn host keeps receiving deposits.

## Implementation plan

1. `pkg/dmsg/dmsgasync`: frames, records and their signatures, the disk store
   with its limits, the host handler, and the client (`Register`, `Fetch`,
   `Deposit`, `Send`, `Handle(port, fn)`). Tests with real dmsg servers and
   clients from `pkg/dmsg/dmsgtest`.
2. dmsg discovery: the inbox record endpoint and store, with the record's
   signature and sequence checks.
3. Visor: the host and the inbox client, configuration, settings file, CLI,
   and `visor state` fields.
4. Skymail: deposit from the Outbox and filing of fetched mail.
5. Skychat 1:1: deposit when the contact is offline, and fetched messages
   into history.
6. A spec in `skywire-specs`, then the other consumers.

Each step is tested against live visors before it merges: a recipient behind
its hypervisor as host, the :8443 wasm tab as a recipient, and a sender that
goes offline before the recipient comes back.

## Open questions

- Which keys may register with a deployment host, and with what limits. The
  host itself uses the disk store.
- Should a host also push to the recipient when it sees R come online,
  instead of waiting for R's fetch? Fetching on each new session covers the
  common case, so push is left out for now.

## References

- RFC #3005, dmsg over UDP, async store and forward.
- #5946, dmsg-async on dmsg servers (closed). Its protocol, limits and client
  API are the starting point here.
- `skywire-specs/specifications/34-Skymail.md`, the Outbox, helpers, carrying
  and sealed envelopes.
- D. Thaler and C. Ravishankar, "Using name-based mappings to increase hit
  rates", 1998. #5946 used this for server placement, which this design
  replaces with recipient choice.
