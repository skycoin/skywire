# GUI embedding standardization — mount vs iframe

Status: draft / accepted-direction. Owner: skywire.

## Problem

Skywire ships a dozen distinct web GUIs written in four different stacks, glued
into the hypervisor UI (and the wasm-visor, which *is* the same Angular SPA
re-served) by three inconsistent mechanisms. The same UI often exists twice —
e.g. the network visualizer is both a 221-line Angular `vis-network` component
(`/nodes/visualizer`) **and** the full `pkg/tpviz` renderer (`/tp-viz/`). That
duplication is expensive to maintain and drifts (different features on each).

We want: **one implementation per UI**, presentable **standalone**, **inside an
SPA tab**, and **inside a WinBox window**, without gratuitous break-out server
paths (`/tp-viz/`, `/wallet/`) that leave the single-page context.

## Inventory (default build)

Grouped by stack:

- **Angular** (`static/skywire-manager-src` → `pkg/visor/static`, one SPA): the
  hypervisor UI and every native tab (network-visualizer, skychat, terminal,
  VPN, skynet, skysocks/web-proxy, rewards-tab, logs, routing, apps). Plus the
  **vendored skycoin Angular apps**: skycoin-web wallet (used, iframed into the
  wallet tab), skycoin node GUI (only in the `skycoin-skywire` binary), explorer
  (dead / unreferenced).
- **Vanilla TS/JS bundles**: **tpviz-legacy** (`pkg/tpviz/legacy/bundle.js`,
  three.js), **skychat standalone** (`cmd/apps/skychat/.../static/index.html`,
  fetch+SSE), **browse.js + WinBox** (`pkg/wasmhv/browseui`, the in-SPA virtual
  browser / mini-desktop), **pty terminal** (`pkg/pty/term.html.gz`, xterm.js).
- **Go server-rendered HTML** (`html/template`+gin): the **reward system UI**
  (`cmd/skywire-cli/commands/rewards/server`), proxied into the SPA at
  `/api/rewards/*`.
- **WASM**: **`skywire web`** (`cmd/skywire/commands/web`, TinyGo `b.wasm` CLI-
  tree browser), **tpviz-wasm** (`-tags tpvizwasm`), **wasm-visor** (its UI is
  the reused Angular SPA).
- **Non-web**: **systray** (fyne).

Three integration patterns exist today:

1. **Native Angular component** (most tabs) — single-context, but reimplemented
   in Angular (the duplication).
2. **`<iframe>` of a same-origin path** — the wallet (`/wallet/`), wallet-config,
   every WinBox `url:` window.
3. **Own path/port only** — tpviz `/tp-viz/`, pty `/pty/{pk}`, skychat-standalone,
   reward-server, `skywire web`.

## Decision rule

> **Use an iframe only when the embedded thing brings its own browsing context by
> necessity. Otherwise MOUNT it into a `<div>` (an SPA tab or a WinBox `mount:`
> window) in the host's own JS/DOM context.**

A WinBox window is **not** inherently an iframe — `browse.js` already uses both
`url:` (→ iframe) and `mount:` (→ plain DOM) windows. The real axis is
**mount-DOM vs iframe**, and WinBox can host either.

### Iframe is justified only for:

1. **Untrusted / foreign content** — arbitrary proxied `.dmsg` / `.skynet` /
   clearnet sites (the resolving-proxy / skysocks-client-lite browser). The
   iframe + `sandbox` *is the security boundary*. Keep.
2. **A self-contained runtime that can't cohabit the host context** — the reward
   server (server-rendered HTML), `skywire web` (own b.wasm runtime), and
   **skycoin-web** (a *second* Angular runtime — two Angular apps can't share one
   document cleanly). Iframe until/unless rewritten as a mountable module.

### Everything else mounts (no iframe):

First-party JS/TS bundles run in the host context via a `mount()` entry: **tpviz
visualizer, skychat, wallet-config, pty/xterm, browse.js**. Benefits: shared
auth/session, shared theme, direct data passing (no `postMessage`), no second
document fetch, no framework double-load.

## The `mount()` contract

Each embeddable UI's bundle exposes a global (or ES module export):

```ts
window.SkywireUI["<name>"] = {
  mount(root: HTMLElement, opts?: Record<string, unknown>): MountHandle,
};
interface MountHandle { unmount(): void; }
```

