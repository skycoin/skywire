# RFC: Unified Service Contract — Folding Visor Subsystems Into the App Framework

Tracking issue:
[#2775](https://github.com/skycoin/skywire/issues/2775).

Predecessor PRs (already merged):
[#2860](https://github.com/skycoin/skywire/pull/2860) phase 1 — name the `AppFunc` contract, add `RunMode` and `RestartPolicy` enums.
[#2861](https://github.com/skycoin/skywire/pull/2861) phase 2a — promote `setApp*` helpers onto `*app.Client`.
[#2862](https://github.com/skycoin/skywire/pull/2862) phase 2b — type `ProcConfig.RunFunc` as `AppFunc`.

Status: **Draft.** Lays out the design space for unifying the supervised-service surface across launcher apps and visor-internal subsystems. Implementation deferred until the recommended path is agreed.

## Background

The skywire visor today supervises two distinct kinds of long-running things, with overlapping but incompatible lifecycle surfaces:

**Launcher apps** (`pkg/app/launcher`, `pkg/app/appserver`) — skychat, skysocks, skysocks-client, skynet, skynet-client, vpn-server, vpn-client. Either external child processes or in-process goroutines whose entrypoint matches `appcommon.AppFunc = func(ctx, args) error`. Lifecycle is owned by `appserver.ProcManager`; runtime control via `cli visor app start|stop|status|info` (RPC).

**Visor subsystems** — `hypervisor` (HTTP server), `dmsgpty` host (PTY listener), `dmsgweb` and `skynetweb` (the resolver SOCKS5 proxies). Started by entries in the `pkg/visor/init.go` module DAG; shutdown via `pushCloseStack`. Each holds direct references to visor-internal state (`v.tpM`, `v.dmsgC`, `v.router`, etc.). Runtime control surfaces are bespoke per-service: hypervisor has no on/off RPC at all, dmsgpty has none, resolvers have `EmbeddedProxies`.

Phases 1, 2a, 2b made the launcher-app contract explicit but did not bridge the two worlds. This RFC is about the bridge.

## Why bridge them

Concrete operator pain that survives the current design:

1. **No uniform "what services are running on this visor" query.** `cli visor app ls` lists launcher apps; the subsystems are invisible. Operators chase status across three sources (apps, EmbeddedProxies, "is hypervisor responding to HTTP").

2. **No uniform start/stop for subsystems at runtime.** Hypervisor and dmsgpty are bound to startup config — turning either off requires editing `config.json` and restarting the visor. Resolvers have ad-hoc `EmbeddedProxies` RPCs that nothing else uses.

3. **No supervision (restart-on-failure, panic recovery) for subsystems.** The launcher will eventually grow this via `RestartPolicy` (declared in #2860). Subsystems will not benefit unless they enter the same supervisor.

4. **Config sprawl.** Each subsystem has its own `visorconfig.*` sub-struct duplicating bits of what `apps[]` already expresses (enable, autostart, args, env). Apps and services drift in shape over time.

5. **Bespoke shutdown ordering.** `closeStack` is reverse-order; the launcher has its own stop semantics. Two ordering systems, two places to debug a stuck shutdown.

The architectural question this RFC asks: **can a single supervisor own all five categories, with one set of lifecycle, observability, and control surfaces?**

## Non-goals

- **Not** about merging the `skynet-srv` / `skynet-client` (and skysocks, vpn) pair binaries into mode-flagged singletons. The two halves of each pair share little code; the gain would be cosmetic. Treated as a separate, optional cleanup.
- **Not** about changing the `app.NewClient()` pipe-RPC machinery for *external* apps. External apps run in their own process and need that pipe; nothing in this RFC alters their boot path.
- **Not** about deprecating the existing per-subsystem `visorconfig.*` sub-structs in v1. Migration must preserve operator config files.

## The asymmetry — facts on the ground

| | Launcher apps (A) | Hypervisor (B) | dmsgpty (B) | Resolvers (B) |
|---|---|---|---|---|
| Entry | `AppFunc(ctx, args)` | `NewHypervisor(...)` → `Enable(ctx)` | `dmsgpty.Host.ListenAndServe()` | constructor → `Start()` |
| Construction site | `cmd/apps/*/commands/*.go` registers via `launcher.RegisterApp` | `pkg/visor/init_apps.go:initHypervisor` | `pkg/visor/init_dmsg.go` / `init_dmsg_skywire.go` | `pkg/visor/embedded_dmsgweb.go`, `embedded_skynetweb.go` |
| Visor-internal access | None — uses `app.NewClient()` RPC pipe | Full — gets passed `v *Visor` | Full — uses `v.dmsgC` etc. | Full — uses `v.dmsgC`, `v.tpM` |
| Supervision | `ProcManager` (start/stop/status RPC) | None | None | None (toggle via `EmbeddedProxies`) |
| Config source | `conf.Launcher.Apps[]` | `conf.Hypervisor` | `conf.Dmsgpty` | `conf.DmsgWeb`, `conf.SkynetWeb` |
| Restart-on-failure | Declared (not yet enforced — see #2775 phase 4) | None | None | None |
| Shutdown surface | `ProcManager.Stop(name)` | `pushCloseStack("hypervisor", hv.Disable)` | `pushCloseStack` on ctx-cancel | `pushCloseStack` |
| `cli visor app ls` visibility | Yes | No | No | No |
| Per-subsystem RPC | `apps/*` family | None | None | `EmbeddedProxies` only |

The hard fact: **launcher apps talk to the visor over RPC; subsystems hold direct visor refs.** Any unification must reconcile this.

## Design options

Three credible shapes for the unification. They differ in how the visor-internal access of subsystems gets reconciled with the RPC-distanced posture of launcher apps.

### Option A — Richer `Client`: plumb visor internals through an in-process app client

Extend `*app.Client` so that an **in-process** client (`RunMode: Internal`) has methods backed directly by visor functions, while an **external** client keeps the existing pipe-RPC surface. The contract stays `AppFunc(ctx, args) error`; the difference is that `c.TransportManager()`, `c.DmsgClient()`, `c.RouteManager()` exist when the proc is in-process and panic / return errors when external.

Subsystems become regular AppFuncs that call those new client methods.

**Pros.** One contract for everything. No new interface. The dispatch on `RunMode` already exists. Future "internal app" authors get a clean, discoverable surface.

**Cons.** The in-process `Client` becomes a god object exposing nearly the full visor surface. The "external client is the same type as internal client" property leaks dangerous methods at runtime (a launcher-app developer who wires up a hypervisor-shaped helper accidentally finds the visor's transport manager). Significant scope to define which visor internals are stable enough to expose.

### Option B — App-shaped facades: subsystems keep their init wiring, register thin `AppFunc` shims

Each subsystem keeps its current visor-init construction. We **additionally** register an `AppFunc` whose body is:

```go
func runHypervisor(ctx context.Context, args []string) error {
    hv := v.hvInstance.Load() // populated by initHypervisor
    if err := hv.Enable(ctx); err != nil { return err }
    <-ctx.Done()
    return hv.Disable()
}
```

The visor's init module **does not** auto-`Enable` anymore — instead, it appends a synthetic entry to `conf.Launcher.Apps[]` so the launcher's start-apps phase invokes it during normal startup.

**Pros.** Smallest change to subsystem internals — construction stays where it is. Operators get the uniform `cli visor app *` surface immediately. Existing `closeStack` ordering can keep dictating shutdown for subsystems if needed (they hand control to the launcher only for start/stop, not for ordering).

**Cons.** Lifecycle is split: construction owned by init, start/stop owned by the launcher. Two places to look for "where does X live?" Closing a subsystem via `cli visor app stop hypervisor` calls `hv.Disable()` but does not destruct — a re-`start` reuses the same instance, which may or may not be the right idempotency model for every subsystem.

### Option C — New `Runnable` interface, distinct from `AppFunc`

Declare a second contract specifically for visor-internal services:

```go
type Runnable interface {
    Name() string
    Run(ctx context.Context) error
}
```

`Runnable` is implemented directly by hypervisor, dmsgpty, the resolvers. The supervisor walks two registries — `launcher.AppFunc` registry and `runnable.Registry` — and presents them uniformly through `cli visor app *`. `AppFunc` is adapted via a wrapper that calls `Fn(ctx, args)` from `Run`.

**Pros.** Honest about the asymmetry — apps and subsystems get different contracts because they ARE different things. Subsystems keep their natural method receivers (`hv.Run(ctx)`) and visor-struct closures. Supervisor logic stays type-safe.

**Cons.** Two contracts to maintain. The `cli visor app *` surface has to abstract over both, which means either a discriminated union in the RPC reply ("kind = app | runnable") or a forced flattening that loses the distinction.

## Recommendation: B then C, never A

**Land Option B first** as the operator-visible unification. It's the smallest credible change that delivers the headline benefits (uniform start/stop/status, no more bespoke `EmbeddedProxies` RPC, visible service inventory). It does not require designing a stable "in-process Client" interface, which is the riskiest part of Option A.

**Promote to Option C in a later pass** once Option B has burned in for a release. The split between construction and start/stop in Option B is a debt that Option C can pay down — by then the supervisor's contract is well-understood and adding `Runnable` is mechanical.

**Reject Option A.** The "Client is the same type whether in-process or external" property is an attractive nuisance: it invites authors to write code that works in tests (in-process) and silently misbehaves in production (external), or vice versa. Discriminating at the type level (Option C) is cleaner than a runtime kind flag.

## Migration plan (Option B)

Five phases. Each lands as one PR. Each is independently revertable.

### Phase 3.1 — config translation layer

Add a `pkg/visor/visorconfig/synth_apps.go` pass that runs after `config.json` parsing. For each enabled subsystem block, append a synthetic entry to `conf.Launcher.Apps[]`:

```go
if conf.Hypervisor.Enable {
    conf.Launcher.Apps = append(conf.Launcher.Apps, AppConfig{
        Name:      "hypervisor",
        AutoStart: true,
        // args carry the bits operators previously set in conf.Hypervisor
    })
}
// dmsgpty, dmsgweb, skynetweb analogous
```

No subsystem code changes yet. The launcher gets new `apps[]` entries but no `RunFunc` is registered, so they fail-to-start with `ErrAppNotFound` — guarded by a feature flag so the migration doesn't break boot.

### Phase 3.2 — register dmsgweb + skynetweb as Internal apps

The lowest-risk subsystems: already runtime-toggleable, smallest blast radius. Each gets an `AppFunc` body in `pkg/visor/embedded_dmsgweb.go` (and `skynetweb`) that wraps the existing `Start` / `Stop` and blocks on ctx-cancel. `launcher.RegisterApp("dmsgweb", ...)` + `RegisterApp("skynetweb", ...)`.

The existing `initEmbeddedDmsgWeb` skips its auto-`Start` when the synthetic apps[] entry exists. The `EmbeddedProxies` RPC stays around as a thin shim that forwards to the launcher (deprecation deferred).

Verify operator workflow: `cli visor app start dmsgweb` brings the SOCKS5 up; `stop` brings it down. Behavior matches the current `EmbeddedProxies` toggle.

### Phase 3.3 — register dmsgpty as Internal app

Same shape. dmsgpty's listener becomes the `AppFunc` body; the existing `init_dmsg_skywire.go:startSkywirePtyListener` is invoked from there instead of from init directly.

Verify: `cli dmsg pty exec <pk> -- echo hi` still works. The remote-dial side is unaffected (different code path).

### Phase 3.4 — register hypervisor as Internal app

Highest blast radius — operators rely on this. Hypervisor's HTTP server becomes the `AppFunc`; `hv.Enable` is called from the `AppFunc`, `hv.Disable` on ctx-cancel.

Verify: the UI loads, the existing `cli visor halt` shutdown sequence still works, restart-loop binary swap behaves identically.

### Phase 3.5 — `cli visor app ls` cosmetic pass

Add a column or grouping that distinguishes "launcher app" from "internal service" in the listing, so operators can still tell at a glance what kind of thing they're looking at. Pure UI; can land last or in parallel with the others.

## Open questions

1. **Idempotent start/stop.** When `cli visor app stop hypervisor` calls `hv.Disable()`, should `cli visor app start hypervisor` reuse the same `*Hypervisor` or construct a fresh one? Current `closeStack` semantics imply teardown — switching to start/stop semantics may need each subsystem to grow a `Reset()` step.

2. **Synthetic config entry visibility.** Should the synthetic `apps[]` entries appear in `config gen` output? If yes, operators can edit them directly; if no, the `conf.Hypervisor.Enable` flag remains authoritative. There's a real tension between "make it discoverable" and "don't introduce duplicate sources of truth."

3. **Naming.** App names today collide with binary names (`skychat` is both the app and the executable). For subsystems with no binary equivalent, do we keep names like `hypervisor` (matching the visorconfig key) or prefix them (`_service.hypervisor`) to avoid future collisions with operator-installable apps named the same?

4. **Restart policy default for subsystems.** Phase 4 enforces `RestartPolicy`. Subsystems registered through this RFC default to which? `RestartOnFailure` matches operator intent ("the hypervisor should come back if it crashes") but means a crash loop won't surface as visibly as today's "the hypervisor is just gone."

5. **External-mode internal services.** A theoretically interesting capability: run hypervisor as a separate process speaking RPC to the visor, for OOM isolation. Option B doesn't preclude this but doesn't enable it either. Worth flagging if anyone has a use case.

## Out of scope for this RFC

- Phase 4 (`RestartPolicy` enforcement). Independent change; can land before or after this RFC's phases.
- Pair-binary collapse (skynet srv+client, etc.). Cosmetic, low-value; not blocking.
- Replacing `EmbeddedProxies` RPC entirely. Deprecation pass after Option B ships.
- Promoting to Option C. Separate RFC once Option B has shipped for a release.

## Decision checklist

For approval, the team needs to weigh in on:

- [ ] Pick Option B vs C vs another shape.
- [ ] Approve phased migration plan order (3.1 → 3.5).
- [ ] Resolve open question 1 (idempotent start/stop semantics).
- [ ] Resolve open question 4 (default RestartPolicy for subsystems).
- [ ] Confirm naming convention for open question 3.

Phases 3.2–3.5 should not start until all of the above are settled.
