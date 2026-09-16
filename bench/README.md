# bench — live route-multiplexing measurements

One directory per `<date>/<develop commit>`; every TSV row is one hash-verified
transfer through one proxy session, produced by `bench.sh`:

    bench/bench.sh <socks host:port> <sink base url> <bytes> <down|up> [label]

`down` GETs `<sink>/?bytes=N` from a `skywire cli proxy loadtest serve` sink and
checks the body's sha256 against the sink's `X-Sha256` header; `up` POSTs N random
bytes to `<sink>/upload` and checks the sink's sha256 of what it received. Columns:
`label dir bytes speed_Bps http got time_s hash_ok`. The first line of each file
names the exit, both ends' commits, the proxy session, its route and transport, and
the sink, so a row can be reproduced.

Rules (docs/design/route-multiplexing-test-plan.md): frozen exit (its update timer
stopped for the set), both ends on the same develop commit, one transfer at a time
unless the row says otherwise, references before subjects, no mux change merges
without a before/after row here.

## Reference sets, unattended

    bench/run-refs.sh <exit pk> bench/<date>/<commit> <pins dir> [trials] [sink]

produces `ref-direct-stcpr`, `ref-direct-squicr` (a `--direct` proxy each, with
`route settings --prefer` selecting the carrier) and one `ref-via-<short>` per pin
file `<pins>/via-<short>.json` (a proxy started with `--route`, so the session
runs on exactly that two-hop leg). Each set also writes `<set>.carrier.tsv`: per
row, the byte deltas of the transport the row is supposed to ride (and, for a
pinned route, the exit-side deltas of hop 2 over the whole set), so the carrier
claim in the header is checked against the transport counters rather than assumed.

## Mux sets, unattended

`bench/run-mux.sh <exit pk> <out dir> <pins dir> [trials] [sink] [pin order]`
takes the stream-level and packet-level sets after the references:

- `mux-tunnels-N` — `proxy start --tunnels N`: N independent tunnels, one
  route group each; the visor steers every extra tunnel onto a different
  first-hop transport.
- `mux-legs-N` — one routed proxy, then `proxy mux set --legs --prune` to
  exactly the first N pinned two-hop routes as legs of ONE route group. The
  group's `src_port` is checked before and after the set (it must not change).

`<set>.legs.json` is the `mux info --json` snapshot the set ran on (groups,
legs, transport types, remote pks); `<set>.carrier.tsv` has the per-row byte
deltas of every first-hop transport. `TUNNELS="2 3"` and `LEGS="2 3 5"`
choose the counts; `[pin order]` lists pin shorts best route first.
