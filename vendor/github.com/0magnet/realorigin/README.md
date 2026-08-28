# realorigin

Render untrusted remote content at a **real, isolated browser origin** whose
network layer is a service worker — while the credentials that fetch it stay on
a different origin, behind a capability.

The content gets a genuine origin with a genuine URL, so storage, cookies,
history, WebAssembly and streaming all behave natively. Nothing is faked, and
nothing is re-implemented in JavaScript.

```
A  https://app.example                the app: holds the credential and the transport
B  https://<id>.browse.example        untrusted content: holds nothing
```

## Why this is not just an iframe

The hard part is not the network. It is the certificate.

Giving `B` a real origin over HTTPS needs a certificate for `B`'s hostname, and
issuing one per site exhausts CA rate limits, so a single wildcard has to cover
every site. But **a wildcard matches exactly one label**, and only the left-most
one (RFC 6125 §6.4.3; CA/Browser Forum Baseline Requirements) — `*.*.example` is
not a certificate any CA issues or any browser accepts. So the target cannot be
encoded into the hostname: a clearnet subdomain has too many labels, and a long
identifier crowds the 63-character DNS label limit on its own.

So the target is not named at all. The origin is a short, stable hash of it:

```
id = base32(sha256(canonical))[:20]
B  = https://<id>.<suffix>
```

One wildcard now covers every site. The hash is deterministic, so a site keeps
its cookies and storage across sessions. And because the frame knows only its own
`id`, it supplies a **path** and never a host — one browse origin cannot ask for
another's content.

## Use

```go
cfg := realorigin.Config{
    Addr:      "127.0.0.1:7998",
    Suffix:    ".browse.example",       // a different registrable domain from the app's
    AppOrigin: "https://app.example",
}
go cfg.ListenAndServe(ctx)              // serves the worker and the shell, nothing else
```

Serve `realorigin.ResponderJS()` from the **app** origin, first-party, and give
it a transport:

```js
realOrigin.configure({
  suffix: '.browse.example',
  fetch: function (target, req) {
    // req: {url, method, headers, body(ArrayBuffer|null), path}
    return myTransport(target, req);   // → {status, headers, body}
  },
});

var id = await realOrigin.register('https://example.com');
frame.src = 'https://' + id + '.browse.example/';
```

That is the whole integration. The transport is the only part you write.

### Telling the visitor what is happening

A transport that has to set up a route before it can fetch anything leaves the
frame blank for a while, and a bare spinner wastes that time. Stream it:

```js
realOrigin.progress('connecting through exit …', id);   // just that frame
realOrigin.progress('route established');               // every loading frame
```

Lines reach the frame until its first response lands — which is exactly the
interstitial's lifetime, since the document is replaced after that.

If the built-in shell is too plain for what you have to say, replace it:

```go
cfg := realorigin.Config{ /* … */ Shell: myShell }
```

Start from `realorigin.BootstrapHTML()`. A replacement must speak the same bridge
protocol, and gets the same `__APP_ORIGIN__` / `__SUFFIX__` substitutions.

## The wire protocol

```
{ type: 'realorigin-hello', shortid }   → a private port, bound to one target
{ type: 'realorigin-fetch', req: { url, method, headers, body } }
                                        → { status, headers, body } | { error }
```

`error` becomes 502, sixty seconds of silence becomes 504, and `content-length`,
`transfer-encoding` and `connection` are stripped so the browser recomputes its
own framing.

## Try it

```
go run ./cmd/realorigin-demo
```

Then open <http://localhost:7999>. The demo's transport is plain HTTP through its
own process, which makes it a real-origin proxy that sidesteps CORS. Swap that
one function for a mesh, an onion route, a peer-to-peer fetch or a decrypted
archive and nothing else changes.

**The demo fetches server-side, and that is not the interesting case.** Its
transport is an HTTP client in the demo's own process, chosen because it needs no
infrastructure to try. A transport that runs *in the visitor's tab* — a wasm
client, a WebRTC peer, a local decrypted archive — is what gives this its unusual
property: the server then serves the shell and the worker and nothing else, and
carries, sees and stores none of the traffic. Nothing scales with how much anyone
browses. That is how skywire uses it, and the substrate is identical either way;
only the transport moves.

## Things that will bite you

Each of these was paid for once already.

- **A wildcard spans one label.** Everything about the naming follows from it,
  and it is why the origin is a hash.
- **Wildcards need DNS-01.** HTTP-01 cannot issue them, so a hosted deployment
  needs a DNS provider credential.
- **`A` and `B` must be different names — locally too.** Same host and port is
  one origin, and the browsed page's own scripts then read the app's
  `localStorage`, DOM and globals directly: whatever credential the app holds is
  one call away. Different origins, and the browser refuses all three, leaving
  `postMessage` as the only channel and the app deciding what to answer. The
  certificate does not enforce this — it only makes an HTTPS origin possible. The
  origin enforces it.
- **Hosted, go further: separate registrable domains.** Different hostnames make
  the two cross-origin, which protects storage, DOM and globals. Only different
  registrable domains make them cross-*site*, which additionally stops `B` from
  setting `Domain=`-scoped cookies the app will receive.
- **The responder must be first-party on `A`.** In a cross-origin helper iframe,
  Storage Partitioning lands it in a different partition from the app's own
  workers, where it cannot reach the client it exists to call.
- **`B` must be framed by `A`.** The shell reaches the app through
  `window.parent` and refuses to run as a top-level document.
- **Navigations are not intercepted**, deliberately: a worker that served the
  first page would have to be installed by a page it had not served yet. Every
  path but the worker serves the shell, and each navigation re-runs it.
- **Mixed content decides whether you need a local certificate.** All-HTTP on
  `*.localhost` needs none, because browsers treat it as a secure context; an
  HTTPS app forces HTTPS browse origins and a `*.<suffix>` SAN.

## Provenance

Extracted from the real-origin mesh browser in
[skywire](https://github.com/skycoin/skywire)'s wasm hypervisor, where it was
built to show mesh content without handing that content the visor's identity key.
Nothing here is specific to that: the transport was always behind an interface,
because the service worker runs on the untrusted origin and must not know what
the credential is for.

## Licence

MIT.
