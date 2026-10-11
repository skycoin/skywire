# skywire cli proxy start

[← skywire cli proxy](../README.md)

Start the proxy client

## Usage

```
skywire cli proxy start [pk]
```

## Flags

```
  -a, --addr string                         address of proxy for use (default ":1080")
      --direct                              force a DIRECT-transport-only route to the exit: create the transport on demand if none exists, dial 1-hop (bypassing the route-finder + setup node), and self-heal when the transport drops. Bypasses the routing policy and the adaptive mux entirely — the point is a single, stable, policy-free direct leg. Mutually exclusive with --routing-policy, --route, --min-hops>1 and --tunnels>1 (all of which ask for the multi-hop/overlay path --direct exists to avoid); any per-app policy is cleared.
      --existing-tp                         only use existing transports, don't create new ones
      --external                            force external launcher
      --http string                         address for http proxy
      --internal                            force internal launcher
      --local-route                         calculate routes locally instead of using route finder
      --min-hops uint16                     minimum routing hops for this session (1=no minimum). Set on the visor before app start; rolled back is not automatic — restart visor or re-run with --min-hops=1 to revert. (default 1)
      --mux-mode string                     mux weight distribution mode: auto (latency-based) or equal (round-robin) (default "auto")
  -n, --name string                         name of skysocks client
  -k, --pk string                           server public key
      --port uint16                         routing port for communication between proxy (skysocks) and visor
      --range-chunk-kib int                 bytes per range request of the transparent range-splitter, in KiB (smaller chunks balance a download across tunnels of unequal speed at the cost of one request round trip per chunk) (default 4096)
      --range-concurrency int               concurrent range requests per split download (default 8)
      --range-port int                      destination port the transparent range-splitter treats as plaintext HTTP (default 80; set to a bench origin's port to split it across --tunnels) (default 80)
      --reconnect                           in-process reconnect on route-group collapse: proxy keeps re-dialing with backoff instead of dropping the SOCKS5 listener; --reconnect=false restores exit-on-failure (default true)
      --route proxy mux set --rg <port>     pin explicit route(s) chosen by you instead of the route finder: a JSON file of {forward,reverse} hop pairs ('cli route calc <exit> --count N --json' shape). Once the proxy is up its mux legs are reconciled to these — each pinned route is added as a leg and any AUX auto legs are pruned. The auto primary leg is pruned too (the router re-homes the primary), so the session runs on the pinned routes alone. One pair = one pinned route; N pairs = N disjoint legs. Tip: 'route calc --source tps' avoids stale-transport install failures. Implies a routed dial (no AppDirect shortcut) so the session has a route group to pin. Pins ONE group, so it cannot be combined with --tunnels >1 — start those, then pin each with proxy mux set --rg <port>.
      --routing-policy string               per-app routing policy: @/path/to/policy.star, @/path/to/policy.wasm, or preset:<name> ("" or "none" clears any previously-installed override)
      --standby-pool proxy mux info         maximum tunnels held open to the exit INCLUDING the active --tunnels. Everything beyond the active set is held in STANDBY: dialed, kept alive and measured on the same 5s ping, carrying no streams — so a tunnel that dies is replaced by a route that already exists instead of one set up from scratch (8-9s through the setup node). The pool fills setup.fill_inflight tunnels at a time (those dials leave as ONE batched setup request where the setup node supports it), each required to leave over a first hop no tunnel already held occupies until setup.first_hop_filter_max are held, after which a reused first hop with a distinct intermediate is allowed; it stops either at this ceiling or the moment the router has no disjoint route left; a dial that merely FAILS is retried in bounded rounds, and a tunnel death re-arms the fill. A first hop is any transport this visor holds, so on a well-connected visor the ceiling is what stops it (the campaign rig had 124 free candidates left at the eighth dial) and this number is a bound on setup-node load, not a measurement of the topology. 0 = hold only the active tunnels; -1 (the default) = AUTO: one tunnel per disjoint route to the exit (one intermediate each, plus the direct transports), up to the live knob pool.size_cap. Roles show as role=active/standby in proxy mux info and as tunnel_role in `visor state --select mux_route_groups`. (default -1)
  -t, --timeout int                         timeout for starting proxy
      --tunnels proxy mux set --rg <port>   number of independent tunnels (route group + noise + yamux each) to stripe browser connections across. The default 2 AGGREGATES bandwidth: the second tunnel is dialed on the BEST-RANKED unused route — of the routes whose first hop no earlier tunnel holds, the one whose first-hop transport has the lowest measured latency (unmeasured ranked last) — so their throughputs sum. 1 = a single tunnel over the AppDirect shortcut (no route group). Shape the legs within each tunnel after start with proxy mux set --rg <port> / `mux auto --rg <port>`, where <port> is the group's own port as `proxy mux info` prints it (desc.dst_port) — every tunnel shares one src_port, so only that port names a single tunnel. Not combinable with --route, which pins a single group. (default 2)
  -v, --verbose                             stream the visor's logs scoped to this app's session (app stdout + tagged router/mux/setup events); ctrl+c stops the proxy and exits
      --verbose-level string                minimum log level when --verbose is set: trace|debug|info|warn|error (default "debug")
```

## Global Flags

```
  -h, --help              show help menu
      --jq string         filter JSON output through a jq/gojq expression (implies --json)
      --json              print output as JSON
      --rpc string        RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
      --shape             print the output schema skeleton (zero values, all fields) instead of data
      --tui               browse commands and help interactively
      --via dmsg://<pk>   remote visor target — dmsg://<pk> or `skynet://<pk>`
```

---
_Generated by `skywire doc` — do not edit by hand._
