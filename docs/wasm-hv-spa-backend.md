# One Angular HV UI, two backends: a clean SkywireHttpBackend

## Goal

Let the **single** Angular hypervisor UI (`static/skywire-manager-src`) run unchanged
against either backend:
- **native** — the visor's REST API over real HTTP (`/api/*`, session cookie + CSRF);
- **in-tab wasm** — the serverless wasm-visor's in-browser gateway
  (`globalThis.skywireVisor.hvApi`), no server, no network for control traffic;
- **dmsg-remote viewer** — `/api/*` fetched over dmsg to a remote hypervisor PK.

This is what makes the wasm-visor able to *substitute for* the native HV UI rather
than ship a separate harness.

## Current state (audit)

- All backend traffic funnels through **one seam**: `ApiService.request()` →
  `this.http.request(method, apiPrefix + url, {... withCredentials ...})`
  (`src/app/services/api.service.ts`). ~10 feature services (node/apps/transport/
  route/auth/dmsg/reward/health…) depend on `ApiService`; none touch `HttpClient`
  directly except `VpnClientService` (one external-IP probe).
- Auth = session cookie (`withCredentials`) + a CSRF token fetched per write from
  `GET /api/csrf`. **No `HttpInterceptor`s** exist.
- The wasm path today is **`pkg/wasmhv/override.js`** — a classic `<script>` loaded
  before Angular boots that **monkey-patches `window.fetch` and `window.XMLHttpRequest`**,
  sniffs same-origin `/api/*` calls, and routes them (per a `window.__SKYWIRE_HV__`
  config) to `skywireVisor.hvApi` (in-tab visor), `skywireDmsg.hvApi` (standalone),
  or `skywireDmsg.fetch(pk,…)` (remote viewer); also serves inlined assets. Works,
  but it's below Angular — invisible to DI, untestable, fragile against XHR/fetch
  changes.

## Design: a swappable `HttpBackend` (Angular-native, replaces override.js)

Angular 21 lets us provide a custom `HttpBackend` (the bottom of the `HttpClient`
pipeline, below interceptors). `ApiService` and every feature service stay
**unchanged** — they keep calling `HttpClient`; only the transport at the bottom
swaps.

```
HttpClient → (interceptors, none) → HttpBackend
                                      └─ SkywireHttpBackend.handle(req)
                                         ├─ native mode      → delegate to HttpXhrBackend (real HTTP)
                                         ├─ in-tab wasm mode  → skywireVisor.hvApi(method, path, body)
                                         ├─ standalone mode   → skywireDmsg.hvApi(method, path, body)
                                         └─ remote viewer     → skywireDmsg.fetch(pk, method, path, body, headers)
```

`SkywireHttpBackend implements HttpBackend`:
- reads the mode once from `window.__SKYWIRE_HV__` (the same config object
  override.js reads), defaulting to **native** when unset (so the normal
  visor-served build is unaffected);
- **native**: `return this.xhr.handle(req)` — zero behavior change;
- **wasm/standalone/remote**: translate the `HttpRequest` → `(method, pathFromUrl,
  bodyString)`, await the gateway promise (`{status, body:Uint8Array, headers}`),
  and emit a single `HttpResponse` (decode body, set status/headers/url). Map
  non-2xx to `HttpErrorResponse` so `ApiService.errorHandler` (401 → /login) still
  works;
- **assets**: a request that isn't `/api/*` (i18n JSON, images) delegates to the
  XHR backend — the wasm-visor serves the bundle + assets normally, so no special
  asset-inlining path is needed (drops that override.js complexity).

Provide it via `provideHttpClient(withInterceptorsFromDi())` +
`{ provide: HttpBackend, useClass: SkywireHttpBackend }`. One build; the mode is a
runtime property, so the same `pkg/visor/static` bundle works native, and the
wasm-visor/generator just sets `window.__SKYWIRE_HV__` before bootstrap.

## CSRF & auth across modes

- **native**: unchanged (cookie + `/api/csrf`).
- **wasm/standalone**: the in-tab gateway has no HTTP session. `GET /api/csrf`
  routed to the gateway returns a benign token (gateway already free to ignore
  `X-CSRF-Token`); `withCredentials` is a no-op without a server. Login
  (`POST /api/login`) routes to the gateway, which decides whether the in-tab UI
  requires auth (typically no — the tab already holds the key). Keep
  `ApiService` as-is; the gateway owns the policy.
- **remote viewer**: auth/CSRF travel over dmsg to the remote hypervisor exactly
  as a real request would — the remote enforces.

## Migration / sequencing

1. Add `SkywireHttpBackend` + a tiny `hv-config.ts` reader; wire it in `app.module`.
   Native mode delegates to `HttpXhrBackend` → **provably no-op for the
   visor-served build** (verify the native HV UI is byte-for-byte behavior-identical).
2. Point the wasm-visor / standalone generator at it (set `window.__SKYWIRE_HV__`,
   stop injecting `override.js`). Validate in-tab: load the Angular HV UI in the
   wasm-visor tab, confirm `/api/visors-summary` etc. resolve through `hvApi`.
3. Delete `override.js` + the XHR/fetch monkey-patch once the backend covers all
   three modes. Net: Angular owns the seam — testable (unit-test `handle()` with a
   fake gateway), debuggable, and one UI truly serves both worlds.

## Risk / notes

- `HttpBackend` swap is below interceptors and DI-injected, so it's the *narrowest*
  seam — feature services and `ApiService` need no edits (low blast radius on the
  production native UI; gate step 1 on "native build unchanged").
- WebSocket (`ApiService.ws()`, RxJS `webSocket()`) bypasses `HttpClient` →
  bypasses the backend. Audit which features use it (log streaming?) and route
  those over the gateway separately, or fall back to polling in wasm mode.
- Keep `window.__SKYWIRE_HV__` as the single source of mode truth (override.js
  already defines its shape) so the generator/standalone-HV config stays compatible.
```
