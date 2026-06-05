# Standalone CXO node — `skywire cxo`

`skywire cxo` bundles the low-level [CXO](https://github.com/skycoin/cxo) object-
replication node (`pkg/cxo/node`) as a standalone daemon plus an RPC CLI. It is
the raw, **multi-feed** content-exchange node: it stores and replicates signed
object trees ("roots") for any number of **feeds**, each identified by a public
key, over TCP and/or UDP.

It is distinct from the higher-level **treestore** layer (`pkg/cxo/treestore`)
that backs the app-level CXO utilities (e.g. CXO-backed skychat, the transport-
discovery aggregator). Treestore is a single-feed key/value + pub/sub abstraction
on top of a CXO node; the standalone daemon below is the general node itself.

- Use the **standalone daemon** when you want a general object-replication node /
  relay, or to inspect feeds and roots.
- Use the **treestore utilities** when you want app-level pub/sub keyed by a
  single feed identity (that is what skychat `--cxo` and the TPD aggregator use).

---

## Identity and the `<pk>@host:port` convention

A CXO node has an **identity key pair**. The public key is how peers refer to the
node (it is used for connection de-duplication, and — for a node that serves its
own feed — it is the feed others subscribe to).

By default the daemon generates a **random ephemeral identity on every start**,
so its public key changes across restarts. Pass `--sk <hex>` to pin a **stable
identity** — required if you want other nodes/utilities to reach this node by a
fixed `<pk>@host:port` address (the same convention used by
`skywire cli skychat send --via tcp://<pk>@host:port`).

On startup the daemon prints its public key and reachability lines:

```
CXO node identity (pk): 039dfe4d2fe1190d2504337f1a73d54db53c191423ae313b57bbdbef3c640c1c9d
CXO reachable (tcp): 039dfe4d...640c1c9d@<host>:8870
```

Substitute `<host>` with the node's reachable address (a forwarded public IP, a
LAN address, or `127.0.0.1` for local testing).

> The node identity key is a **connection** identity. A *feed* is a separate
> public key (the key whose holder signs the feed's roots). For a node that
> serves a single feed equal to its own identity, the two coincide — that is the
> treestore pattern. For a general multi-feed node they need not.

---

## Running the daemon

```
skywire cxo daemon [flags]
```

Key flags:

| Flag | Default | Purpose |
|------|---------|---------|
| `--sk <hex>` | _(random)_ | Node identity secret key. Empty = random ephemeral identity (pk changes each restart). |
| `--tcp <addr>` | `:8870` | TCP listen address. |
| `--udp <addr>` | _(off)_ | UDP listen address. |
| `--rpc <addr>` | `:8871` | RPC control address (used by `skywire cxo cli`). |
| `--public` | `false` | Advertise as a public server. |
| `--mem-db` | `false` | In-memory object store (nothing persisted to disk). |
| `--data-dir <path>` | `~/.skycoin/cxo` | On-disk data directory (when not `--mem-db`). |
| `--max-connections <n>` | `1000000` | Connection cap (in + out, tcp + udp). |
| `--max-filling-time <dur>` | `10m` | Max time to fill a root. |
| `--max-heads <n>` | `10` | Max heads per feed. |
| `--debug` | `false` | Verbose logs. |

Example — a stable-identity node on TCP, in-memory:

```
skywire cxo daemon --sk 5f1c...e9 --tcp :8870 --rpc 127.0.0.1:8871 --mem-db
```

By default the daemon **accepts all inbound subscription requests** (it shares
any feed a peer asks it to), which makes it usable as an open relay.

---

## The CLI

`skywire cxo cli` talks to a running daemon over RPC. Point it at the daemon's
`--rpc` address with `-a/--address` (default `[::]:8871`). Run interactively, or
one-shot with `-e/--exec "<command>"`.

Command tree:

- **`feed`** — which feeds this node hosts/replicates:
  - `feed share <pk>` — start hosting/serving feed `<pk>`.
  - `feed unshare <pk>` — stop.
  - `feed list` — list shared feeds.
  - `feed is-sharing <pk>` — check one.
- **`tcp`** / **`udp`** — transport connections:
  - `tcp connect <address>` / `tcp disconnect <address>`.
  - `tcp subscribe <pk>@<address>` — connect-context + subscribe to feed `<pk>`
    on the peer at `<address>` (the pk-as-identity form). The two-argument
    `tcp subscribe <address> <pk>` form also works.
  - `tcp unsubscribe <pk>@<address>` (or `<address> <pk>`).
  - `tcp address` — the node's TCP listen address.
  - (`udp …` mirrors `tcp …`.)
- **`connection list`** / **`connection list-by-feed <pk>`** — current peers.
- **`root info|tree|last <pk> [<nonce> <seq>]`** — inspect a feed's roots.
- **`stat`** — node statistics.
- **`stop`** — stop the daemon.

---

## Worked example — replicate a feed between two nodes

Node **A** serves a feed (here, its own identity feed); node **B** subscribes to
it and replicates it.

Start two daemons (different ports; stable identities):

```
skywire cxo daemon --sk <SK_A> --tcp 127.0.0.1:8870 --rpc 127.0.0.1:8871 --mem-db
skywire cxo daemon --sk <SK_B> --tcp 127.0.0.1:8872 --rpc 127.0.0.1:8873 --mem-db
```

Note A's printed identity, e.g. `PK_A = 031ff650…18ad4b`.

On **A**, start serving the feed:

```
skywire cxo cli -a 127.0.0.1:8871 -e "feed share 031ff650…18ad4b"
```

On **B**, connect to A and subscribe to the feed using `<pk>@host:port`:

```
skywire cxo cli -a 127.0.0.1:8873 -e "tcp connect 127.0.0.1:8870"
skywire cxo cli -a 127.0.0.1:8873 -e "tcp subscribe 031ff650…18ad4b@127.0.0.1:8870"
```

Verify on **B**:

```
skywire cxo cli -a 127.0.0.1:8873 -e "connection list"
#   ↑ tcp://127.0.0.1:8870(✓)
skywire cxo cli -a 127.0.0.1:8873 -e "feed list"
#   031ff650…18ad4b
```

B is now replicating feed `PK_A` from A. As A's feed advances (new roots), B
follows. Inspect with `root last 031ff650…18ad4b` on either node.

---

## Notes and limits

- **Subscribe requires a connection.** `tcp subscribe <pk>@<addr>` operates over
  an existing connection to `<addr>` — run `tcp connect <addr>` first (the
  example does).
- **Publishing roots is programmatic.** The CLI hosts, subscribes to and inspects
  feeds, but authoring new objects/roots for a feed (which requires that feed's
  secret key) is done through the node API (`pkg/cxo/node` + `pkg/cxo/skyobject`),
  not the CLI. For turnkey app-level publishing keyed by a single identity, use
  the treestore layer (e.g. CXO-backed skychat) instead.
- **`--mem-db` keeps nothing.** Restarting a `--mem-db` node drops its store; use
  `--data-dir` for persistence.
- **Stable identity is opt-in.** Without `--sk` the node's pk — and therefore its
  `<pk>@host:port` address — changes on every restart.

See also: `docs/skychat_cxo_tcp_standalone.md` for the treestore-backed,
single-feed app pattern.