- `mount(root, opts)` builds all of its own DOM inside `root` (it must NOT depend
  on a pre-existing static `index.html` structure), wires events, starts data
  fetches, and returns a handle with `unmount()` for teardown.
- The **standalone** page becomes a thin shell: `<div id="root"></div>` +
  `SkywireUI["<name>"].mount(root)`. Same artifact, so standalone and embedded
  never drift.
- Styles are self-scoped (prefixed classes, or a shadow root) so mounting into
  the SPA doesn't leak CSS.
- Data endpoints stay same-origin (`/api/...`) so the mounted bundle works
  identically standalone, in a tab, and in a WinBox window.

An Angular side hosts it with one generic wrapper: load the bundle script once,
`mount(divRef.nativeElement)` on init, `unmount()` on destroy.

## Per-UI target

| UI | Target | Notes |
|---|---|---|
| network visualizer | **mount** (tpviz) | pilot; retire the Angular reimplementation |
| skychat | **mount** | standalone HTML → `mount()`; Angular tab hosts it |
| wallet-config | **mount** | already a vanilla page |
| pty terminal | **mount** | xterm.js into the tab/window |
| browse.js / WinBox | already mount | the host itself |
| resolving-proxy browser | **iframe** (keep) | untrusted foreign content |
| reward server UI | **iframe** | server-rendered; or convert later |
| `skywire web` | **iframe** | own b.wasm runtime |
| skycoin-web wallet | **iframe** (for now) | separate Angular runtime; converging it is a large separate effort |
| own Angular routes in WinBox (`?embed=1`) | **Angular portal** | stop iframing the SPA into itself |

## Migration order

1. **Pilot — network visualizer**: give `pkg/tpviz` a `mount()` entry; the
   Angular `network-visualizer` tab loads the tpviz bundle and mounts it into its
   `<div>` (no `/tp-viz/` iframe, no Angular reimplementation). Delete the
   vis-network component once at parity.
2. **skychat**: `mount()` entry for the standalone UI; Angular tab hosts it.
3. **pty terminal**, **wallet-config**: same contract.
4. **`?embed=1` WinBox windows** → Angular CDK portal.
5. ~~(Large, separate) **skycoin-web** convergence into the SPA as a lazy
   module.~~ **Superseded** (see the progress note below): skycoin-web stays
   iframed — a separate upstream-owned Angular app + the wallet custody boundary.

## Implementation findings (2026-08-04, from a full code inventory)

