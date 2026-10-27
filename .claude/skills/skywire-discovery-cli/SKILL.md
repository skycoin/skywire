---
name: skywire-discovery-cli
description: Query surface for the skywire discovery/services CLI groups, `skywire cli sd` (combined service+transport network stats), `ut` (uptime tracker: TPD/SD/DMSG-integrated /uptimes, daily %, timeline graphs), and `svc` (deployment-service read endpoints: health, TPD/DMSGd/AR read APIs). All read-only, DMSG-only, fetched via the local visor RPC by default. Load when inspecting the SD/UT/TPD/DMSGd/AR discovery data or checking deployment-service health from the CLI.
---

# skywire discovery / services CLI (`sd` · `ut` · `svc`)

Three read-only command groups that query the deployment discovery services over the mesh. Deployment services are **DMSG-only** (no plain HTTP). Every command fetches through a shared chain, **CXO subscriber-cache → visor RPC (DmsgHTTP) → direct DMSG**, tunable with `--no-cxo` / `--no-rpc` / `--no-dmsg` (there is no `--http` step). A local visor on `--rpc` (default `localhost:3435`) is used for the RPC hop; without one, the direct-DMSG path (a CLI-owned ephemeral dmsg client) still works but is slower. Add `--json` for machine-readable output.

Service keys come from `svc health`. Do not hard code them.

## `svc`, deployment-service health + read endpoints
- **`svc health`**, the fast first look: table of SERVICE / STATUS / LATENCY / PROTOCOL(dmsg) / VERSION / PK for Config, Transport Discovery, DMSG Discovery, Address Resolver, Route Finder, Service Discovery, and every DMSG Server. Also hands you all the service PKs. `--json` for the raw entries.
  - `--direct`, skip the visor RPC, probe services with a CLI-owned client (CXO/DMSG).
  - `--service <svc-pk>`, narrow to ONE service; **only effective with `--dmsg-server` or `--direct`** (ignored on the default all-services RPC path, it prints a note saying so).
  - `--dmsg-server <pk | pk@host:port>`, pin the /health fetch through ONE dmsg server via a standalone direct client. Tests "is service X reachable VIA server Y", even for direct-client services with no discovery entry. Requires `--service`.
  - `--dmsg-server <pk> --carriers wt,ws,quic,tcp`, DIRECT per-carrier reachability probe of that one server (one clean standalone session per carrier). Reveals which protocols actually reach it, e.g. `quic FAIL / wt OK / ws OK / tcp OK`. `--hold-carrier <c>` holds a session on `<c>` in-process during the probes to reproduce in-process carrier interference (diagnostic).
  - `--port <n>`, service dmsg port for /health (default 80).
- **`svc tpd <sub>`**, Transport Discovery read endpoints. Network-wide subs need no args; per-visor/per-transport take `-p/--pk`, `-p/--pks`, `-i/--id`, `-i/--ids`:
  - `stats` (network by_type + totals), `versions` (version histogram), `per-key-stats` (every visor's tp counts), `visor-stats -p <pk>`, `bandwidth` (network `/metric`; `-p <pk>` → per-visor), `bandwidth-tp -i <tpid>`, `versions-pk -p <pk,...>`, `metrics-visor -p <pk,...>`, `metrics-tp -i <id,...>`.
  - `uptime`, TPD's OWN session history (version-tagged, restart-loop / down-window detection) from `pkg/serviceuptime`. `-n N` limits rows, `--gap-threshold` / `--restart-loop-threshold` tune the annotations. A TPD that does not keep this history answers HTTP 503, and the command says so.
- **`svc dmsgd <sub>`**, DMSG Discovery: `all-servers`, `server-clients` (clients grouped by server), `clients -p <server-pk>` (clients sessioned with one server).
- **`svc ar`**, Address Resolver `/transports` (stcpr/sudph-registered edges). **`svc ar check <pk>`**, is a visor registered, without revealing its IP → `registered for [stcpr sudph wt]` or not-found. (This is the only `svc` sub that is RPC-only, no `--direct`.)

All `svc tpd`, `svc dmsgd` and `svc ar` subs take `--direct` and `--rpc`, and honor `--json`. `--tpdurl`, `--dmsgdurl` and `--arurl` override the service URLs.

## `ut`, uptime tracker (per-day %, online lists, version tallies, timeline graphs)
Bare **`ut`** queries the TPD-integrated `/uptimes?v=v3&days=30` and prints `<pk> <date> <daily%>` for days above `-n/--min` (default 75). Key flags: `-k/--pk <substr>` (filter), `-o/--on` (online only), `-s/--stats` (count), `-t/--stats2` (with `--on`: tally online visors by version), `-l/--list-versions`, `-v/--version <exact>`, `--min-version <v>` (now correctly includes git-describe builds like `v1.3.92-0-…`), `--max-tp N` (fetch TPD, keep visors with ≤N transports), `--date YYYY-MM-DD` (single day). URL/cache: `-u/--url` (alias hidden `--uturl`, matching `sd`), `--tpdurl`, `--cdu`/`--cdt` cache dirs (`""` disables), `-m/--cfa` cache-age minutes.
  - Local visor daily %: `skywire cli ut -n0 -k $(skywire cli visor pk)` (empty if the visor hasn't accrued daily data yet, that's normal, not a failure).
- **`ut sd` / `ut mdisc` / `ut tpd`**, same shape against the OTHER integrated sources: service-discovery (`sd`, cheapest, rides SD heartbeats), dmsg-discovery (`mdisc`, most accurate "reachable via dmsg"), transport-discovery (`tpd`, feeds rewards). These use the shared `cliuptime` factory: typed per-day table, flags `--url -k --on -s --version --min-version -l --min-daily -d/--days --since --until -a/--all --json`.
  - **`ut <src> graph`**, render the v3 per-5-minute timeline as shaded 1-hour blocks. `--per-day` (row per day), `--hours N` (rolling window), `-v/--verbose`, `--shuffle`. Needs v3.
  - GOTCHA: `ut sd/mdisc/tpd` default to `v2`, which the CXO feed does NOT mirror (only `v3&days=30` is mirrored). So they always CXO-miss and fall to RPC/direct-DMSG; a transient `dmsg error 202 - cannot connect to delegated server` just means retry.

## `sd`, combined service + transport network view
**`sd`** joins Service Discovery (proxy/vpn/visor entries), Transport Discovery (`/all-transports`, tp counts by type), and the UT online flag into one table: `pk country version services stcpr sudph dmsg stcp total`, color-coded (red = offline, white-on-red = not in UT, black-on-red = online but <2 real transports). Sorted by total transports desc.
- Filters: `-c/--country`, `-e/--version`, `-n/--min <minTransports>`, `-o/--noton` (keep offline / not-in-UT rows; default shows online only). `--json` emits the joined rows.
- URLs/cache: `-a/--sdurl`, `-b/--tpdurl`, `-w/--uturl`, `--cds`/`--cdt`/`--cdu` cache dirs, `-m/--cfa` cache-age.
- It fetches FIVE endpoints sequentially (proxy+vpn+visor SD, all-transports, uptimes) so it is the slowest command here, allow 60–120s over dmsg; time out ≠ down.

## See also
skywire-deployment-services for health, pprof and logs over the :4443 proxy, skywire-transports and skywire-transport-setup for `tp`, skywire-routing for mux telemetry, skywire-cli for the index.
