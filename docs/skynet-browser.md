# The skynet browser (wasm-visor iframe browser)

The wasm-visor ships an in-tab **skynet browser** — a WinBox window whose address
bar accepts a visor **PK**, **`pk.dmsg`**, an **`alias.dmsg`** (e.g.
**`home.dmsg`**), or an **`https://`** clearnet URL. Pages are fetched over dmsg
(skynet) — no DNS, no certificate authorities, IP-anonymous — or, for clearnet,
routed through a skysocks exit selected in the ⚙ panel.

Nav bar: `◀ ▶ ⟳ ⌂` (back / forward / reload / **home** → `home.dmsg`), the
address bar + `go`, `⚙` (skysocks proxy + per-window request log), and `ⓘ`
(this page's summary, in-UI).

## Limitations (by design)

Every fetched page is rendered into an iframe declared
`sandbox="allow-scripts allow-forms"` — deliberately **without
`allow-same-origin`**. The browser then forces each document into a **unique
opaque origin**, which is the source of the properties below.

- **No persistent storage.** Opaque origins cannot use Web Storage, cookies,
  IndexedDB, or the Cache API — access throws. So **cookies / localStorage /
  logins do not persist**, not even across a reload; each visit is effectively a
  fresh, incognito, storage-less context.
- **Isolation.** Sites cannot read each other's data, and — critically — cannot
  read the visor's own storage. The visor's secret key lives in the *parent*
  page's `localStorage` (`skywire-visor-sk`); the opaque-origin sandbox walls
  browsed sites off from it. This isolation is the whole reason for the sandbox.
- **Restricted scripting.** `allow-scripts allow-forms` only: no plugins, no
  popups, no top-level navigation, no `allow-same-origin`. Some clearnet sites
  that depend on cookies or service workers will misbehave.
- **Direct-load path is sandboxed too.** For "direct" clearnet sites the engine
  sets `frame.src = url` but leaves the `sandbox` attribute on the element, so
  even a directly-loaded real site runs opaque-origin and cannot use its cookies.

These are a privacy tradeoff, not bugs: no cross-site or site→visor leakage, and
no persistent tracking state.

## Why the wasm visor can't give per-site persistence

Browser storage is partitioned by **origin** (`scheme://host:port`). To give each
site its own durable, isolated storage bucket, each site must *be* a distinct
origin. A browser tab cannot mint new origins for content it fetches itself, and
a Service Worker is scoped to its page's single origin (path separation does not
isolate storage). So in the keyless in-tab wasm visor there is no way to give
browsed sites real per-origin storage without either (a) wildcard-subdomain
serving infrastructure with a per-subdomain dmsg fetch relay, or (b) an embedded
dmsg client per site — both heavy, and (a) reintroduces domain/DNS dependence the
keyless model avoids.

## The native path (future): one local proxy origin per site

On the **native desktop** browser (a real process, e.g. the WebKitGTK sibling)
per-site persistence *is* achievable, two ways:

1. **Wildcard-host loopback reverse proxy.** Serve
   `http://<sitekey>.skynet.localhost:PORT/` where `<sitekey>` is a stable 1:1
   encoding of the dmsg target (e.g. `PubKey.DNSLabel()` base32). Decode the host
   → dmsg target, fetch over the existing resolving-proxy path, serve it back
   under that host. `*.localhost` needs no DNS and is a secure context in
   Chromium, so each site gets its own persistent, isolated cookies / localStorage
   / IndexedDB / service worker — isolated from other sites *and* from the visor
   UI origin. In-page links are rewritten to keep same-site navigations on the
   same synthetic host (cross-site → another `<sitekey>.skynet.localhost`, clearnet
   → a clearnet-proxy origin). With isolation coming from the origin, the sandbox
   can be dropped.
2. **Point the whole webview at the resolving SOCKS5 proxy.** With
   `socks5h://127.0.0.1:<port>` resolving `<pk>.dmsg`, the browser engine treats
   `<pk>.dmsg` as the origin and partitions storage per-pk for free — no reverse
   proxy, no rewriting. Browsers just don't allow a per-iframe SOCKS proxy, which
   is why the wildcard-localhost reverse proxy is the fallback for the embedded
   case.

Recommended pairing: an **ephemeral / incognito** toggle that clears a site's
bucket on close, and keeping the visor UI origin strictly out of the proxy's host
namespace so a proxied site can never navigate to it or read its storage.
