# Routing-policy examples

Live, runnable Starlark policies for the operator-programmable
routing DSL (RFC #2882). Every file in this directory parses and
decides cleanly under `skywire cli route policy test`; the ones
that gate on candidates are no-ops when previewed without any.

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

## Layer 1 — per-dial route selection (works today)

| File | Use case |
|---|---|
| `app-mux.star` | Different apps get different mux + min-hops. |
| `no-us-transit.star` | Refuse routes that pass through US intermediates. |
| `rt-latency-budget.star` | Real-time apps need < 200ms; drop otherwise. |
| `trusted-only.star` | Every intermediate must be on the operator's trust list. |
| `hv-boost.star` | Peering with a hypervisor → 4 mux legs. |
| `business-hours.star` | 9–17 weekdays: only SG/JP/ID; off-hours: anything. |
| `vpn-killswitch.star` | VPN MUST egress via direct-IP transports; drop on relay-only. |
| `friday-id.star` | RFC headline: Friday 17:00 → Indonesia-transit only. |
| `smoke-test.star` | Visible policy used for integration testing — logs every dial. |

## Layer 2 — per-packet distribution (works today, RFC phase 5)

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

| File | Use case |
|---|---|
| `weighted-by-kind.star` | Two legs of mixed direct-IP kinds (stcpr + sudph) → 3:1 to stcpr. |
| `vpn-bulk-split.star` | VPN packets > 1400B → wide-pipe leg; small → RR rest. |
| `force-equal-rt.star` | Real-time apps force equal RR (lower jitter than auto). |
| `friday-id-mux.star` | Friday-ID combined with VPN size-threshold split. |

## Authoring notes

Two Starlark gotchas the examples already work around:

- **No chained comparisons.** Python's `9 <= h < 17` doesn't
  parse; use `h >= 9 and h < 17`.
- **`logging.info/warn` take exactly one string.** No `%s`
  formatting. Build the message with `+` concatenation:
  `logging.info("fired for app=" + ctx.app)`.

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
