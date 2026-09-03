# RFC: Align the skynet iframe browser with a real browser (real-origin transparent proxy)

> **Status: implemented, and not exactly as proposed below. Read this for the
> reasoning, not for the design.**
>
> The substrate this RFC argues for now lives in
> [github.com/0magnet/realorigin](https://github.com/0magnet/realorigin) and is
> imported by `pkg/visor/wasmserve.go`. What is current:
>
> - `docs/real-origin-browser.md` — how it actually works, including the origin
>   naming, which this document predates entirely.
> - `pkg/wasmhv/browse-bootstrap.html` — skywire's navigation shell.
> - `pkg/wasmhv/browse-transport.js` — the dmsg/skynet/skysocks transport.
>
> Three things below were built differently, and each is worth knowing before
> you trust a paragraph of it:
>
> 1. **Navigations are not intercepted.** §4b has the service worker handling
>    the top navigation as well as subresources. It does not: it returns without
>    calling `respondWith` when `req.mode` is `'navigate'`, and the server serves
>    the shell for every path but the worker's. A worker that served the first
>    page would have to be installed by a page it had not served yet.
> 2. **There is no cross-origin helper iframe.** §4b has V opening a hidden
>    helper on B. Storage Partitioning puts such a helper in a different
>    partition from V's SharedWorker, where it cannot reach the booted visor at
>    all. The responder is first-party on V instead.
> 3. **Origins are content-addressed.** This document assumes the target can be
>    encoded into the hostname. It cannot: a TLS wildcard matches exactly one
>    label, so no certificate covers a multi-label target. Origins are a
>    truncated hash of the target — see `docs/real-origin-browser.md`, which
>    records the schemes that were tried and rejected first.
>
> What remains accurate, and is why this file is kept: the problem statement in
> §1, and the argument that re-implementing the web platform inside a sandboxed
> `srcdoc` is inherently lossy.

## 1. Problem

The WinBox "skynet browser" does not proxy a browser — it **re-implements a
browser in JavaScript**. A top-level navigation fetches the HTML through a
backend call, parses it off-DOM, rewrites/inlines its subresources, injects a
CSP, and drops the result into an iframe as **`srcdoc` with
`sandbox="allow-scripts allow-forms"` and no `allow-same-origin`** — i.e. a
**null/opaque origin with no URL**. Every web-platform behaviour after that is
approximated in JS: `fetch`/XHR are monkeypatched, links/forms/history are
re-dispatched via `postMessage`, storage/cookies/serviceWorker are stubbed, and
subresources are pre-fetched and turned into `data:` URIs.

Re-implementing the web platform in a sandbox is inherently lossy. The bugs we
keep hitting are all facets of one architecture:

- `magnetosphere.net.<pk>.dmsg` → **`200`/`3xx` with 0 bytes**: the single-shot
  fetcher never follows the `Location` of Caddy's HTTP→HTTPS redirect, and the
  render guard only error-pages `>= 400`, so the empty redirect body renders as
  a blank page.
- `skycoin.com` → **`502` bursts**: an SPA's request storm maps 1:1 onto
  unbounded concurrent per-request dmsg/skysocks round-trips, and any failure in
  the bridge is turned into a synthetic `502`.
- WASM sites blank even though the CSP grants `wasm-unsafe-eval`: the site's own
  `.wasm`/bundle fetches don't complete faithfully through the bridge.

### Root divergences (ranked by how much breakage each explains)

1. **Opaque-origin sandboxed `srcdoc` instead of a real origin.** Master cause:
   no cookies, no `localStorage`/`sessionStorage` persistence, no service
   workers, `history.pushState` throws, no secure context, no cross-site
   isolation. Breaks every login/session/PWA.
2. **Request/response transcoding with no redirect following and lossy
   headers.** The bridge keeps only status + `Content-Type` + body; multi-value
   headers (`Set-Cookie`) collapse, `Location`/caching/`Content-Disposition` are
   dropped, MIME is guessed from the path extension when absent, 3xx is never
   followed.
3. **DOM/URL rewriting + `fetch`/XHR monkeypatching instead of a real network
   stack.** `import()`/ES-modules can't be intercepted, `srcset`/responsive
   images are dropped, streaming/range is impossible, `window.location`/popups
   break, forms can't do multipart/file upload.
4. **No WebSocket / SSE / streaming + `connect-src 'none'` fail-closed CSP.**
   Kills all real-time apps.
5. **Unbounded fan-out over per-request streams with synthetic 502-on-error.**
   The fragility on asset-heavy pages.
6. **No dmsg TLS-MITM analog; the `https` scheme is cosmetic** for dmsg sites.

Divergences #1–#2 cannot be fixed by patching the transcoder — they are the
transcoder.

## 2. The reference is already in the tree: `pkg/dmsgweb`

`dmsgweb` is a **transparent SOCKS5 splice**, not a transcoder (`runtime.go`
package doc: "raw TCP forwards bytes transparently and works for HTTP,
WebSockets, server-sent events, chunked transfers, and any other protocol").
A real browser pointed at it "just works" because:

- it **resolves `.dmsg`/`.skynet` to `127.0.0.1`** so the browser issues normal
  requests (and non-mesh TLDs also map to loopback to prevent DNS leaks);
- `Dial` returns a **raw dmsg stream** as the SOCKS tunnel — the browser's own
  HTTP/1.1 bytes (its cookies, redirects, keep-alive, range, `Upgrade`) flow
  through untouched;
- name-based backends get a **`Host` rewrite on the real byte stream**;
- **`CONNECT` on :443 is TLS-MITM'd** with a locally-trusted leaf cert, so
  `https://…dmsg` is a real secure context.

The browser stays the browser; skywire only moves the transport. That is the
behaviour we want inside the WinBox app too.

## 3. Design principle

> **Proxy the transport; don't re-implement the browser.**
> The iframe must load from a **real origin** served by a transparent proxy that
> streams responses **verbatim** (status + all headers + body, redirects,
> chunked/streaming, content-types). Subresource loading, redirects, cookies,
> WASM, streaming, and relative-URL resolution become the **browser's** job —
> correct by construction — and skywire supplies only the mesh transport.

An in-page iframe can't be given a SOCKS proxy (that's browser-global — which is
exactly what Waterfox does and what we just proved works). So we give it a real
origin backed by a transparent proxy, per surface:

## 4. Proposed architecture

### 4a. Native visor (:8000): local reverse-proxy origin

Expose a reverse proxy that is an **origin the iframe points at**, reusing
`dmsgweb`'s resolver + dialer + TLS-MITM rather than duplicating them:

- Per-site origin via **`*.localhost` subdomains**:
  `http://<pkslug>.mesh.localhost:<proxyport>/<path>` (browsers resolve any
  `*.localhost` to loopback **and** treat each subdomain as a **distinct
  origin**). This restores per-site cookie/storage/CORS isolation that the
  current single-opaque-origin model destroys.
- The iframe's `src` is that real URL. The browser fetches **every** subresource
  itself; those requests hit the same reverse-proxy origin and are forwarded
  over dmsg/skysocks. **Zero DOM/URL rewriting.**
- Response is streamed **verbatim** — status, all headers, body — so redirects,
  `Set-Cookie`, content-types (incl. `application/wasm`), chunked/SSE all pass
  through. `Location` on a same-mesh redirect is rewritten host→`*.localhost`
  subdomain (or the resolver maps it) so the browser follows it back through the
  proxy.
- HTTPS mesh sites: the reverse proxy does the **TLS-MITM upstream** (reusing
  `dmsgweb`'s MITM + the existing `~/.skywire/resolver` CA) and serves the
  iframe locally; the site is a real secure context.

Net: cookies, storage, service workers, history, WASM, streaming, WebSockets,
forms (incl. file upload) all work, because the browser is doing them.

### 4b. Wasm visor (:8443 / standalone): service worker as the network layer

The wasm visor's dmsg client lives **in the browser** (Go/wasm in the visor
app), not in a privileged Go process, so the transport must be reached from the
browser. The isolated origin is achieved with the **same `*.mesh.localhost`
scheme as the native case** — the difference is only *who fulfils the fetch*:

**Origins (one listener, Host-routed):** `wasmserve` already runs an HTTP(S)
server for the visor app. Serve two origin classes off the same listener:

- `localhost:<port>` (+ the real host) = the **visor app origin `V`** — holds the
  wasm dmsg client and the identity key.
- `<pkslug>.mesh.localhost:<port>` = the **browse-frame origin(s) `B`** — one
  distinct origin per mesh site. `wasmserve` serves only a tiny bootstrap page +
  the Service Worker for these; the SW answers everything after install.

`B ≠ V` (different host) makes them **cross-origin**, so a mesh site can never
reach the visor's origin, storage, or key. Per-site `<pkslug>` subdomains also
isolate sites **from each other** (separate cookies/storage). Both are served on
the *same TCP port* — the browser sends distinct `Host` headers and the browser
treats each as a distinct origin; no second listener needed.

**The SW as the transport:** the SW scoped to `B` intercepts the top navigation
**and every subresource** and fulfils each via the in-wasm dmsg/clearnet fetch,
returning a `Response` streamed **verbatim** (status + headers + body,
`Content-Type` preserved, `redirect:"manual"` followed by re-issuing through the
proxy). The browse iframe's `src` is a real URL
(`https://<pkslug>.mesh.localhost:<port>/<path>`); the browser does native
subresource loading, and the site gets real cookies/storage/history/WASM/
streaming — isolated from `V` and from other sites.

**Wiring the SW → in-wasm fetch (the crux):** a SW on origin `B` cannot postMessage
across to origin `V`. So establish the channel **out-of-band, once per `B`
origin**, without involving any untrusted site document:

1. The visor app `V` opens a **hidden helper iframe** on origin `B` (a page we
   control, served by `wasmserve`).
2. `V` ↔ helper(`B`) do a `postMessage` handshake over a `MessageChannel`; the
   helper **transfers one `MessagePort` to `B`'s Service Worker**
   (`navigator.serviceWorker.controller.postMessage(port, [port])`).
3. The SW now holds a private port to `V`'s wasm. For each intercepted request it
   sends `{method, url, headers, body}` over the port; `V`'s wasm runs the
   existing `fetchDmsg`/`fetchClearnet` and returns
   `{status, headers, body}` (transferred `ArrayBuffer`); the SW builds the
   `Response`.
4. The port carries **only the transport capability**, never the identity key,
   and is held by the SW — not exposed to any site document. The untrusted site
   (later loaded as the top document of `B`) shares origin `B` with the SW but
   has no handle to the helper or the port; it can only *cause* fetches, which is
   the intended behaviour.

**Secure context / mixed content:** Service Workers require a secure context.
`*.localhost` is treated as potentially-trustworthy (secure) by Chromium and
Firefox, so `http://<pkslug>.mesh.localhost` alone is SW-eligible. But when the
visor app `V` is served over **HTTPS** (`hv serve --tls`), an `http://` `B`
iframe is mixed-content-blocked — so `B` must also be HTTPS. Extend
`wasmLocalhostTLSCert` to add a `*.mesh.localhost` SAN (and `mesh.localhost`) so
the one self-signed cert covers both `V` and every `B`. When `V` is plain HTTP
(no `--tls`), `B` is plain HTTP too and no cert work is needed.

**Hosting modes — the `B`-origin suffix is a DEPLOYMENT PARAMETER, not `.localhost`.**
`*.localhost` resolves to loopback with no DNS, so `<pkslug>.mesh.localhost` only
reaches a server running on the *user's own machine*. That holds for a local
`wasmserve` (`hv serve` / `wasm_serve` on `:8443`) and for the native visor
(always local), but NOT when the wasm visor is fetched from a remote host (e.g.
`theskywirenetwork.net`) — there is no co-located listener. The SW + helper-iframe
`MessagePort` bridge is identical in every case (cross-origin `postMessage`/port
transfer works regardless of where the origins live); only where `B` is served
changes, so the suffix is configured per deployment:

- **Local** (`wasmserve` on the machine, or native visor): `B = <pkslug>.mesh.localhost`
  → loopback → the co-located listener.
- **Remote, wildcard subdomain (443-friendly, preferred):**
  `B = <pkslug>.mesh.<publichost>`, needing wildcard DNS `*.mesh.<publichost>` +
  a wildcard TLS cert; the server serves only the SW bootstrap there (it does NOT
  proxy dmsg — the SW relays to `V`'s in-tab wasm). Keeps per-site isolation, works
  over 443 (CDN-compatible).
- **Remote, second port on the same host:** `B = <publichost>:<port2>` — cross-origin
  from `V` (port is part of origin), and the existing host cert already covers it
  (certs are host-scoped, not port-scoped), so no wildcard DNS. Cheaper, but loses
  *inter-site* isolation (all sites share one `B`) and needs a non-443 port (not
  CDN-friendly).
- **Shared browse origin (alternative):** one skywire-operated `*.mesh.<shared>`
  used by any deployment; removes per-deploy wildcard setup at the cost of
  centralizing (and having to trust/pin) the host serving the SW code.

The native reverse-proxy (§4a) is unaffected — the native visor is always the
local daemon, so loopback is always correct there; this only concerns the wasm
surface's hosted mode.

**Residual limitations (documented, far smaller than today's):** the site's *own*
service worker can't nest under ours; and environments with no `B`-origin at all
(`file://` single-file HV, or a static host with neither a wildcard subdomain nor
a spare port) fall back to the transcoder (§6). Per-site subdomain isolation can
ship in a second step (v1 may share one `B` origin for simplicity, still
protecting `V`).

## 5. Security / isolation trade-off

- **Today:** opaque `srcdoc` = strong isolation, broken platform. The site can't
  persist anything or reach the visor — but also can't function.
- **Proposed:** a **real, per-site, isolated origin** = correct platform + real
  origin isolation *between mesh sites*. The site gains real cookies/storage
  (which is the point — logins work), so the design must guarantee the site's
  origin can **never** reach the visor's origin or secret key: separate origin
  (distinct `*.localhost` subdomain / distinct SW origin), no shared storage,
  `frame-ancestors`/`X-Frame-Options` on the visor origin, and the reverse proxy
  refusing to proxy the visor's own control endpoints.

## 6. What we keep

The current transcoder is retained as an explicit **fallback** for environments
with no proxy origin available — the `file://` single-file hypervisor, or a
browser where SW/`*.localhost` isn't usable. Feature-detect and prefer the
real-origin path; fall back to transcoding with a visible "compatibility mode"
indicator.

## 7. Divergence inventory → how the new model resolves it

| Current mechanism (approximation) | Real-origin model |
|---|---|
| Opaque `srcdoc`, no origin | Real per-site origin (`*.localhost` / isolated SW origin) |
| `window.fetch`/XHR monkeypatch | Native fetch/XHR through the proxy origin |
| `<script>`/`<link>`/`<img>` pre-inline as `data:` | Native subresource loading (streamed) |
| `srcset` dropped, images lazy-swapped | Native responsive images |
| ES-module `import()` uninterceptable | Works (native) |
| Form serialize→re-fetch, no file upload | Native form submit incl. multipart |
| History as parent-side stack; `pushState` swallowed | Native history / SPA routing |
| storage/cookie/serviceWorker stubs | Real storage/cookies; SW is *ours* (transport) |
| MIME guessed from extension | Upstream `Content-Type` preserved verbatim |
| No redirect follow → blank pages | Browser follows redirects natively |
| `connect-src 'none'`, no WS/SSE | Native WS/SSE/streaming through the tunnel |
| Unbounded fan-out → 502 bursts | Browser's own connection management + a bounded transport pool |
| base64 body, 16 MiB cap | Streamed bytes, no cap |

## 8. Migration path

1. **Native, behind a flag:** implement the reverse-proxy origin
   (`dmsgweb`-derived) + `*.localhost` per-site origins; point the WinBox iframe
   at real URLs; keep the transcoder as fallback. Validate against the
   Waterfox+resolving-proxy reference (`cmd/wfdrive`).
2. **Wasm:** SW network layer on an isolated origin; same validation.
3. **Flip the default** to the real-origin path; transcoder only as fallback.
4. **Delete** the redundant approximation code paths once parity holds.

Throughout: **reuse `pkg/dmsgweb`** (resolver, dialer, TLS-MITM, host rewrite) —
the reverse proxy is a thin re-framing of the forward proxy, not a re-write.

## 9. Open questions / risks

- `*.localhost` origin behaviour across OSes/browsers (Chromium + Firefox both
  map `*.localhost`→loopback and isolate origins; confirm on the target set).
- Per-site local TLS: wildcard/on-the-fly leaf issuance from the local CA for
  `https://<pkslug>.mesh.localhost`.
- Wasm isolated-origin problem — **resolved in §4b** via `*.mesh.localhost`
  Host-routed origins + a SW whose transport port is wired from `V` through a
  hidden helper iframe. Residual risks: robustness of the SW↔`V` port handshake
  across reload/SW-restart (re-wire on `controllerchange`); per-origin SW
  registration cost if per-site subdomains are used from v1; `*.mesh.localhost`
  SAN on the self-signed cert when `V` is HTTPS.
- Keeping the visor identity/control surface unreachable from the site origin.
- Performance: streaming vs. today's base64 (strictly better), and a **bounded**
  transport pool to replace unbounded fan-out.
- The `cmd/wfdrive` (Waterfox/BiDi) + `cmd/hvinspect` (Brave/CDP) rigs give us a
  real-browser oracle to diff the WinBox browser against during migration.