The pilot (network-visualizer → tpviz mount) is landed (#3709/#3713). Before the
rest can proceed, the SPA's structure imposes a **foundational prerequisite** that
this doc originally understated:

- **The SPA is a single monolithic NgModule** (`app.module.ts` declares ~169
  components/pipes/directives). There is **no `SharedModule`**, **no feature
  module**, and **every route is eager** (`app-routing.module.ts` uses `component:`
  everywhere; zero `loadChildren`/`loadComponent`). **Zero components are
  `standalone: true`** — including the shared bases `PageBaseComponent` and
  `TopBarComponent`.
- Because it's one module, every feature template implicitly sees every shared
  declaration. E.g. the VPN templates use `<app-button>`, `<app-dialog>`,
  `<app-top-bar>`, `<app-paginator>`, `<app-loading-indicator>`, `<app-line-chart>`,
  `<app-copy-to-clipboard-text>` and pipes `autoScale`/`dataFilterer`/
  `loadingBackendData`/`currentRemoteServer`/`name`/`translate`.

**Therefore lazy-loading ANY feature (VPN, skychat, …) is gated on first extracting
a `SharedModule`** that declares+exports the cross-feature UI components, pipes and
directives; then `app.module` imports it (and drops those declarations) and each new
lazy feature module (`loadChildren`) imports it too. This is high-blast-radius (the
whole UI depends on those shared pieces) and must be done carefully with iterative
`make build-ui` verification on all three surfaces (native HV, `hv serve`, wasm
visor — note `useHash:true` + lazy-chunk serving, `serve.go:189-193`).

**Recommended sequence:**
1. **SharedModule extraction** (prerequisite; own PR). Identify every declaration
   used by ≥2 features, move to `SharedModule`, verify no shared piece imports a
   feature component. Keep everything eager for this PR — pure refactor, no route
   changes — so it's independently verifiable.
2. **A generic external-bundle mount host** (`<app-bundle-mount [src] [globalName]>`)
   factored from `network-visualizer.component.ts` for the `window.SkywireUI[...]`
   contract above.
3. **A WinBox↔Angular CDK-portal bridge** (`window.SkywireNg.mountComponent(el,
   token, opts)`), so the `?embed=1` self-iframe WinBox windows (chat, logs —
   `browse.js:1590-1624`, `:1407-1429`) mount the real Angular component instead of
   iframing the SPA into itself.
4. **VPN → `loadChildren` feature module** (first lazy conversion; `loadChildren`
   "Lane A", not `loadComponent`, to avoid the standalone migration).
5. skychat WinBox → CDK portal; wallet `/wallet/config` iframe → mounted panel;
   then web-proxy/skysocks/skynet/logs lazy-load.

**Corrections to the "Per-UI target" table below:** skychat should **NOT** be
rebuilt as a vanilla `mount()` bundle — it's a rich 1500-line Angular component and
the WinBox window already reuses it via `?embed=1`; rebuilding it as a bundle would
re-introduce the dual-surface divergence that #3641/#3596 removed. Keep the one
Angular component and host it in WinBox via the CDK-portal bridge (step 3). The pty
terminal stays iframed (self-contained xterm+WS runtime, deliberately parked in
`NodeComponent` so its WS survives tab switches).

## Progress + findings (2026-08-05)

Steps 1, 2 and 4 have landed and were each live-validated (native HV + a fresh
`hv serve --harness` over CDP):

- **Step 1 — SharedModule extraction (#3715):** the ~22 layout components, the
  `autoScale` pipe and the `clipboard` directive now live in a `SharedModule`
  that also re-exports `CommonModule`, the forms modules, the common Material
  list, and the **bare** `RouterModule` / `TranslateModule` (root `forRoot`
  configs stay in `AppModule`, so future lazy modules share the single router +
  translate service).
- **Step 2 — generic `<app-bundle-mount>` host (#3716):** the script-load +
  `mount()`/`unmount()` lifecycle + loading/unavailable/error states, factored
  out of `NetworkVisualizerComponent`, which now delegates to it (tpviz mounts
  through it with the `?view=` deep-link preserved).
- **Step 4 — VPN lazy module (#3717):** the eight VPN components moved into a
  lazy `VpnModule`; `/vpn` uses `loadChildren`, shipping VPN as its own ~100 KB
  chunk fetched on demand (out of `main.js`). Confirmed the chunk loads on
  demand over `hv serve` and renders. Caveat: the single-file `hv gen` build
  can't serve dynamic-import chunks (documented in `serve.go`/`generate.go`), so
  `/vpn` degrades there — acceptable, since VPN is non-functional on a
  browser/wasm visor anyway.

**Steps 3 + 5 — skychat/logs WinBox windows now mount the real Angular component
(#3720).** The concern below turned out tractable: `SkychatComponent` and
`LogsComponent` both read only `node.localPk`, and neither injects
`ActivatedRoute`/`Router`/`NodeComponent`. So the decoupling is small — each gains
an optional `embeddedNodeKey` input (+ `embeddedPeer` for chat); when set (portal
mount) it synthesizes the node from the key instead of subscribing to
`NodeComponent.currentNode`, and the routed path is unchanged. A `NgBridgeService`
exposes `window.SkywireNg.mountComponent(el, name, opts)` (CDK `DomPortalOutlet` in
the root injector); `browse.js`'s chat/logs windows prefer it and fall back to the
`?embed=1` iframe if the bridge is absent. Validated live: ☰ Logs opens a WinBox
hosting `app-logs` with no iframe. The vanilla `wallet/config` page remains a
smaller future candidate for the `<app-bundle-mount>` host.

**skycoin-web stays iframed (not converged).** It is a separate, upstream-owned
(`skycoin/skycoin`) Angular application; merging two Angular runtimes into one
document is not viable, and the iframe is also the wallet's custody/isolation
boundary. Tighter integration, if wanted, is via the iframe (theme/session/
deep-link bridges) — not by converging the apps.

## Non-goals

- Rewriting server-rendered (reward server) or own-runtime (`skywire web`,
  skycoin-web) UIs — those stay iframed by the rule above.
- Changing the resolving-proxy browser — its iframe isolation is required.
