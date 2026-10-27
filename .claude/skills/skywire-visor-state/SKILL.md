---
name: skywire-visor-state
description: One call runtime introspection of a visor with `skywire cli visor state`, the runtime counterpart of `config show`. A curated snapshot without secrets, with --select, --watch, --jq, --shape and --via. Load for a fast answer about a visor's health, routing, transports, apps, policy, embedded services or which subsystems are wired.
---

# skywire cli visor state

`skywire cli visor state` returns a curated snapshot of the visor's live runtime. It never includes secret keys. It is the runtime counterpart of `config show`, which prints the config on disk.

## Workflow
1. `skywire cli visor state --shape` prints the schema skeleton with every field. It needs no live data, so read it first to learn the JSON paths.
2. `skywire cli visor state --jq '<expr>'` projects what you want. `--jq` implies `--json`.
3. `--via dmsg://<pk>` runs it against a remote visor. The remote must allow the caller, as for other debug surfaces.
4. `--select a,b` builds only those subtrees on the server. The full snapshot is large, so select when you can. Keys are `summary`, `health`, `routing`, `mux`, `pool`, `apps`, `transports`, `modules`, `cxo`, `services`, `proxy`, `diag`, `roles`.
5. `--watch 1s` streams one NDJSON snapshot per tick. It composes with `--select`, `--jq` and `--via`.

## Top level keys
`at`, `suspended`, `summary`, `health`, `service_health`, `routing_stats`, `route_groups`, `mux_route_groups`, `mux_counters`, `pool`, `routing_policy`, `router_config`, `apps`, `transports`, `persistent_transports`, `modules`, `cxo`, `services`, `proxy`, `roles`, `diag`, `tpd_leaf_publisher`, `notes`. The `--shape` output is the authority.
- `summary` embeds the `visor info` overview.
- `modules` shows which optional subsystems are wired, often the answer to "why is there no stats feed".
- `services` lists each embedded deployment service with its running state, whether it has its own key, restarts, and its CXO aggregators and publishers.
- `roles` shows what the visor is for the network, such as its dmsg server and relay roles.
- `notes` holds per section errors. A suspended or half started visor still returns a useful snapshot.

## Common queries
```
skywire cli visor state --select health --jq '.health'
skywire cli visor state --select summary --jq '.summary.overview.build_info.version'
skywire cli visor state --select transports --jq '.transports | group_by(.type) | map({(.[0].type): length}) | add'
skywire cli visor state --select services --jq '.services[] | {name, running, own_key}'
skywire cli visor state --select mux --jq '.mux_route_groups'
skywire cli visor state --select modules --jq '.modules'
skywire cli visor state --via dmsg://<pk> --jq '.routing_policy'
```
To compare a field across a fleet, loop over the keys from `skywire cli visor hv ls` with `--via`, one at a time.

## Changing state
`visor state` is read only. Live setters are separate commands such as `route settings`, `route minhops`, `proxy settings`, `visor app arg ...`, `tp auto` and `visor skyenv`. See skywire-route-cli and skywire-visor-cli. A config edit on disk needs a restart or regen to take effect.
