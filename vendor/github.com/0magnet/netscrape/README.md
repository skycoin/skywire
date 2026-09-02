# netscrape

A browser inside the browser — the skywire virtual-browser engine and the
mini-desktop it lives in.

Your browser speaks DNS, IP and TLS. The networks netscrape browses don't:
[skywire](https://github.com/skycoin/skywire) sites are addressed by public
key and fetched over dmsg or skynet routes, clearnet pages tunnel through a
skysocks exit, and a wasm visor running *in the same tab* serves its own
hypervisor UI on a virtual loopback no real socket ever touches. netscrape
renders all of them anyway.

## Channels

An address typed into its bar dispatches by shape:

| address | channel |
|---|---|
| `http://<pk>.dmsg/…`, `dmsg://…`, `skynet://…` | fetched over the mesh by public key |
| `https://example.com` | tunneled through a skysocks exit (policy: block / direct / pinned exit) |
| `http://127.0.0.1:<port>` | the tab's virtual loopback ([bottle](https://github.com/0magnet/bottle) vnet) — an in-page listener shadows the port, an unclaimed one falls through to the real host loopback |
| real-origin mode | hands the load to an isolated origin whose network layer is a service worker ([realorigin](https://github.com/0magnet/realorigin)) |

Without real-origin mode, pages render through the transcoder: the document
is fetched over the channel, same-site subresources are inlined (stylesheets
as `<style>`, the rest as `data:` URIs), link clicks and the page's own
`fetch` are relayed back through the same channel, and the result lives in a
sandboxed iframe.

## Desktop

The engine ships the mini-desktop that hosts it: a panel, and windows —
browser, terminals, log, CLI — built on the `WinBox` constructor from
[winbox-go](https://github.com/0magnet/winbox-go).

## Use

The engine is dependency-injected. A hosting page supplies the fetchers (or
provides `globalThis.skywireVisor`):

```go
import "github.com/0magnet/netscrape"

serveJS(w, netscrape.BrowseJS())
```

```js
var browser = createBrowser({
  frame: iframe,
  fetchDmsg: (pk, method, path, body) => ...,      // → {status, body, headers}
  fetchClearnet: (exit, method, url, body) => ..., // → same shape
});
browser.browseTo("home.dmsg", "/");
```

Grown in [skycoin/skywire](https://github.com/skycoin/skywire), where it is
the browser of both the wasm-visor page and the native hypervisor dashboard.
