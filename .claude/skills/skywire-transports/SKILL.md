---
name: skywire-transports
description: Query and inspect skywire transports, the local visor's live transports, a remote visor's transports (live via the Transport Setup Node), and the Transport Discovery (TPD) network view. Load when asked "what transports does X have", to compare a visor's live transports vs TPD, dump all network transports, or check TPD/uptime health/stats.
---

# skywire transport querying (local · remote-via-TPS · TPD/network)

Three distinct sources of transport truth, do NOT conflate them:
1. **LOCAL**, the visor's own transport manager (live, authoritative for that visor).
2. **REMOTE via TPS**, another visor's LIVE transports, fetched through the Transport Setup Node.
3. **TPD (Transport Discovery)**, the network's registered/published record. A visor **publishes ALL its transports (inbound AND outbound)** to TPD; the route-finder consults TPD.

`skywire cli tp` is top-level (NOT under `cli visor`). Types: **stcpr, sudph, dmsg, squicr (QUIC), webrtc, stcp**. Transport ID = 16-byte UUID, deterministic from (sorted PK-pair, type), see `tp id`.

## Local visor's transports
- `skywire cli tp`, list local live transports (type · id · remote_pk · mode(in/out) · label).
- Flags: `-t/--types stcpr,sudph` (filter by type) · `-p/--pks <pk>,...` (filter by peer) · `-l/--logs` (default on) · `-b/--bw <N>` (append bandwidth/latency cols, last N days, FAST, verified) · `-m/--more` (append SD country/region cols, **SLOW, ~40-70s even for one peer; avoid unless needed**).
- `skywire cli tp -L`/`--live`, bubbletea TUI, 1s live-refresh of per-transport bw/latency. **Requires a real TTY** (FATAL `could not open /dev/tty` when run headless/piped). Not compatible with `--remote`/`--id`/`--tptypes`.

## A REMOTE visor's LIVE transports (via Transport Setup Node)
- `skywire cli tp --remote <pk>[,<pk>...]`, remote visor(s)' transports LIVE via TPS. Prints `[i/N] <pk> (K transports)` + table. Shows what a peer ACTUALLY has now vs what TPD registered.
- `skywire cli tp --skynet <pk>` reads the list a remote visor signs and serves, over a skywire transport to it.
- `--via dmsg://<pk>` / `skynet://<pk>`, route the query through a specific visor.

## Transport Discovery (TPD), the network's registered view
- `skywire cli tp disc -p <pk>`, transports TPD registered for a visor key (id · type · edge1 · edge2). `-i <tpid>`, one transport by ID. Fetch-chain flags: `--http` (bypass RPC, query TPD directly), `--no-cxo`/`--no-rpc`/`--no-dmsg` (skip a stage), `--tpdurl dmsg://...:80`.
- `skywire cli tp all`, **dump ALL transports on the network** (the explicit all-of-TPD list; ~6000-9000 network-wide). Answer to "get everything from TPD".
- `skywire cli tp tpd-stats`, visors ranked by TPD transport count (`pk total stcpr sudph dmsg` + `Total visors: N`). `-n/--top N`, `-t/--type stcpr`, `--min N`.
- `skywire cli tp net-stats`, network aggregate (Total Transports, Unique Visors, by-type). Verified.
- `skywire cli tp metrics`, TPD bandwidth metrics per visor. `-d/--days`, `-n/--top`, `-p/--pk`, `-t/--by-transport`, `--tree`, `-v/--verbose`. Verified.
- `skywire cli tp tree`, tree map of network transports. Rich: `-k/--source`, `-d/--dest` (map a route), `-s/--stats` (just counts, verified), `-K/--keys` (reachable PKs from `-k` source), `-H/--hops 1|2`, `-r/--raw`/`-p/--pretty` (json), `-g/--good` (hide offline), `-v/--version <min>`, cache `--cft`/`--cfu`/`--cfa`.
- `skywire cli tp tpd-health`, TPD version/health. Verified.
- `skywire cli tp v`, list public visors (from service discovery). `-c/--country`, `-k/--pk`, `-s/--stats`, `--json`, `-r/--raw`, `-e/--version`.
- `skywire cli tp uptime`, TPD-integrated transport-level uptime.

## Degraded behavior
- **`tp uptime` (default, bulk per-transport rows)**: the bulk `/uptimes/transports` fetch can still time out over the visor RPC (large payload), but the CLI now WARNS and falls back to the `/metrics/uptime` aggregate instead of erroring empty. The warning itself lists the remedies: `--no-rpc` (direct DMSG, longer timeout), scope with `--ids`/`--visors`, or `--v v1`. `tp uptime -m` (aggregate) and `--ids <tpid>` still work as before.
- **`tp v`** now returns the real visor list by default; `tp v -o`/`--noton` remains useful to include offline rows.

## Comparing LOCAL vs TPD ("does TPD agree with my visor?")
```
MYPK=<pk>
skywire cli tp             | awk 'NR>1{print $2}' | sort -u > local.txt
skywire cli tp disc -p $MYPK | awk 'NR>1{print $1}' | sort -u > tpd.txt
comm -23 local.txt tpd.txt   # local has, TPD missing
```
**Gotcha:** right after a visor restart the gap looks huge. This is publish convergence lag of about 10 minutes, not a chronic gap. Always factor in the time since restart.

To CREATE/REMOVE transports, see **skywire-transport-setup**.
