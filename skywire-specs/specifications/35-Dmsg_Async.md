# Dmsg Async

| Status   | Implemented                                                            |
|----------|------------------------------------------------------------------------|
| Revision | 2026-11-02, skywire 4ac16bbc9                                          |
| Code     | `pkg/dmsg/dmsgasync`, `pkg/dmsg/discovery/api/inbox.go`, `pkg/visor/dmsg_async.go`, `pkg/visor/skychat_async.go`, `pkg/app/appserver/chat_host.go` |

## Abstract

Dmsg async is store and forward for dmsg keys that are offline. A recipient
chooses inbox hosts, signs a record naming them and registers with each. A
sender that cannot reach the recipient deposits a payload on one of those
hosts, and the recipient fetches it when it comes back. Dmsg servers only
relay the streams and store nothing.

## Overview

Three parties take part.

- The **recipient** R signs an inbox record, registers it with each of its
  hosts and MAY publish it to dmsg discovery. It fetches from its hosts.
- An **inbox host** H holds messages for the keys registered with it. By
  default a visor that runs a hypervisor is a host, and holds for itself and
  the visors it manages.
- A **sender** S finds R's record, deposits on R's hosts in order until one
  takes the payload, and is told the message id.

Consumers name a dmsg-async port, which labels the message and is not a
listener. Skymail parks mail on port 25 ([Skymail](34-Skymail.md)) and
skychat parks 1:1 messages on port 1. Both seal the payload for R and sign
it with S's key, so the host can read neither and R does not have to trust
the host's `from`.

## Addressing

A host serves the protocol on dmsg port 85 (`skyenv.DmsgAsyncPort`,
`dmsgasync.Port`). Every operation is one dmsg stream. Dmsg streams are
Noise KK sessions, so the host knows the remote key of each stream, and
every operation is made as that key.

Dmsg discovery serves records over dmsg HTTP at
`dmsg://<discovery-pk>:80/dmsg-discovery/inbox/{pk}`.

## Data Formats

### Frames

Every message on a stream is a frame: a 4 byte big endian length, then that
many bytes. A control frame holds one JSON object of at most 64 KiB. A
payload frame holds raw bytes of at most 1 MiB (`dmsgasync.MaxMessageLimit`).

### Record

`dmsgasync.Record`

| JSON | Type | Required | Meaning |
|------|------|----------|---------|
| `pk` | hex public key | yes | R's key |
| `hosts` | list of hex public keys | yes | where to deposit, in order, at most 4 (`dmsgasync.MaxRecordHosts`) |
| `max_message` | int | no | largest payload R takes, 0 for the host's limit |
| `seq` | uint64 | yes | a newer record has a larger value |
| `expires` | RFC 3339 time | yes | at most 30 days ahead (`dmsgasync.MaxRecordLifetime`) |
| `sig` | hex signature | yes | R's signature |

The signature covers the JSON encoding of an object with exactly the fields
`pk`, `hosts`, `max_message`, `seq` and `expires` in that order, where
`expires` is Unix seconds. A record is valid when the signature verifies
against `pk`, the bounds above hold, it has not expired and it expires no more
than 30 days and one hour ahead.

### Policy

`dmsgasync.Policy`, sent only to hosts.

| JSON | Type | Meaning |
|------|------|---------|
| `allow` | list of hex public keys | senders that may deposit, empty for every key |
| `deny` | list of hex public keys | senders that may never deposit |
| `max_per_sender` | int | messages one sender may have waiting, 0 for 20 |

### Request

`dmsgasync.request`

| JSON | Type | Used by | Meaning |
|------|------|---------|---------|
| `op` | string | all | `register`, `unregister`, `record`, `deposit`, `fetch` or `ack` |
| `record` | Record | register | the record to register |
| `policy` | Policy | register | the sender policy |
| `pk` | hex public key | record | whose record to return |
| `to` | hex public key | deposit | the recipient |
| `port` | uint16 | deposit | the consumer port, not 0 |
| `size` | int | deposit | payload length in bytes |
| `ref` | 32 hex characters | deposit | names the message across retries, optional |
| `ports` | list of uint16 | fetch | ports to fetch |
| `ids` | list of strings | ack | messages to remove |

### Reply

