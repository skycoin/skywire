# Clearnet HTTP forward-proxy over dmsg

## Problem

The browser **wasm-visor** can already browse skynet/dmsg sites: it fetches HTTP
over a dmsg stream (`skywireVisor.fetchDmsg`) and renders the result, routing a
page's subresources and its own `fetch` over dmsg (see the wasm-visor browse
overlay). What it cannot do is reach the **clearnet** (ordinary `https://`
websites):

- It runs under **TinyGo**, which has no `crypto/tls`, so the tab cannot
  terminate TLS to an origin itself.
- A browser cannot be pointed at a SOCKS5 proxy from JavaScript, and a sandboxed
  iframe cannot run a Service Worker, so there is no transparent proxy hook.

## Model

A full visor (normal Go, with TLS) acts as a **forward proxy reached over dmsg**.
The wasm-visor sends an absolute-URL HTTP request over dmsg; the proxy visor
fetches it from the clearnet and returns the response. This is the same shape as
the existing resolving proxy's *upstream fallback*, but the ingress is HTTP over
dmsg instead of local SOCKS5.

```
wasm-visor --dmsg(HTTP)--> proxy visor --[upstream SOCKS5]--> skysocks exit --> clearnet
                                       \--[no upstream set]--> direct ---------> clearnet
```

TLS terminates at the proxy visor (or the skysocks exit), never in the tab.

### Wire format

The wasm-visor passes the **full URL as the request path** to `fetchDmsg`. The
dmsg HTTP client (`dmsgclient.httpRoundTrip`) writes the path verbatim as the
request target, producing the standard *absolute-form* proxy request:

```
GET http://example.com/page HTTP/1.1
Host: <proxy-pk-hex>
Connection: close
```

Go's `http.Server` parses this with `r.URL.IsAbs()` true (scheme + host set), so
the handler needs no custom framing — it is an ordinary `http.Handler`.

## Security

A clearnet forward proxy is an open-relay risk, so it is deliberately
constrained:

1. **Opt-in.** Off unless `dmsg_web.forward_proxy` is `true`. Default visors do
   not serve it.
2. **Whitelist-gated.** `pkGatedListener` drops any inbound dmsg stream whose
   remote public key is not in the visor's authoritative whitelist (own PK +
   survey whitelist + hypervisors + pty whitelist) — the same set that gates the
   visor's other privileged dmsg surfaces. An unauthorized peer never reaches the
   HTTP handler. It is **not** an open relay.
3. **Egress prefers upstream.** When `upstream_socks` is set, every fetch is
   dialed through that SOCKS5 proxy (a skysocks exit), so the exit IP is the
   skysocks node, not the proxy visor. Only when `upstream_socks` is empty does
   the proxy connect directly — and then its own IP is the exit.
4. **SSRF guard on direct egress.** When egressing directly, targets that resolve
   to loopback / private (RFC1918/ULA) / link-local / unspecified addresses are
   refused (`403`), so a whitelisted-but-curious caller cannot probe the proxy
   operator's LAN or cloud-metadata endpoint. With an upstream SOCKS5 the upstream
   is the boundary and the check is skipped.
5. **No tunneling.** `CONNECT` is refused (`405`); only fetched http(s) requests
   are served. Redirects are returned to the caller, not followed server-side, so
   the browsing tab's address bar stays honest.

## Config

Fields on `DmsgWebConfig` (`dmsg_web` in the visor config), reusing the
resolver's existing `upstream_socks`:

| field            | default | meaning                                              |
|------------------|---------|------------------------------------------------------|
| `forward_proxy`  | `false` | serve the clearnet forward-proxy over dmsg           |
| `forward_port`   | `84`    | dmsg port to listen on                               |
| `upstream_socks` | `""`    | egress via this SOCKS5 (skysocks exit); empty=direct |

## Status

- [x] Server: gated dmsg-served `http.Handler`, upstream/direct egress, SSRF
      guard, unit-tested (`pkg/visor/forward_proxy.go`).
- [ ] Client: wasm-visor resolver that routes a browsed page's external `http(s)`
      requests to a configured proxy PK over dmsg, and a browse-overlay setting
      for the proxy PK. (Follow-up.)
