# Proxy status hosts + HTTPS interstitial

Two related features on the embedded-web-proxy surface (`dmsg_web`,
`skynet_web`, and the `skysocks-client` tunnel they chain to).

## 1. Interstitial over HTTPS

The branded "building a route over the mesh…" interstitial
(`pkg/proxyinterstitial`) is injected by the resolving proxies' SOCKS5 `Dial`
callback when a route to the target is still warming. For plaintext HTTP it is
spliced in directly; for HTTPS it must ride a locally-terminated TLS session
(TLS MITM) because a raw-TLS tunnel can't carry an HTML page.

TLS termination uses a **name-constrained** local CA (`pkg/skynetca`) that can
only mint leaves for `.skynet` and `.dmsg`. The live bug was that the HTTPS
interstitial path attempted a mint for **every** TLS-port dial failure —
including clearnet HTTPS forwarded to the upstream `skysocks-client` — which the
CA can never cover, producing a `host does not match permitted suffix` error on
every such request.

Fix: `skynetca.Permits(minter, host)` (a non-breaking optional interface,
`HostPermitter`, implemented by `CachedMinter`) reports whether the CA can cover
a host **without** minting. The HTTPS-interstitial path in both resolving
proxies now gates on it:

```
case cfg.TLSMITM && isTLSPort(port) && skynetca.Permits(cfg.LeafMinter, origHost):
    // mint leaf, MITM-terminate, serve interstitial HTML over TLS
```

So a `.skynet`/`.dmsg` HTTPS target reliably renders the interstitial over TLS,
while a clearnet HTTPS target (which cannot be MITM'd by a name-constrained CA
anyway) cleanly falls through to the real error instead of a per-request log
spam. The page itself gained a footer with a deep-link to the surface's status
host (below).

## 1b. Streaming interstitial (live route-setup progress)

The interstitial can hold the browser connection OPEN and stream **live** route-
setup progress via HTTP chunked transfer-encoding, instead of the one-shot
`Content-Length` page + client meta-refresh. `pkg/proxyinterstitial/stream.go`
(`StreamConn` / `DriveStream` / `StreamOpen`+`StreamStep`+`StreamClose`) renders a
progressive-HTML shell and flushes a line per real attempt, then — once the route
is up — flushes a final chunk that reloads the page into the now-live content.

**What signal is real (the seam).** The interstitial is minted *after* a dial has
already failed transiently; there is no in-flight route-setup to subscribe to at
that point, and `pkg/router` exposes **no** per-hop / noise-handshake progress
hook — `DialOptions` has no observer, and even the mux-telemetry harness only
synthesizes coarse `RouteEstablished`/failed events around its own `DialRoutes`
call. So the granular `finding route → 2-hop via <pk> → noise handshake → route
group up` narrative is **not observable today**. Two honest sources are used
instead:

- **browse-origin** (`meshInterstitialRT`): it already runs the real cold-route
  round-trip in the background; the stream renders that actual in-flight
  attempt's outcome (`streamingInterstitialResponse`, flushed via the reverse
  proxy's `FlushInterval < 0`).
- **SOCKS resolving proxies** (`dmsgweb` / `skynetweb`): the mint point has no
  live attempt, so the streamer DRIVES a fresh one via a `Probe` (`dmsgRedialProbe`
  / `skynetRedialProbe`) and streams each real attempt's `StatusLine` + the
  success. Coarse, but real.

The **missing seam** to get the granular lines is a router-side progress callback
— e.g. `DialOptions.OnProgress func(phase RouteSetupPhase)` invoked by
`DialRoutes` / the cascade setup path as hops resolve and the noise handshake
completes. That lives in `pkg/router` (owned separately) and is intentionally
**not** added here; this PR streams only the coarse signal actually available and
does not fabricate per-hop events. `pkg/skysocks`'s `ServeSOCKS5` mint point is
also left one-shot: it tears its session down for reconnect, with no attempt to
drive/observe from that function.

**Fallback.** `StreamConn` reads the request first and serves the existing
one-shot meta-refresh page to an HTTP/1.0 client; a nil probe / absent event
source likewise degrades to the static page.

