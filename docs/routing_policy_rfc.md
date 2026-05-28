# RFC: Operator-Programmable Routing Policy

Status: **Draft.** Lays out the design space for letting operators write arbitrary per-dial routing decisions in an embedded scripting language. Implementation deferred until the design is agreed.

Related work: this builds on the unified-app-framework groundwork (RFC #2863, PRs #2860–#2879). It's downstream of that work but not blocked on it.

## Background

Today the visor's routing decisions are driven by a handful of CLI flags and per-dial overrides:

- `--routes N` — number of parallel mux legs
- `--min-hops K` — reject direct paths below K
- `--forward-min-hops` / `--reverse-min-hops` — per-direction overrides
- `--forward-mux` / `--reverse-mux` — per-direction leg counts
- `--via PK` — pin a specific intermediate

These are useful for ad-hoc experiments but fall short as a *policy* system:

1. **No persistence.** Operators specify routing knobs on every CLI invocation. There's no per-app persistent policy that says "for skychat, always prefer routes through trusted intermediates."
2. **No conditionality.** The knobs are static. There's no way to say "use mux=4 only during business hours" or "prefer routes through Indonesia on Fridays."
3. **No composition.** A flag like `--min-hops 2` either applies or doesn't. Operators can't write "first try mux=4 over min-hops=2; if no candidate matches, fall back to direct."
4. **No introspection.** The router picks a route opaquely. Operators can't see *why* a particular route was chosen, only that one was.
5. **No per-app default differentiation.** A skywire visor running both skychat and vpn-client today applies the same routing posture to both, even though they have very different requirements (low-latency interactive vs. high-throughput streamed).

Operator framing: *"we are really lacking a good interface for multihop/multiplexed routing control, a way to show routes in use by client app, well-defined automatic routing modes, and manual controls that don't fight automatic modes."*

The proposal in this RFC: **let operators write a routing policy in a small embedded scripting language that the visor evaluates at dial time.**

## Goals

1. **Arbitrary expressiveness.** The operator can write any decision logic that maps `(dial context, candidate routes) → chosen route`. Examples that must be expressible:
   - "Only routes through Indonesia on Friday at 5pm"
   - "Mux=4 if peer is in same datacenter, mux=1 otherwise"
   - "Avoid intermediates I've had >3 failures with in the last hour"
   - "For skychat: prefer low-latency direct routes; for vpn: prefer high-bandwidth multi-hop"
2. **Per-app override of a visor-wide default.** Each app can carry its own policy that supplants the global. The script always sees `ctx.app` so a single global can branch internally if the operator prefers.
3. **Deterministic, fast, sandboxed.** Per-dial budget is ~10ms. The policy must never block the visor, never do I/O, never hang on infinite loops.
4. **Hot-reload.** Operator edits their policy file; visor picks it up without restart.
5. **Failure-safe.** If the policy panics, times out, or returns garbage, the visor falls back to the current built-in default selection and logs the violation. A broken policy never breaks dialing.

## Non-goals

- **Not** a full Turing-complete runtime for the operator to do arbitrary work. The policy is a *decision function*, not a daemon.
- **Not** replacing the existing flags. CLI flags remain as per-invocation overrides; the policy is the persistent default.
- **Not** building a new language. The aesthetic the operator wants is achievable in existing embedded-script ecosystems.
- **Not** per-packet decision-making in the first pass. Per-packet decisions need a different perf tier (<10µs vs. <10ms); they're a deferred layer (see below).
- **Not** distributed policy synchronization. Each visor owns its own policy file. Cluster-wide reconciliation is out of scope.

## Design

### Two layers

**Layer 1 (per-dial)** — fires when an app dials a peer. Operator writes:

```python
def decide_route(ctx, candidates):
    # ... operator logic ...
    return RouteSpec(chosen=candidates[0], mux=4, fallback="direct")
```

Tens to hundreds of calls per minute. Budget ~10ms. Operator's "Indonesia on Friday" lives here.

**Layer 2 (per-packet)** — fires inside the mux router when a packet needs assignment to one of N parallel legs. Thousands of calls per second. Budget <10µs.

Layer 2 is **deferred** in this RFC. The Layer 1 `RouteSpec` returns a distribution descriptor (`"round-robin"`, `"weighted: [0.5, 0.3, 0.2]"`, `"size-threshold:1500"`) and the per-packet Go code stays compiled. The operator gets to *configure* per-packet behavior from Layer 1; the hot path stays fast.

When real per-packet scripting becomes necessary (operator wants logic per-packet that can't be expressed as a static distribution descriptor), Layer 2 lands as a separate RFC with its own language choice (CEL or compiled-bytecode candidates).

### Language: Starlark

Compared against CEL, Expr, Lua (gopher-lua), Goja (JavaScript), Tengo, Risor, Yaegi, Rego, WASM, and clean-room mini-mcl.

Eliminated outright:
- **Yaegi / WASM** — overkill (Yaegi is a Go interpreter; WASM forces operators to compile their scripts for what's usually a 10-line policy).
- **Rego** — technically excellent for policy but Datalog/logic-programming mental model is alien to most operators.
- **Clean-room mini-mcl** — writing a new language for one feature commits the project to maintaining a parser, type checker, evaluator, and tooling forever. Bad trade.
- **CEL / Expr** — fastest by far (<1µs/eval, bytecode-compiled) but **expression-only**. The Indonesia-Friday example already wants intermediate bindings; CEL forces it into one nested ternary that gets unreadable fast. CEL stays in scope for Layer 2 *because* it's an expression language fits per-packet's `(state) → leg_index` shape.

The remaining tournament was **Starlark vs. Lua**. Both are technically excellent, mature, sandboxable, sub-millisecond. Starlark wins on three counts:

1. **Safety by default.** Starlark literally cannot do I/O, spawn goroutines, mutate globals, or run unbounded loops. Sandboxing is the design, not a configuration of removed APIs.
2. **Aesthetic match.** The mgmt mcl example that prompted this proposal:
   ```
   $is_friday = datetime.weekday(datetime.now()) == "friday"
   mode => if $is_friday { "0550" } else { "0770" }
   ```
   In Starlark:
   ```python
   is_friday = datetime.weekday(datetime.now()) == "friday"
   mode = "0550" if is_friday else "0770"
   ```
   Near-1:1.
3. **Maintained by Google for Bazel.** Several decades of operational hardening, including the precise use case of "untrusted operators writing configuration scripts."

Lua is the runner-up. Notably more operators have touched Lua before (OpenResty / Redis / Wireshark / game scripting), and gopher-lua is pure Go. If the team prefers Lua for ecosystem familiarity over mgmt-aesthetic match, it's a defensible choice. The recommendation here is Starlark.

License: `go.starlark.net` is Apache-2.0.

### Operator-facing example

```python
# /etc/skywire/policies/friday-id.star
load("datetime")
load("transports")

def decide_route(ctx, candidates):
    # Bind intermediate concepts cleanly.
    is_friday_evening = (
        datetime.weekday(ctx.now()) == "friday" and
        ctx.now().hour == 17
    )

    if is_friday_evening:
        # Only routes that transit through Indonesia.
        candidates = [c for c in candidates if "ID" in c.hops_geo]

    if not candidates:
        # Nothing matched our filter — fall back to direct.
        return RouteSpec(fallback="direct")

    # Pick the lowest-latency surviving candidate.
    candidates.sort(key=lambda c: c.est_latency_ms)
    return RouteSpec(
        chosen=candidates[0],
        mux=4 if ctx.app in ("vpn-client", "vpn-server") else 1,
        min_hops=2,
    )
```

### Per-app override hierarchy

Two levels:

- **Visor-wide default**: `conf.Routing.PolicyPerDial` — file path (`@/etc/skywire/policies/global.star`) or inline string. Applies to every dial unless the app supplants it.
- **Per-app override**: `AppConfig.RoutingPolicy` — same shape. When set on a specific app, that policy wins for dials originating from that app.

The script always sees `ctx.app` so a single visor-wide policy can branch on app name internally if the operator prefers a unified file.

### Standard library (mcl-flavored)

Exposed to Starlark as importable modules:

- **`datetime`** — `now()`, `weekday(t)`, `hour`, `minute`, time arithmetic. Returns operator-config-timezone values.
- **`transports`** — `latency(pk)`, `kind(pk)` (stcpr/sudph/dmsg), `history(pk)` (recent successes/failures).
- **`geo`** — `country(pk)` returning ISO-3166 code from the embedded geoip database. Already available in the binary; this just surfaces it.
- **`peers`** — `is_trusted(pk)`, `is_hypervisor(pk)`, `whitelist_contains(pk)`.
- **`logging`** — `info(msg)`, `warn(msg)`. Bounded — script can't log-flood the visor; rate-limited per-script-instance.

Expensive lookups (geoip, transport history) are memoized per-visor-uptime. Even a policy that asks "where are all my known transports geographically?" doesn't burn CPU on every dial.

### Configuration

```jsonc
"routing": {
  "policy_per_dial": "@/etc/skywire/policies/global.star",
  "policy_timeout_per_dial_ms": 50,
  "policy_failure_mode": "fallback-to-default"  // or "drop"
}
```

Inline scripts allowed for tiny policies; `@` prefix references a file. File-watcher reloads on change. In-flight evaluations finish on the old version; subsequent dials use the new one.

### Failure modes

Every policy invocation runs under a context with the configured timeout (default 50ms). If the script:

- **Panics** — caught by the host. Fallback to built-in default. Logged at WARN.
- **Times out** — context canceled. Fallback. Logged at WARN.
- **Returns the wrong type** — type-checked at receive. Fallback. Logged at WARN.
- **Returns `None`** — explicit "I have no preference"; visor uses built-in default. No log.

`policy_failure_mode: "drop"` is for operators who want a broken policy to actually break dialing (so they notice immediately). Default is `"fallback-to-default"`.

### Tooling

- **`skywire cli route policy test --script foo.star --dial '<json ctx>'`** — preview the decision a script would return for a synthetic dial context. No actual dial. Operators iterate without bouncing the visor.
- **`skywire cli route policy bench --script foo.star`** — runs the policy 1M times against synthetic contexts, reports p50/p99 eval time. Hard error at load if budget exceeded.
- **`skywire cli route policy reload`** — explicit reload (the file-watcher is the normal path).
- **`skywire cli route policy trace --tail`** — every decision streams a structured event (input context, candidates considered, return value, elapsed µs). Deferred to post-MVP per operator direction; mentioned here for completeness.

## Phased implementation plan

Five phases. Each lands as one PR. Each is independently revertable.

### Phase 1 — Go-side scaffold

`pkg/router/policy` package. Defines `RoutingContext` (Go struct), `Candidate` (Go struct), `RouteSpec` (Go struct). Adds a no-op policy hook in the dial path that always returns the built-in default. Wired behind a feature flag in `conf.Routing` so the existing dial path is unchanged when the policy field is empty.

### Phase 2 — Starlark integration

Add `go.starlark.net` to `go.mod`. Implement `policy.NewEvaluator(scriptPath)` that loads + parses + validates the script, returns an evaluator with `Decide(ctx, candidates) (RouteSpec, error)`. Timeout enforced via `starlark.Thread.SetMaxExecutionSteps`. Failure mode wired up.

After this phase, the Indonesia-Friday example is parseable and the visor can call it, but the stdlib is empty so the script can't actually inspect the world.

### Phase 3 — mcl-flavored stdlib

`datetime`, `transports`, `geo`, `peers`, `logging` modules exposed as Starlark builtins. Per-visor-uptime caches for the expensive lookups.

After this phase, the Indonesia-Friday example *works*.

### Phase 4 — Operator tooling

`cli route policy test` + `cli route policy bench` subcommands. File-watcher for hot-reload. Per-app override field on `AppConfig`.

### Phase 5 — Layer 2 distribution descriptor (LANDED)

`RouteSpec.distribution` field is parsed by `pkg/router/policy.ParseDistribution` and applied to the route group's existing `transportSelector` via `DialAdjustment.Distribution`. Starlark stays out of the per-packet path; operator gets to configure distribution from Layer 1.

**Descriptor vocabulary (v1, finalized):**

| Descriptor | Mode | Behavior |
|---|---|---|
| `""` | `DistributionUnset` | No override — route group uses the router's visor-wide `muxMode` default (`WeightModeAuto`, latency-weighted). |
| `"auto"` | `DistributionAuto` | Force `WeightModeAuto` (latency-weighted with round-robin fallback). |
| `"round-robin"` / `"equal"` | `DistributionRoundRobin` | Force `WeightModeEqual` — packets alternate across legs ignoring latency. |
| `"weighted: f1, f2, ..."` | `DistributionWeighted` | Operator-supplied fractional weights normalized into the selector's integer schedule. Length must match leg count; mismatches fall through with a log line. |
| `"size-threshold: N"` | `DistributionSizeThreshold` | Payloads `> N` bytes go to leg 0 (wide pipe); payloads `≤ N` round-robin across the remaining legs. Single-leg routes ignore the descriptor. Control/handshake packets (size unknown) take leg 0. |

Real per-packet scripting (CEL or compiled bytecode) is a separate RFC, opened only when an operator demonstrates a use case that can't be expressed as a static distribution descriptor. The vocabulary above leaves room for that follow-up: descriptors not on this list (`"sticky: 5tuple"`, `"latency-aware"`, etc.) return a parse error today so they can be added meaningfully later without ambiguity.

### Phase 6 — Post-setup leg-change callback (LANDED)

`decide_route(ctx, candidates)` is one-shot at dial-setup time. But mux route groups have dynamic legs: `appendRouteToGroup` can add legs after setup, and transport failure can drop legs mid-session. Phase 5's distribution descriptor was set once and the selector adapted internally (skipping nil / closed transports during `Rebuild`), but the script had no way to react — a `"weighted: 3, 1"` schedule against an initial 2 legs stayed at `[3, 1]` even after the route group grew to 3 legs.

Phase 6 adds an optional script function:

```python
def on_leg_change(ctx, legs, change):
    # ctx is the dial's RoutingContext (same fields as decide_route's ctx).
    # legs is the route group's current legs: [{index, kind, latency_ms, alive}, ...].
    # change is {"event": "added" | "dropped", "leg_index": N}.
    #
    # Return a RouteSpec; only the distribution field is honored
    # — mux/min_hops/chosen are dial-time decisions that can't be
    # changed after setup.
    n = sum([1 for l in legs if l.alive])
    if n == 0:
        return RouteSpec()
    # Re-balance weights across all live legs.
    weights = ", ".join(["1"] * n)
    return RouteSpec(distribution = "weighted: " + weights)
```

Scripts that don't define `on_leg_change` get phase 5's static behavior — the function is optional. The route group fires the callback synchronously from leg-mutation paths (`appendForwardLeg`, transport close detection) so the schedule rebuild stays consistent with the leg set. Failure / panic / parse-error in the callback falls through to the previous distribution; the leg change itself isn't undone.

The callback runs under the same step-ceiling budget as `decide_route` (`DefaultMaxSteps` = 100k). A leg-change storm (e.g. mass transport failure on dmsg server outage) won't pin the read loop — each callback is bounded.

## Open questions

1. **Trace / audit storage.** Per the operator's direction, trace is deferred. But policies will eventually need an audit trail — "why did this dial in 2026-Q3 route through Singapore?" Needs to land *before* anyone uses policies for security-sensitive routing (e.g. "never route through country X"). Suggested: trace events land in the existing visor log pipeline with a `policy=true` tag, plus a ring buffer for the last N decisions readable via RPC.

2. **Versioning.** When the visor's stdlib gains new modules (or removes old ones), existing operator policies should keep working. Need a `starlark_compat=2026.05` style version tag in the policy file's header. Out of scope for v1 but worth flagging.

3. **Multi-script composition.** Should an operator be able to `load("./common.star")` shared helpers from their own policy file? Starlark supports this natively; cost is reading multiple files at policy-load time. Default: yes, allow within a single directory; out-of-directory loads rejected.

4. ~~**Layer 2 contract finalization.** What exactly is the distribution descriptor's vocabulary?~~ **Resolved** — see Phase 5 table above. Vocabulary is `""` / `"auto"` / `"round-robin"` / `"equal"` / `"weighted: f1, f2, ..."` / `"size-threshold: N"`. Unknown descriptors error at parse time so future additions stay unambiguous.

5. **Policy granularity vs. CLI flags.** Today's `--routes N --min-hops K` per-CLI-invocation overrides interact with policies how? Suggested rule: CLI flags supplant the policy's matching fields on that invocation; the operator's policy sees `ctx.cli_overrides` so they can choose to honor or override the flag.

## Decision checklist

For approval:

- [ ] Confirm Starlark as the Layer 1 language (vs. Lua, the runner-up).
- [ ] Confirm the two-layer model (Starlark Layer 1 + deferred Layer 2 distribution descriptor).
- [ ] Confirm per-app overrides global (single hierarchy, app wins).
- [ ] Resolve open questions 4 and 5 (Layer 2 contract + CLI/policy interaction) before phase 1 lands.
- [ ] Pick the v1 stdlib surface (currently: `datetime`, `transports`, `geo`, `peers`, `logging`).

Phases should not start until the above are settled.