`dmsgasync.reply`

| JSON | Type | Meaning |
|------|------|---------|
| `ok` | bool | the operation succeeded |
| `code` | string | the failure, see below |
| `error` | string | a description of the failure |
| `id` | string | the id of a deposited message |
| `record` | Record | the record a `record` operation asked for |
| `messages` | list of Header | the messages a `fetch` returns |
| `more` | bool | more messages wait after this fetch |
| `deleted` | int | messages an `ack` removed |
| `dropped` | int | messages an `unregister` removed |

| Code | Meaning |
|------|---------|
| `not_hosted` | the host does not hold for this key |
| `not_allowed` | the recipient's policy refuses this sender |
| `too_big` | the payload or the record's `max_message` is over the limit |
| `recipient_full` | the recipient has too much waiting on this host |
| `sender_full` | this sender has too much waiting for the recipient |
| `store_full` | the host's store is full |
| `bad_record` | the record is invalid, is not the asking key's, does not name this host, or is older than the registered one |
| `bad_request` | the request is malformed |
| `internal` | any other failure |

### Header

`dmsgasync.Header`

| JSON | Type | Meaning |
|------|------|---------|
| `id` | string | 20 digit Unix nanoseconds, a dot, 16 hex characters |
| `from` | hex public key | the key whose stream deposited it |
| `port` | uint16 | the consumer port |
| `size` | int | payload length |
| `deposited` | RFC 3339 time | when the host took it |

## Interfaces

### Host operations

Each operation opens a stream to the host, writes one request frame and reads
one reply frame, except where noted. The host closes the stream after the
operation and allows it 60 seconds.

- **register** stores R's record and policy. The record MUST be the asking
  key's, valid, and name this host, and the host MUST accept the key. A record
  with a smaller `seq` than the registered one is refused. A `max_message`
  above the host's limit is refused with `too_big`.
- **unregister** removes the asking key's registration and every message
  held for it.
- **record** returns the registered record for `pk` while it has not expired.
  Any key may ask.
- **deposit** checks the request against R's registration and policy, then
  replies `ok` with no id. The sender then writes one payload frame of exactly
  `size` bytes, and the host replies with the id. A deposit with the same
  sender and `ref` as a message still held returns that message's id and
  stores nothing.
- **fetch** replies with the headers of the asking key's oldest messages on
  `ports`, ordered by id, then writes one payload frame per header in the same
  order. A fetch returns at most 32 messages and 1 MiB of payload, but always
  at least one message. Nothing is removed.
- **ack** removes the asking key's messages that `ids` name. An id that is not
  the asking key's is ignored.

### Dmsg discovery

| Method | Path | Body | Status |
|--------|------|------|--------|
| `GET` | `/dmsg-discovery/inbox/{pk}` | none | 200 with the stored Record, 400 for a bad key, 404 when none is stored |
| `POST` | `/dmsg-discovery/inbox/{pk}` | Record | 200 stored, 400 for a bad key or body, 422 when the record is not for `pk`, is invalid or has a smaller `seq` than the stored one |

The record's signature is the only authorization. Discovery keeps it until it
expires.

### App RPC

The visor serves these on its app server (`appserver.RPCIngressGateway`,
`pkg/app/appserver/chat_host.go`) only to the app named `skychat`, and
returns `ErrNoChatHost` to any other. Skymail's calls are in
[Skymail](34-Skymail.md).

| Method | Request | Response | Meaning |
|--------|---------|----------|---------|
| `ChatPark` | `ChatParkReq{Rcpt PubKey, Msg []byte, Ref string}` | `PubKey` | seal Msg for Rcpt with the visor's key, deposit it on port 1, return the host |
| `ChatUnpark` | none | `[]ParkedChat{ID string, Host, From PubKey, Msg []byte, At time}` | fetch port 1 without removing, open each, and return the signer as From |
| `ChatAck` | `[]ParkedRef{Host PubKey, ID string}` | none | remove the named messages |

`ChatUnpark` removes a message that does not open and leaves it out of the
response.

### Visor RPC and CLI

`DmsgAsyncStatus`, `DmsgAsyncSend`, `DmsgAsyncTake` and `DmsgAsyncSync`
(`visorapi`) back `skywire cli async status`, `send`, `take` and `sync`.
`DmsgAsyncTake` refuses a port the visor handles itself.

