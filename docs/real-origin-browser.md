# The real-origin mesh browser

The visor's in-app browser (both the native hypervisor UI and the in-browser
WASM visor) can open a `.dmsg` / `.skynet` site — or a clearnet site over
skysocks — as a **normal, isolated, secure web page**: its own origin, its own
cookies / `localStorage` / service worker / secure context, native subresource
loading, redirects, streaming and WASM. The visor only proxies the *transport*;
the browser does everything else, exactly as it would for any website.

This document explains what "real origin" means here, the two different ways a
mesh site can become a real browser origin, how the zero-config in-app browser
achieves it, and — because it was not obvious — how the origin **naming** scheme
evolved from encoding the target into the hostname to hashing it.

## Two ways to get a real origin (they are not the same thing)

There are two distinct mechanisms, and it is easy to conflate them:

### 1. The SOCKS5 resolving proxy (`dmsgweb`) — needs browser configuration

If you point your browser's SOCKS5 proxy at the visor's resolving proxy, the
**browser itself** resolves `http://<pk>.dmsg` (and `<pk>.skynet`, aliases,
name-vhosts, clearnet-over-skysocks) through the proxy → the visor → the mesh.
In this mode `<pk>.dmsg` *is* a genuine, distinct browser origin with real
cookies/storage — no extra machinery required.

The cost: every user must configure their browser's proxy settings. That is fine
for an operator, but not for someone who just opens a hosted page.

### 2. The in-app iframe browser — zero configuration

The visor's built-in browser (the WinBox "browser" window) is for users who have
**not** configured a proxy — most importantly, a visitor who just opens a hosted
wasm visor like `theskywirenetwork.net`. That browser cannot point an iframe at
`http://<pk>.dmsg`, because the visitor's browser has no way to resolve `.dmsg`.

Historically it worked around this by fetching the page with the visor and
injecting the HTML into a **sandboxed `srcdoc` iframe** — which has an **opaque**
origin: no persistent cookies/storage, no service workers, logins do not survive
a reload. Correct isolation, broken platform.

The **real-origin** browser replaces that: it gives the iframe a genuine,
per-site origin **without** any proxy configuration, so the browser does native
cookie/storage/SW/subresource handling. The rest of this document is about that
mechanism.

> TL;DR — if you have the SOCKS proxy configured, `.dmsg`/`.skynet` already work
> as real origins and none of this is needed. The real-origin browser exists for
> the **zero-config, in-page** case (the hosted PWA).

## How the zero-config real origin works

Each mesh site is served from its own isolated **browse origin** `B`
(`<id>.<browse-domain>`), separate from the **visor app origin** `V` (the page
that holds the in-tab visor and its key). Because `B ≠ V` (different host), the
browser treats them as cross-origin: a mesh site can never read the visor's
storage or key.

- **Native HV UI**: `B` is a loopback reverse-proxy origin on the operator's own
  machine (`pkg/visor/meshproxy.go`) — content flows through *their* transports.
- **WASM visor (hosted)**: `B` is served by a tiny bootstrap. The server hands
  the browser only a **static Service-Worker bootstrap** (`browse-bootstrap.html`
  + `browse-sw.js`). The SW then becomes `B`'s network layer: it relays every
  fetch through a `postMessage` bridge to `V`'s **first-party** in-tab visor,
  which fetches over dmsg/skynet/skysocks. **The host never serves content** —
  only the bootstrap. `V` holds the key; `B` may only ask "fetch this URL".

The trust boundary is `browse-responder.js`, injected into `V`: it validates the
`B` origin, hands `B` a private `MessagePort`, and services fetches via `V`'s own
visor. `B` never touches the key.

### Networks

The target network is explicit in the address you type, and the visor fetches
accordingly:

| You type | Network | How it's fetched |
|---|---|---|
| `<pk>.dmsg`, `home.dmsg`, `<vhost>.<pk>.dmsg` | dmsg | `fetchDmsg` (resolving proxy) |
| `<pk>.skynet` | skynet | `fetchDmsg` (skynet route) |
| `https://skycoin.com` | clearnet | `fetchClearnet` via skysocks-lite (anonymous exit) |

A public key may be given in the 66-char **hex** or the 53-char **base32** form;
both resolve to the same site.

## Origin naming — why it ends in a hash, and the road not taken

Giving `B` a real origin over HTTPS requires a TLS certificate for `B`'s
hostname. Issuing a cert per site does not scale (CA rate limits), so we want a
**single wildcard** cert to cover every site. Getting there took a wrong turn
worth recording.

### First attempt: encode the target into the hostname

The obvious scheme was to put the target *in* the name:

