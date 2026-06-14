# PK-aware websites over skynet / dmsg

A website forwarded over skynet or dmsg is, by default, a **blind TCP splice**:
the visor pipes bytes between the remote peer and your local web server, so the
web server never learns *who* is connecting. The visor, however, already knows —
the skywire transport is authenticated by the caller's public key (the noise
handshake). `inject_pk` hands that identity to your backend so a site can do
per-PK behavior (auth, personalization, gating) the splice can't.

## What it does

With `inject_pk` enabled on a forwarded port, the visor stops splicing and
instead terminates HTTP and reverse-proxies to your backend, stamping two
headers on every request:

| Header | Value |
|---|---|
| `X-Skywire-Remote-PK` | the caller's 66-hex public key (omitted if the peer can't be identified) |
| `X-Skywire-Transport` | `dmsg` or `skynet` |

Any client-supplied copy of those headers is **stripped first**, then the
authentic value is set — so a caller cannot forge them *over this path*. This is
the moral equivalent of an mTLS-terminating reverse proxy exposing the verified
client certificate to its backend: the visor did the one handshake and vouches
for the PK.

```bash
skywire cli serve 80 --inject-pk --preserve-host --proxy-addr 127.0.0.1:8080
# (or the deprecated `skywire cli skynet port add 80 --inject-pk ...`)
```

Your backend then just reads a header — in any language:

```go
pk := r.Header.Get("X-Skywire-Remote-PK") // "" = anonymous / not via skywire
```

## The trust boundary (read this)

The header is only trustworthy if **your backend is reachable ONLY through the
visor.** The visor strips-and-sets the header on requests that pass *through* it;
a request that reaches the backend by any other door was never sanitized.

- **Safe (default):** bind your backend to `127.0.0.1`. The visor dials
  `localhost`, and nothing off-host can reach a loopback listener — so the visor
  is the sole ingress and the header is authoritative.
- **Unsafe:** if the *same* backend listener is also exposed directly to
  clearnet, a direct client can send a forged `X-Skywire-Remote-PK` and your
  backend will believe it. The visor cannot sanitize a request it never saw.

**Trust the destination, not the source IP.** Don't try to solve the dual-exposure
case with "trust the header only from `127.0.0.1`" (e.g. Caddy `trusted_proxies`):
it *fails open*. The moment anything that re-dials your server from localhost sits
in front of it — a cloudflared/ngrok tunnel, a container's port-proxy, another
reverse proxy — every external request appears to come from `127.0.0.1` and the
forged header is trusted, silently. Separation by listener fails *closed*
(misconfig → no header → treated as anonymous), which is what you want for auth.

## Serving on both skynet AND clearnet (Caddy example)

The common setup: **Caddy** is the public front door on `:80`, serving several
sites, and you forward Caddy's port over skynet. Because that single `:80`
listener takes both clearnet and skynet traffic, you can't trust the header on
it. The fix is a **dedicated loopback listener** that only the visor reaches:

```caddyfile
# Public clearnet door — never trust the injected header here.
:80 {
	request_header -X-Skywire-Remote-PK
	import sites
}

# skywire-only door — bound to loopback, so only the visor can reach it.
# The visor forwards skynet/dmsg traffic here and injects the PK.
127.0.0.1:8080 {
	import sites   # same sites; here {http.request.header.X-Skywire-Remote-PK} is authentic
}

(sites) {
	# your vhosts; reverse_proxy passes the header through to each app
	@example host example.com
	handle @example {
		reverse_proxy 127.0.0.1:3000
	}
}
```

Then forward the **loopback** listener (not the public `:80`):

```bash
skywire cli serve 80 --inject-pk --preserve-host --proxy-addr 127.0.0.1:8080
```

- `--preserve-host` passes the incoming `Host:` through so Caddy's vhost routing
  works — pair it with the resolver's subdomain form
  (`http://example.com.<visor-pk>.skynet`, see
  [resolving-proxy.md](resolving-proxy.md)).
- The PK header propagates to every site behind Caddy for free — each app just
  reads `X-Skywire-Remote-PK`; no per-app visor config.
- Clearnet visitors hit `:80`, where the header is stripped, so they're simply
  anonymous.

A working end-to-end example (Caddyfile + a tiny PK-aware backend) lives in
[../examples/pk-aware-website/](../examples/pk-aware-website/).

## Limits

- **HTTP only.** `inject_pk` makes the visor parse HTTP; enabling it on a
  non-HTTP service breaks the stream. Leave it off for raw TCP forwards.
- **Plain HTTP between visor and backend.** The visor injects into cleartext
  HTTP; if the backend speaks end-to-end TLS the visor can't add a header
  without terminating TLS. Put the backend on plain-HTTP loopback.
- **Whitelist still applies.** `inject_pk` is orthogonal to the per-port PK
  whitelist: the whitelist gates *whether* a peer may connect at all; the header
  tells your app *who* connected so it can decide what to show. Use either or
  both.