## Behaviour

### Recipient

R's candidate hosts are `dmsg_async.inbox.hosts`, or its hypervisors when that
is empty, without its own key, at most 4. R signs a record naming them all,
with `seq` set to the current Unix nanoseconds and `expires` 30 days ahead,
and registers it with each. When some refuse, R signs a second record naming
only the hosts that accepted, with `seq` one higher, and registers that, so a
record never names a host that refused. R's policy allows the keys in
`dmsg_async.inbox.allow`, or every key when it is empty.

R publishes its record to dmsg discovery when its hosts change and once a
day, unless `dmsg_async.inbox.publish` is false. R registers again once a
day, when the record has less than 7 days left, and hourly while a host
refuses. A visor does all of this every 10 minutes and when asked through
`DmsgAsyncSync`, and fetches the ports it handles itself at the same times.

### Host

A visor runs a host when it runs a hypervisor, or as `dmsg_async.host.enable`
says. The host accepts its own key, the visors connected to its hypervisor,
and the keys in `dmsg_async.host.hosted`. Acceptance is checked only at
registration, so a managed visor that goes offline stays registered.

A disk host keeps headers in memory, each payload in a file under
`<dir>/messages/<recipient hex>/<id>`, and the registrations in
`<dir>/registrations.json`, where `dir` is `dmsg_async.host.dir` or
`dmsg-async` beside the visor's local path. It loads both at start. A memory
host keeps everything in memory. Every 10 minutes the host removes messages
older than the TTL and registrations that have expired.

### Sender

A visor finds R's record from R's registration on its own host, then from a
record it fetched within the hour, then from dmsg discovery. It deposits on
the record's hosts in order until one takes the payload. A `not_allowed` or
`too_big` answer stops the attempt, since every host applies the same policy
and size. A deposit for a host that is the sending visor itself is served in
process.

### Skychat

A 1:1 message that no network delivers is parked when it has an id. So is a
message whose requested ack does not arrive in time, since a write to a dead
conn succeeds. The ref is the first 16 bytes of
`sha256(id + "/" + the recipient's 33 key bytes)` as hex. The recipient fetches port 1 when
skychat starts and every 2 minutes, handles each message like a frame from a
live conn from its signer, and removes it from the host. A message whose id is
already in the conversation's history is only removed.

### Limits

| Constant | Value | Identifier |
|----------|-------|------------|
| Largest message, default | 64 KiB | `dmsgasync.DefaultMaxMessage` |
| Largest message, any host | 1 MiB | `dmsgasync.MaxMessageLimit` |
| Messages per recipient | 200 | `dmsgasync.DefaultMaxPerRecipient` |
| Bytes per recipient | 8 MiB | `dmsgasync.DefaultMaxRecipientBytes` |
| Messages per sender per recipient | 20 | `dmsgasync.DefaultMaxPerSender` |
| Store size | 256 MiB | `dmsgasync.DefaultMaxTotal` |
| Message TTL | 7 days | `dmsgasync.DefaultTTL` |
| Hosts per record | 4 | `dmsgasync.MaxRecordHosts` |
| Record lifetime | 30 days | `dmsgasync.MaxRecordLifetime` |
| Visor cycle | 10 minutes | `visor.asyncCycle` |
| Skychat fetch | 2 minutes | `dm.unparkInterval` |

A host's limits come from `dmsg_async.host` and take the defaults for a zero
value.

## Security Considerations

The host knows the sender and recipient keys of every operation from the dmsg
stream, so a deposit cannot claim another sender and only R can fetch, ack or
unregister its messages. The `from` in a header is that stream key.

A host sees the sender, recipient, port, size and time of each message. It
cannot read or forge a sealed payload. It can drop, delay or replay messages,
and consumers drop repeats by their own message ids.

A published record shows R's hosts, which are usually R's own other visors. R
can keep them private by turning publishing off and giving its record to
contacts directly.

Each host bounds what one sender, one recipient and the whole store can hold.
Only senders R's policy allows can use R's share.

## References

- [Dmsg](05-Messaging_System.md)
- [Skymail](34-Skymail.md)
- `docs/design/dmsg-async.md`
