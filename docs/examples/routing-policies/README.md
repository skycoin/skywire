# Routing-policy examples

Live, runnable Starlark policies for the operator-programmable
routing DSL (RFC #2882). Every file parses + decides cleanly
under `skywire cli route policy test`; the candidate-filtering
ones return the bare default in the previewer (no candidates →
no filter) and only do meaningful work in production where the
SelectRoute hook supplies real candidates.

## Quickstart

```bash
# Preview what a policy would decide for a synthetic dial.
skywire cli route policy test \
  --script docs/examples/routing-policies/app-mux.star \
  --dial '{"app":"vpn-client"}'

# Install one as the visor-wide default.
sudo install -m 0644 docs/examples/routing-policies/app-mux.star \
  /etc/skywire/policies/

# In skywire.json:
#   "routing": {
#     "policy_per_dial": "@/etc/skywire/policies/app-mux.star"
#   }
#
# Reload picks it up automatically — file watcher debounces 200ms.
```

## The two-phase invocation model (critical)

`decide_route(ctx, candidates)` is called **twice per dial**:

1. **`BeforeDial`** — fires *before* the route-finder runs. The
   `candidates` argument is **empty**. The router honors:
   - `mux` — number of parallel mux legs
   - `min_hops` — minimum acceptable intermediate count
   - `distribution` — per-packet distribution descriptor (baseline)
   - `fallback="drop"` — refuse the dial outright
2. **`SelectRoute`** — fires *after* the route-finder returns
   candidates. The `candidates` argument is populated. The router
   honors:
   - `chosen` — the candidate to use
   - `distribution` — per-packet distribution descriptor (overrides
     BeforeDial's when set; lets the script branch on the chosen
     candidate's `transport_kinds` / `est_latency_ms` / `hops_geo`)
   - `fallback="drop"` — refuse the dial

`mux` and `min_hops` are BeforeDial-only — the route-finder query
needs them before it runs, so they can't be reconsidered in
SelectRoute. `distribution` can be set in either phase; when both
set it, SelectRoute's value wins (it fires later in the flow and
has more information).

### Directional asymmetry

`mux` / `min_hops` / `chosen` have per-direction overrides:
`forward_mux` / `reverse_mux`, `forward_min_hops` / `reverse_min_hops`,
and `reverse_chosen`. The reverse-direction candidates are
exposed in SelectRoute as `ctx.reverse_candidates` (separate
list from the positional `candidates` argument, which is always
forward).

```python
# Different transport kinds per direction:
# stcpr upstream, sudph downstream.
def decide_route(ctx, candidates):
    if not candidates:
        return RouteSpec(forward_mux=1, reverse_mux=4)

    fwd = None
    for c in candidates:
        if "stcpr" in c.transport_kinds:
            fwd = c
            break
    rev = None
    for c in ctx.reverse_candidates:
        if "sudph" in c.transport_kinds:
            rev = c
            break
    return RouteSpec(chosen=fwd, reverse_chosen=rev)
```

See `asymmetric-stcpr-sudph.star` for a full annotated example.
Partial picks (only `chosen` or only `reverse_chosen`) are
valid — the router fills the unset direction with its built-in
latency-ranked pick.

### Knowing the leg count

In SelectRoute, the script sees the route-finder's disjoint
candidates — `len(candidates)` is the maximum number of mux legs
the router could potentially establish (capped further by the
script's `mux` value and what `establishMuxRoutes` actually
manages to negotiate). Use it to size `weighted: ...` arrays:

```python
def decide_route(ctx, candidates):
    if not candidates:
        return RouteSpec(mux=4, distribution="auto")
    n = min(len(candidates), 4)
    # Equal weights, n entries — matches the actual leg count.
    weights = ", ".join(["1"] * n)
    return RouteSpec(chosen=candidates[0], distribution="weighted: " + weights)
```

When `weighted: ...` weight count doesn't match the actual
established leg count, the router logs a warning and the selector
substitutes weight=1 for missing entries (or ignores trailing
ones). There's no callback when a leg drops or is added after
setup — the selector adapts internally (skips nil/closed
transports) but the script's distribution config is set once.

The canonical pattern:

```python
def decide_route(ctx, candidates):
    if not candidates:
        # BeforeDial: ctx-only knobs (mux, min_hops, distribution).
        return RouteSpec(mux=2, distribution="size-threshold: 1400")

    # SelectRoute: candidate-dependent filter + pick.
    filtered = [c for c in candidates if some_condition(c)]
    if not filtered:
        return RouteSpec(fallback="drop")
    return RouteSpec(chosen=filtered[0])
```

**Why this matters:** before this convention, a policy doing
`if not filtered: return RouteSpec(fallback="drop")` would drop
**every dial** at BeforeDial time, because BeforeDial always
sees empty candidates. Every example here guards the empty
case explicitly.

## Layer 1 — per-dial route selection

| File | Use case |
|---|---|
| `app-mux.star` | Different apps get different mux + min-hops. |
| `no-us-transit.star` | Refuse routes that pass through US intermediates. |
| `rt-latency-budget.star` | Real-time apps need < 200ms; mux=2 BeforeDial, latency filter SelectRoute. |
| `trusted-only.star` | Every intermediate must be on the operator's trust list. |
| `hv-boost.star` | Peering with a hypervisor → mux=4 + min_hops=2 (forces multihop redundancy). |
| `business-hours.star` | 9–17 weekdays: only SG/JP/ID; off-hours: anything. |
| `vpn-killswitch.star` | VPN: opt into mux BeforeDial, refuse non-direct-IP legs SelectRoute. |
| `friday-id.star` | RFC headline: Friday 17:00 → Indonesia-transit only. |
| `asymmetric-stcpr-sudph.star` | Forward stcpr (TCP), reverse sudph (UDP); different mux/min-hops per direction. |
| `smoke-test.star` | Visible policy used for integration testing — logs every dial. |

## Layer 2 — per-packet distribution

The Starlark script emits a `distribution="..."` descriptor; the
policy package parses it; the router's transport selector
consumes it. Vocabulary:

- `"auto"` — latency-weighted (default)
- `"round-robin"` / `"equal"` — even round-robin across legs
- `"weighted: f1, f2, ..."` — operator fractional weights
- `"size-threshold: N"` — payload `> N` → leg 0; `≤ N` → RR across the rest

Distribution only takes effect on **multi-leg** route groups (the
peer must negotiate `CapMux` and the route-finder must surface
multiple disjoint candidates). Single-leg dials log
`"Route group distribution skipped: not mux-enabled"` and use the
sole leg.

DMSG transports never appear in multihop / mux routes — the
router strips them at route construction (a dmsg server is an
opaque intermediary that neither endpoint can observe, possibly
chained via server-to-server forwarding). So no distribution
example needs to reason about DMSG legs; they can't be there.

Distribution can be set in either BeforeDial or SelectRoute. A
SelectRoute distribution overrides the BeforeDial one — useful
when the script wants to branch on the chosen candidate's
properties (`weighted-by-kind.star` demonstrates the pattern).

| File | Use case |
|---|---|
| `weighted-static.star` | Static 3:1 weighted distribution for any mux dial. |
| `weighted-by-kind.star` | Dynamic: stcpr+sudph → weighted; otherwise auto. |
| `vpn-bulk-split.star` | VPN packets > 1400B → wide-pipe leg; small → RR rest. |
| `force-equal-rt.star` | Real-time apps force equal RR (lower jitter than auto). |
| `friday-id-mux.star` | Friday-ID combined with VPN size-threshold split. |

## Authoring notes

Three Starlark gotchas the examples already work around:

- **No chained comparisons.** Python's `9 <= h < 17` doesn't
  parse; use `h >= 9 and h < 17`.
- **`logging.info/warn` take exactly one string.** No `%s`
  formatting. Build the message with `+` concatenation:
  `logging.info("fired for app=" + ctx.app)`.
- **`candidates` is always a list** (empty in BeforeDial, populated
  in SelectRoute). Always check `if not candidates: ...` first.

The stdlib surface (`datetime`, `geo`, `transports`, `peers`,
`logging`, `RouteSpec`, `Candidate`) is defined in
`pkg/router/policy/bridge.go`. The RFC at
`docs/routing_policy_rfc.md` describes the full contract.

## Verifying end-to-end

When a policy is loaded the visor logs:

```
INFO [router]: Routing policy active. source="/etc/skywire/policies/<name>.star"
```

On each dial that matches the policy:

```
INFO [router]: policy <name>.star: info: <your logging.info message>
DEBUG [router]: Routing policy adjusted dial opts. app_name=... policy_mux=2 policy_min_hops=0 policy_distribution=size-threshold
DEBUG [RouteGroup ...]: Route group distribution applied. distribution=size-threshold legs=2 size_threshold=1400
```

The third line only fires when the dial established multiple
legs. If you see only the first two, your route is single-leg
and the distribution descriptor is a no-op (intended — there's
nothing to distribute across).