```
<base32pk>.dmsg.<browse-domain>
<clearnethost>.skysocks.<browse-domain>
```

This works for a bare public key. But it runs straight into how TLS wildcards
are defined:

- A wildcard certificate matches **exactly one label**, and only as the
  **left-most** label (RFC 6125 §6.4.3; CA/Browser Forum Baseline Requirements).
- `*.*.<domain>` is **not** a valid certificate — no CA issues it and no browser
  accepts it. A `*` never spans a dot.

So any **multi-label** target has no cert:

- a clearnet **subdomain** — `explorer.skycoin.com` → `explorer.skycoin.com.skysocks.<domain>`;
- a dmsg **name-vhost** — `magnetosphere.net.<base32pk>.dmsg`, where the 53-char
  base32 PK already nearly fills the 63-char DNS-label limit, so `vhost + PK`
  cannot even be collapsed into a single label.

Dead ends we considered and rejected:

- **Per-depth wildcards** (`*.dmsg`, `*.skysocks`, `*.com.skysocks`, …): unbounded
  for clearnet — you would need one wildcard per distinct prefix path, forever.
- **On-demand per-host TLS**: readable, but issues a new certificate per site on
  first visit → Let's Encrypt rate limits (~50/week per registered domain) break
  under normal browsing.
- **Hyphen-escaping the dots** into one label (`skycoin-com`, real `-` → `--`):
  reversible and readable, but a `vhost + 53-char base32 PK` still blows past the
  63-char label cap. Handles clearnet, not name-vhosts.

### The fix: content-addressed origins

Stop encoding the target in the name at all. Make `B`'s origin a short **stable
hash** of the target, and keep the map in the visor:

```
id = base32( sha256(canonical-target) )[:20]      # 20 chars, 80 bits
B  = https://<id>.<browse-domain>
```

- The visor keeps `id → target` (`globalThis.__meshOrigins`), populated when the
  browser navigates. `B`'s bootstrap sends its `id` at handshake; `V`'s responder
  looks up the target and **binds it to the port** — an untrusted `B` cannot ask
  for a different site's content.
- The `id` is a deterministic hash, so the same site always gets the same origin
  → per-site cookies/storage persist across sessions. Hex and base32 PK inputs
  canonicalize to the same `id`.
- **One wildcard** (`*.<browse-domain>`) now covers **every** site — any PK
  length, any clearnet subdomain depth, any dmsg name-vhost — because the label
  no longer scales with the target.

The trade-off is that the origin is opaque (`k3f9x2q7m1a8b4c0zzzz.<domain>`), not
`skycoin.com…`. That is purely cosmetic: the browser's address bar shows the
**true** URL (`https://skycoin.com`, `https://magnetosphere.net`); the hash is an
internal handle the user never types or sees.

## Hosted deployment

`V` (the wasm-visor PWA) and `B` (the browse origins) must be **different
registrable domains** so untrusted mesh content is fully cross-site from the
visor app (no shared cookies/storage). For example `V = theskywirenetwork.net`,
`B = *.<browse-domain>` on a separate eTLD+1.

A single process serves both, on two ports, behind one reverse proxy:

```
Caddy:  V-domain           → 127.0.0.1:7999     # the visor PWA + browse-responder.js
        *.<browse-domain>  → 127.0.0.1:7998     # the browse-origin bootstrap (static)

skywire cli hv serve \
  --addr 127.0.0.1:7999 \
  --browse-origin 127.0.0.1:7998 \
  --browse-suffix .<browse-domain> \
  --v-origin https://<V-domain>
```

or via the visor config (`hypervisor.wasm_serve`):

```json
"wasm_serve": {
  "addr": ":7999",
  "browse_origin_addr": "127.0.0.1:7998",
  "browse_suffix": ".<browse-domain>",
  "browse_v_origin": "https://<V-domain>"
}
```

The `*.<browse-domain>` wildcard needs a wildcard TLS cert, which requires the
**DNS-01** ACME challenge (HTTP-01 cannot issue wildcards). With Caddy and the
`caddy-dns/cloudflare` provider that is a few lines; one wildcard covers every
content-addressed origin.

The bootstrap server never proxies content — it serves only the embedded
`browse-bootstrap.html` + `browse-sw.js`; the SW relays all fetches to the
visitor's own in-tab visor.

## See also

- `pkg/wasmhv/browseui/RFC-real-origin-browser.md` — the design RFC.
- `pkg/visor/meshproxy.go` — the native reverse-proxy origin.
- `pkg/wasmhv/browse-{bootstrap.html,sw.js,responder.js}` — the hosted bridge.
- `docs/deploy/standalone-wasm-visor/` — deploy recipe.