**Liveness tie-in.** The same probe *is* the "is-my-connection-up" signal; a
future `status.*` mux view can share it as a WS/WT liveness indicator (extension
point, not built here).

## 2. Per-proxy status hosts

Each proxy serves a read-only diagnostic page at a reserved, well-known host
**through itself**, mirroring the existing in-process `home.<suffix>` host:

| host                | surface   | underlying app    |
|---------------------|-----------|-------------------|
| `http://status.dmsg/`      | dmsg     | `dmsgweb`          |
| `http://status.skynet/`    | skynet   | `skynetweb`        |
| `http://status.skysocks/`  | skysocks | `skysocks-client`  |

`pkg/proxystatus` owns the surface taxonomy, host matcher (`Match`), the
in-process HTTP responder (`ServeConn`, same net.Pipe trick as
`serveHomeInProcess`), the read-only `Snapshot` shape + `Provider` interface, and
the HTML renderer. Both resolving proxies' `Dial` callbacks check `Match(host)`
**before** suffix resolution / upstream forwarding, so any of the three hosts is
reachable through either proxy (a browser typically points at one).

The visor implements `proxystatus.Provider`
(`pkg/visor/embedded_proxystatus.go`) entirely on **existing** read APIs:

- **logging** — `Visor.LogsSince(app)` tails the app's log store;
- **mux view** — `Visor.RouteGroupMuxInfo(app)` gives the same per-leg
  bandwidth/RTT/retransmit telemetry `cli proxy mux plot` renders, drawn as a
  static per-leg bandwidth-share table (meta-refreshes to stay live);
- **running** — `procManager.ProcByName(app)`.

MVP status vs. scaffold:

- **`status.skysocks`** is the fully-realized MVP (logs + live mux view for the
  route group where multiplexing actually happens).
- **`status.dmsg` / `status.skynet`** share the identical page; their mux
  section is empty until/unless their route group is tagged for
  `RouteGroupMuxInfo`.
- **route/transport events** render an empty section today — the collection
  buffer is the scaffolded extension point.

### Reaching status over HTTPS (real cert, no warning)

The in-process SOCKS path above serves the status pages over **plain HTTP** at
bare hosts (`status.skysocks` etc.) — fine through the resolving proxy, but a
browser hitting `https://status.skysocks/` would need the self-signed skynetca CA
installed to avoid a warning.

For a warning-free `https://`, the status pages are **also** served through the
browse-origin listener (`pkg/visor/meshproxy.go`, gated by `BrowseOrigin.Enable`),
which already terminates TLS with the deployment's **real** wildcard cert
(`BrowseOrigin.TLSCert`/`TLSKey`, or a fronting Caddy) under `BrowseOrigin.Suffix`
(e.g. `.haltingstate.net`). Reached at a **single-label** host so a single-level
wildcard (`*.<suffix>`) covers it:

    https://status-skysocks.haltingstate.net/
    https://status-dmsg.haltingstate.net/
    https://status-skynet.haltingstate.net/

`meshStatusHandler` intercepts these hosts on the browse-origin mux (both
`subdomain` and `port` modes) **before** the reverse proxy, renders the same
`proxystatus` page, and lets every other (browse-frame) host fall through. This is
the real-cert alternative to the name-constrained skynetca leaf path in
`pkg/skynetweb` — no CA install. When browse-origin is disabled or unconfigured in
a deployment, the plain-HTTP SOCKS path remains the fallback.

Note the wildcard is single-level: `status-<surface>.<suffix>` is covered, but a
multi-label host is not — which is also why the matcher rejects multi-label hosts.

### Extension seam: route control

The MVP is deliberately read-only. The page renders a disabled "route control"
section, and `Snapshot`/`Provider` are shaped so control lands additively: add a
mutating method to `proxystatus.Provider` and implement it on the existing visor
mux-reshape API (`AddMuxRoute` / `RemoveMuxRoute` / `SetMuxMode`) plus dmsg relay
selection — no wire reshape, no new plumbing in the proxies.
