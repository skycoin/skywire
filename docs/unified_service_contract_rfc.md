# RFC: Unified Service Contract — Folding Visor Subsystems Into the App Framework

Tracking issue:
[#2775](https://github.com/skycoin/skywire/issues/2775).

Predecessor PRs (already merged):
[#2860](https://github.com/skycoin/skywire/pull/2860) phase 1 — name the `AppFunc` contract, add `RunMode` and `RestartPolicy` enums.
[#2861](https://github.com/skycoin/skywire/pull/2861) phase 2a — promote `setApp*` helpers onto `*app.Client`.
[#2862](https://github.com/skycoin/skywire/pull/2862) phase 2b — type `ProcConfig.RunFunc` as `AppFunc`.

Status: **Draft.** Scope was revised after the first round of discussion (see "Scope revision" below) — hypervisor is now out of Phase 3.

## Scope revision

The original `#2775` issue body included the hypervisor UI in the "make it an app" list. That decision was made before the `skywire web` surface was scaffolded. With the web PTY in the picture, the unification this RFC is really after — **uniform operator visibility and control** — gets delivered at the operator-interface layer, not at the supervisor layer.

**About the web PTY.** Mechanistically, `skywire web` is **the pty subsystem serving its pseudoterminal over HTTP/WebSocket** so the operator's browser can render the terminal. The skywire CLI runs inside that PTY as a normal interactive shell session. It is **not** a REST/RPC gateway exposing per-command endpoints — it is the same PTY mechanism that backs the existing dmsg-transport pty (`cli dmsg pty exec`), just rendered over a different transport. The security model inherits the pty subsystem's PK-based authentication; no new auth surface is introduced.

**Terminology rename.** The subsystem is conceptually a **pty (pseudoterminal)**. How it connects or serves and over what transport is a detail — dmsg today, HTTP via the web scaffold, potentially a local unix socket later. The current code identifier `dmsgpty` (package, config key, CLI subcommand) bakes the transport into the name; this RFC standardizes on **`pty`** for the concept and treats `dmsg` / `http` / `local` as **modes** (transports) of the same thing. The CLI surface for the standalone serves becomes `skywire app pty serve --transport=dmsg|http|local`; the existing dmsg-transport command tree (`skywire dmsg pty exec`) gains an `skywire app pty exec` alias path. The deeper code rename (`pkg/dmsgpty/` → `pkg/pty/`, `conf.Dmsgpty` → `conf.Pty` with backward-compat, etc.) is a follow-up PR — out of scope here; the rest of this RFC uses **pty** as the concept name and references existing identifiers like `pkg/dmsgpty/` only where pointing at code that hasn't been renamed yet.

The implications for this RFC:

- Any capability the `skywire` binary supports — hypervisor on/off, config edits, transport ops, RPC introspection — is reachable through the PTY by the operator typing the corresponding `skywire ...` command. No code change to expose new things; everything the CLI can already do is already there.
- The thing that originally made "hypervisor as an app" attractive was uniform visibility (`cli visor app ls` showing the full picture). The web PTY delivers that same uniformity *one level up*: the operator's single remote-control surface is the shell, and `cli visor app ls` is just one of many things they run inside it.
- Hypervisor stays as a visor subsystem with its existing init wiring. No lifecycle refactor inside critical-path code.

Phase 3 therefore shrinks to **dmsgweb, skynetweb, pty** — the three runtime-toggleable subsystems where the supervisor-side unification actually buys something. Total scope is ~150 LOC instead of ~500 LOC, and the riskiest piece (hypervisor's lifecycle refactor) is removed.

**pty's elevated role:** since pty is the mechanism backing the meta-surface, Phase 3.3 (migrating pty to an Internal app) is touching the very thing that delivers this RFC's "hypervisor stays as subsystem" argument. Still safe — pty stays running across the migration, just managed via the launcher afterward. But it means Phase 3.3 needs the same operator-regression care that hypervisor would have needed: don't ship pty's `RestartPolicy` enforcement (Phase 4) until 3.3 has burned in.

The web PTY scaffold is a prerequisite for this revision to hold. As long as `skywire web` ships before or alongside Phase 3, operators don't lose anything by hypervisor staying as a subsystem.

## Background

The skywire visor today supervises two distinct kinds of long-running things, with overlapping but incompatible lifecycle surfaces:

**Launcher apps** (`pkg/app/launcher`, `pkg/app/appserver`) — skychat, skysocks, skysocks-client, skynet, skynet-client, vpn-server, vpn-client. Either external child processes or in-process goroutines whose entrypoint matches `appcommon.AppFunc = func(ctx, args) error`. Lifecycle is owned by `appserver.ProcManager`; runtime control via `cli visor app start|stop|status|info` (RPC).

**Visor subsystems** — `hypervisor` (HTTP server), the `pty` host (currently `dmsgpty` in code; PTY listener), `dmsgweb` and `skynetweb` (the resolver SOCKS5 proxies). Started by entries in the `pkg/visor/init.go` module DAG; shutdown via `pushCloseStack`. Each holds direct references to visor-internal state (`v.tpM`, `v.dmsgC`, `v.router`, etc.). Runtime control surfaces are bespoke per-service: hypervisor has no on/off RPC at all, pty has none, resolvers have `EmbeddedProxies`.

Phases 1, 2a, 2b made the launcher-app contract explicit but did not bridge the two worlds. This RFC is about partially bridging — specifically for `pty`, `dmsgweb`, `skynetweb`.

## What unification still buys (post-revision)

With hypervisor out of scope, the wins are smaller but real:

1. **Uniform `cli visor app ls` view** for dmsgweb / skynetweb / pty alongside launcher apps. An operator inside the web PTY runs the same `cli visor app` subcommands as in a local terminal; the migration just adds these three to the list.
2. **Drop the bespoke `EmbeddedProxies` RPC** in favor of standard `cli visor app start|stop dmsgweb`. One less ad-hoc control surface.
3. **pty gains a runtime on/off switch** it doesn't have today. Operators who don't want PTY access exposed can disable it without restarting the visor (and re-enable for a maintenance window). The same control surface covers all pty transports — dmsg today, HTTP once the web scaffold ships.
4. **Supervision (`RestartPolicy`) becomes available** for these three when phase 4 lands. pty restart-on-crash is the most useful — the listener occasionally dies silently in the field.
5. **Construction site stays where it is for all three.** No init refactor; the AppFunc shim just wraps existing `Start()` / `Stop()` methods.

What unification does **not** buy:
- Hypervisor's bespoke startup stays. Operators still toggle it via `conf.Hypervisor.Enable` + visor restart (or via the web PTY once that ships, which can invoke the visor RPC to re-init it).
- Config sprawl is partially addressed: dmsgweb/skynetweb/pty get synthetic `apps[]` entries, but their `visorconfig.*` sub-structs stay (for the args).

## Non-goals

- **Not** about merging the pair binaries (`skynet-srv` / `skynet-client`, etc.) into mode-flagged singletons. The two halves of each pair share little code; the gain would be cosmetic. Treated as a separate, optional cleanup.
- **Not** about changing the `app.NewClient()` pipe-RPC machinery for *external* apps. External apps run in their own process and need that pipe.
- **Not** about migrating hypervisor. It stays as a visor subsystem; the web PTY is its meta-management surface.
- **Not** about deprecating the existing per-subsystem `visorconfig.*` sub-structs in v1. Migration must preserve operator config files.

## The asymmetry — facts on the ground

| | Launcher apps (A) | pty (B) | Resolvers (B) | Hypervisor (out of scope) |
|---|---|---|---|---|
| Entry | `AppFunc(ctx, args)` | `dmsgpty.Host.ListenAndServe()` (code-name still `dmsgpty`) | constructor → `Start()` | `NewHypervisor(...)` → `Enable(ctx)` |
| Construction site | `cmd/apps/*/commands/*.go` registers via `launcher.RegisterApp` | `pkg/visor/init_dmsg.go` / `init_dmsg_skywire.go` | `pkg/visor/embedded_dmsgweb.go`, `embedded_skynetweb.go` | `pkg/visor/init_apps.go:initHypervisor` |
| Visor-internal access | None — uses `app.NewClient()` RPC pipe | Full — uses `v.dmsgC` etc. | Full — uses `v.dmsgC`, `v.tpM` | Full — gets passed `v *Visor` |
| Supervision today | `ProcManager` (start/stop/status RPC) | None | None (toggle via `EmbeddedProxies`) | None |
| Config source | `conf.Launcher.Apps[]` | `conf.Dmsgpty` | `conf.DmsgWeb`, `conf.SkynetWeb` | `conf.Hypervisor` |
| Shutdown surface | `ProcManager.Stop(name)` | `pushCloseStack` on ctx-cancel | `pushCloseStack` | `pushCloseStack("hypervisor", hv.Disable)` |
| `cli visor app ls` visibility today | Yes | No | No | No |
| Per-subsystem RPC today | `apps/*` family | None | `EmbeddedProxies` only | None |

After Phase 3, the three middle columns gain the launcher-apps row's properties.

## Design

The shape is **app-shaped facades** — keep each subsystem's current construction in visor init, register a thin `AppFunc` shim that wraps the existing `Start()` / `Stop()` methods and blocks on `ctx.Done()`.

```go
// pkg/visor/embedded_dmsgweb.go (after Phase 3.2)
func initEmbeddedDmsgWeb(ctx, v *Visor, log) error {
    if !v.conf.DmsgWeb.Enable {
        return nil
    }
    // CONSTRUCTION only — no Start()
    v.embeddedDmsgWeb = newEmbeddedDmsgWeb(v.dmsgC, v.conf.DmsgWeb.ProxyPort, ...)
    launcher.RegisterApp("dmsgweb", v.runDmsgWebApp)
    return nil
}

func (v *Visor) runDmsgWebApp(ctx context.Context, args []string) error {
    if err := v.embeddedDmsgWeb.Start(); err != nil {
        return err
    }
    <-ctx.Done()
    return v.embeddedDmsgWeb.Stop()
}
```

A config translation layer (Phase 3.1) auto-appends `AppConfig{Name: "dmsgweb", AutoStart: true}` to `conf.Launcher.Apps[]` whenever `conf.DmsgWeb.Enable=true`, so operators don't have to edit their configs.

### Why this shape (not a separate Runnable interface)

The earlier draft of this RFC also discussed Option C — a separate `Runnable` interface that subsystems implement directly. With hypervisor out of scope, the case for Option C collapses:

- The pty/dmsgweb/skynetweb subsystems all have natural `Start()`/`Stop()` method pairs. The AppFunc shim wrapping those is trivial; promoting them to implement a new `Runnable` interface buys near-zero additional clarity.
- The "wart" of split construction-vs-start in Option B was painful mainly for hypervisor's already-tangled lifecycle. For the three simpler subsystems, the wart is invisible — construction is one line, start/stop are one line each.
- Maintaining a second supervisor contract (`Runnable`) alongside `AppFunc` adds surface for ~50 LOC of net benefit. Not worth it.

If hypervisor's lifecycle ever does come into scope later, revisit Option C then.

### Why not Option A (richer Client)

Option A — making the in-process `*app.Client` expose visor internals — was an attractive nuisance even when hypervisor was in scope. Without hypervisor, there's no remaining motivation for it. The three middle subsystems already access their needed state via Go-package-level visor methods inside their construction; they don't need a Client-mediated API.

## Migration plan

Four phases. Each lands as one PR. Each is independently revertable.

### Phase 3.1 — config translation layer

Add a `pkg/visor/visorconfig/synth_apps.go` pass that runs after `config.json` parsing. For each enabled subsystem block, append a synthetic entry to `conf.Launcher.Apps[]`:

```go
if conf.DmsgWeb.Enable {
    conf.Launcher.Apps = append(conf.Launcher.Apps, AppConfig{
        Name:      "dmsgweb",
        AutoStart: true,
        // args carry the bits operators previously set in conf.DmsgWeb
    })
}
// skynetweb, pty analogous
```

No subsystem code changes yet. The launcher gets new `apps[]` entries but no `RunFunc` is registered, so they fail-to-start with `ErrAppNotFound` — guarded by a feature flag (e.g. `conf.Launcher.SynthesizeSubsystems`) so the migration doesn't break boot. The flag flips to default-on once 3.2/3.3 land.

### Phase 3.2 — register dmsgweb + skynetweb as Internal apps

The lowest-risk subsystems. Each gets an `AppFunc` body in `pkg/visor/embedded_dmsgweb.go` (and `skynetweb`) that wraps the existing `Start` / `Stop` and blocks on ctx-cancel. `launcher.RegisterApp("dmsgweb", ...)` + `RegisterApp("skynetweb", ...)`.

The existing `initEmbeddedDmsgWeb` skips its auto-`Start` when the synthetic apps[] entry exists. The `EmbeddedProxies` RPC stays around as a thin shim that forwards to the launcher (deprecation deferred to a follow-up).

Verify operator workflow: `cli visor app start dmsgweb` brings the SOCKS5 up; `stop` brings it down. Behavior matches the current `EmbeddedProxies` toggle.

### Phase 3.3 — register pty as Internal app

Same shape. The pty listener (code-name `dmsgpty.Host` today) becomes the `AppFunc` body; the existing `init_dmsg_skywire.go:startSkywirePtyListener` is invoked from there instead of from init directly. The Internal app registered name is `pty` (not `dmsgpty`) so the future HTTP and local transports can register the same app name and the operator surface (`cli visor app start pty`) stays stable across the deeper rename.

Verify: `cli dmsg pty exec <pk> -- echo hi` still works. The remote-dial side is unaffected (different code path).

### Phase 3.4 — `cli visor app ls` cosmetic pass

Add a column or grouping that distinguishes "launcher app" from "internal service" in the listing, so operators can still tell at a glance what kind of thing they're looking at. Pure UI; can land last or in parallel with 3.2/3.3.

## Open questions

1. **Default RestartPolicy for the three subsystems.** Phase 4 (independent of this RFC) enforces `RestartPolicy`. The proposed defaults:
   - pty: `OnFailure` — listener occasionally dies silently in the field; restart is exactly what operators want.
   - dmsgweb / skynetweb: `Never` — these are user-driven (operator clicks "enable proxy"), no value in auto-restart.
   Confirm or adjust.

2. **Naming.** App names today collide with binary names (`skychat` is both the app and the executable). For the three subsystems with no binary equivalent, do we keep names like `dmsgweb` (matching the visorconfig key) or prefix them (`_service.dmsgweb`) to avoid future collisions with operator-installable apps named the same? Recommend keeping the bare names — operator-installable apps named "dmsgweb" would be a self-inflicted wound.

3. **Synthetic config entry visibility.** Should the synthetic `apps[]` entries appear in `skywire config gen` output? If yes, operators can edit them directly; if no, the `conf.DmsgWeb.Enable` flag remains authoritative. Recommend **no** for now — keep `conf.DmsgWeb` etc. as authoritative, synthesis happens at load time. Operators who really want to override pass args through the existing config block.

4. **Idempotent start/stop.** When `cli visor app stop dmsgweb` calls `Stop()`, should `cli visor app start dmsgweb` reuse the same instance or construct a fresh one? Current semantics: dmsgweb / skynetweb already cleanly stop-then-start the underlying SOCKS5 listener. The pty listener is more sensitive — the host keeps active sessions; stopping kills them. Recommend instance reuse for all three (simpler, matches today's runtime-toggle behavior for dmsgweb/skynetweb), and document the active-session loss for pty.

## Out of scope for this RFC

- Phase 4 (`RestartPolicy` enforcement). Independent change; can land before or after this RFC's phases.
- Hypervisor lifecycle. Stays as a visor subsystem; operators reach it through the web PTY (or any other CLI invocation).
- Pair-binary collapse (skynet srv+client, etc.). Cosmetic, low-value; not blocking.
- Full deprecation of `EmbeddedProxies` RPC. After Phase 3.2 ships and burns in, a follow-up can remove the shim.
- Web CLI itself. Tracked separately; is a prerequisite for this RFC's scope revision to hold.

## Decision checklist

For approval:

- [ ] Confirm the scope revision (hypervisor out, web PTY is the meta-surface).
- [ ] Pick default RestartPolicies per open question 1.
- [ ] Resolve naming convention per open question 2.
- [ ] Confirm synthetic-config-visibility direction per open question 3.
- [ ] Confirm instance-reuse semantics per open question 4.

Phases 3.1–3.4 should not start until the above are settled.
