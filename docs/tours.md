# The two tours

Skywire ships two guided walkthroughs, because it has two front ends and they
stopped being the same thing some time ago.

| | hypervisor-UI tour | desk tour |
|---|---|---|
| covers | the Angular dashboard | the desktop around it |
| lives in | `static/skywire-manager-src/src/assets/tour/hv-tour.js` | `pkg/wasmhv/deskhost/desk_tour_js.go` |
| written in | dependency-free JS, no Angular code | Go, as a desk app |
| opened from | the **?** button, bottom-right | the launcher, app `tour` |
| shows itself | once, on first run (`localStorage`) | never; you ask for it |
| mechanism | dims the page, spotlights one element | opens the app each step describes |

## Why two

There used to be one. It was `startTour()` in `pkg/wasmhv/browseui/browse.js`,
it filtered its own steps with `nativeOnly` / `wasmOnly`, and that worked
because both surfaces were the same Angular DOM — a step could anchor to
`th.dmsg-column` and hit it on a native hypervisor and a browser visor alike.

That file left in #4404, when browse.js moved to `github.com/0magnet/netscrape`,
and netscrape then retired its JS engine in favor of a Go/wasm browser. The
tour went with it and nothing has shipped one since 2026-09-02.

Rebuilding it as a single tour is no longer possible, and not because of where
the code went. The two surfaces no longer share a DOM, a language, or a subject:

- the **dashboard** is an Angular app about *visors* — one visor's transports,
  routes, apps and logs, and the mesh-wide views over all of them;
- the **desk** is a window manager about *this tab* — its browser, its shell,
  its filesystem, its key, its mailbox.

One step array addressing both would be two step arrays wearing one coat.

## The seam

The seam is a single sentence: **the desk renders the dashboard as a browser
tab.**

The dashboard is an Angular app the visor serves on its virtual loopback. The
desk's browser opens it at `vnet:8001` and `netscrape.DirectLoader` makes that
tab render *natively* rather than through the transcoding sandbox, because an
Angular app needs its same-origin (see the comment at `desk_js.go`, around the
`DirectLoader` assignment). So the dashboard is inside the desk, and the desk's
tour and the dashboard's tour are one click apart — each ends by naming the
other.

What each side owns:

**Desk** (`pkg/wasmhv/deskhost` + `0magnet/desk` + `websh` + `netscrape` + vnet)
— the window manager and taskbar; `browser` (mesh and clearnet, real-origin
rendering); `console` and `terminal` (websh, and a host pty where a host serves
the desk); `files` (the shared jsfs the shell sees); `mail`; `identity`;
`pair`; `settings`; `install`.

**Angular hypervisor UI** (`static/skywire-manager-src`) — the visor list;
a visor's own tabs (info, transports, routing, bandwidth, apps, uptime, logs,
settings, and the rest); the mesh-wide tabs (transports, network, visualizer,
services health, uptime, and — native only — rewards and resources); the
hypervisor-wide settings page.

## Native vs. wasm, inside the hypervisor-UI tour

The dashboard is *one* build served two ways, so its tour keeps the per-platform
split the old one had. `detectMode()` mirrors `isWasmHvCore()` in
`app/utils/home-tabs.ts`: `window.__SKYWIRE_HV__.visor` or `.standalone` means
an in-tab wasm core; a native build leaves the global unset; and `.pk` is a
remote viewer of a *native* hypervisor, so it reads as native. A step's copy may
be a string, `{wasm, native, both}`, or `fn(mode)`, and `nativeOnly` /
`wasmOnly` drop a step entirely rather than skipping it at render time, so the
"N / total" counter stays honest.

Two steps are `nativeOnly`, matching the two tabs `homeTabsData()` hides on a
browser core: **Rewards** (a tab that vanishes when you close it cannot hold up
an uptime record) and **Resources** (a browser tab has no host to measure).

## Gotchas worth keeping

- **Anchor dmsg and transport steps to the count cell *or* the column header**,
  in that order — `".dmsg-counts, th.dmsg-column"`. A browser visor's dmsg
  sessions flap to zero, the cell then renders `-` with no `.dmsg-counts`, and a
  cell-only step would silently skip. `pickTarget` walks the comma list in
  priority order for exactly this reason; `querySelector` would pick by document
  order instead.
- **A missing target is not an error.** Tabs differ between builds, so after an
  8-second deadline the step renders centred with no spotlight rather than
  stalling the tour.
- **The desk tour closes only what the reader has not touched.** `windowMark`
  records position, size and the min/max/full flags when a step opens a window;
  if any of it changed, the window has been adopted and the tour leaves it open.
- **Address the visor through `#/nodes/local`, not a PK scraped from the DOM.**
  A hypervisor manages many visors, and the first `/nodes/<pk>/` link in the page
  is whichever row sorted first — the first draft said "your visor" over somebody
  else's for eleven steps. The local route is the router's own answer to which
  visor is mine; the redirect puts that PK in the hash and the later steps read
  it from there.

- **`localStorage` can throw**, in a private window or with site data blocked.
  Every read and write of the seen-flag is wrapped, and a throw means "do not
  nag", not "show it again".

## What was not carried over

The old tour spliced **synthetic visors** into the list (`demo-frankfurt-hv`
and `demo-paris-visor`) so a solo browser visor could show what a managed
cluster looks like, by wrapping the wasm core's `hvApi` bridge. That is a
genuinely good demo and it is not in this rewrite: it is a wasm-core data-bridge
feature rather than tour text, and it belongs in its own change.
