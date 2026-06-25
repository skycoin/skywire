# Visor-core convergence: one assembly, two shells

## Problem

There are two implementations of "assemble a skywire visor from its subsystems":

- **Native** — `pkg/visor`, a concurrent module DAG (`init.go` → `init_dmsg.go`,
  `init_transport.go`, `init_router.go`, `init_apps.go`, …). Assumes a filesystem,
  OS processes (app launcher, dmsgpty), an HTTP API server, system networking.
- **Browser** — `cmd/wasm-visor` (`bootEdge`), a linear hand-assembly that bypasses
  `pkg/visor` because `pkg/visor` cannot compile/run under js/wasm.

Both build the **same subsystems** with the **same constructors and types**
(`dmsgc.New`/`direct.StartDmsg`, `transport.NewManager`, `router.New`,
`appserver.NewProcManager`), but via **divergent wiring code** and **divergent
config sourcing**. That drift is not hypothetical: PR #3277 (a multi-session
debugging effort) was caused by exactly this — the wasm dmsg setup served before
installing the registering-fallback discovery, while the native path installs it
before serving. Two assemblies of one subsystem drifted on an ordering invariant.

## Goal

A single, platform-neutral **`pkg/visor/visorcore`** package that holds the
assembly logic. `pkg/visor` becomes "core + OS/server shell"; `cmd/wasm-visor`
becomes "core + browser shell." Neither re-implements the core. Config flows
through one resolver so it cannot diverge either.

Feasibility confirmed: `visorconfig`, `pkg/transport`, `pkg/router`,
`pkg/app/appserver`, and `deployment` **all compile under `GOOS=js GOARCH=wasm`**,
so the core can use the real `visorconfig.V1` types.

## What's shared vs platform-specific

| Subsystem | Shared (→ visorcore) | Platform-specific (injected via deps) |
|---|---|---|
| dmsg client | NewClient → set transport → Serve → Ready → **register** (the #3277 invariant) | how the disc client is built: native = clearnet/dmsg HTTP over the full server list; browser = seeded WS + dmsg-HTTP upgrade |
| transport mgr | `NewManager` + `ManagerConfig` + `Serve` | which `InitClient` network types run (native: STCP/STCPR/SUDPH/QUIC/DMSG; browser: WS/WT/WEBRTC dial-only) |
| router | `router.New` + `router.Config` (RouteFinder, RouteGroupDialer, SetupNodes, MinHops, AwaitSetupListener) | route-source build tags; embedded RSN (native only) |
| proc manager | `NewProcManager` | ServerAddr (native: TCP; browser: "" = net.Pipe), app launcher (native only) |
| config | **`ResolveServices(v1) Services`** — the one place `pick(config, deployment-default)` lives | where the V1 comes from: native = file; browser = built from `deployment.Prod` |

Native's config = `visorconfig.V1` (operator overrides) merged with
`deployment.Prod.*` (embedded defaults) via scattered `pick()` calls across
`init_*.go`. Browser reads `deployment.Prod.*` directly. Convergence = both go
through `visorcore.ResolveServices`.

## Incremental plan (each step a separate, independently-validated PR)

1. **`visorcore.ResolveServices`** — a `Services` struct (resolved dmsg servers,
   dmsg/clearnet disc URLs, TPD, route-finder, setup nodes, STUN, AR) + a resolver
   that merges a (nullable) `*visorconfig.V1` with `deployment.Prod`. Adopt in
   `cmd/wasm-visor` first (replace direct `deployment.Prod.*` reads). Low risk:
   new package + one wasm consumer; validate the tab still boots/registers/fetches.
2. **`visorcore.StartDmsg`** — the register-before-serve starter (NewClient →
   `setTransport` hook → Serve → Ready). `StartDmsgSeeded` and `pkg/dmsgc` + 
   `init_dmsg` both call it, so the #3277 ordering invariant lives in one place.
3. **`visorcore.BuildTransportManager` / `BuildRouter` / `BuildProcManager`** —
   extract the construction (not the platform-specific InitClient set). wasm-visor
   adopts; then a native `visor_core` module delegates to them.
4. **Native adoption** — migrate `pkg/visor` `init_*.go` `pick()` calls to
   `ResolveServices`, and the construction to the `Build*` helpers, one module at a
   time. `pkg/visor` is production-critical → smallest possible diffs, each tested.
5. **Config alignment** — `cmd/wasm-visor` consumes a real (minimal) `visorconfig.V1`
   instead of `deployment.Prod` directly, so both sides share the config shape.

## Sequencing note

This must land **before** the Angular HV UI → SPA work. The SPA needs a
backend-abstraction layer (REST for native, in-tab JS for wasm); building it on a
still-divergent visor would bake the divergence into the UI layer too. Converge
the core first, align config, then unify the UI.

## Risk / non-goals

- NOT making all of `pkg/visor` compile under wasm (multi-month fight with OS
  assumptions, little gain). Only the platform-neutral core is shared.
- `visorcore` must stay lean: it may import `pkg/dmsg`, `pkg/transport`,
  `pkg/router`, `pkg/app/appserver`, `deployment`, `visorconfig` — but NOT
  `pkg/visor` (which pulls in launcher/dmsgpty/OS code), or the wasm build breaks.
  A `GOOS=js GOARCH=wasm go build ./pkg/visor/visorcore/` gate enforces this.
