# PK-aware website example

A skynet/dmsg-forwarded website that shows **different content per caller** by
reading the authenticated public key the visor injects. Demonstrates the
[skynet-website-auth](../../guides/skynet-website-auth.md) feature with the
recommended **Option A** trust model (separate the skywire ingress from the
clearnet ingress by *listener*, not source IP).

## Pieces

| File | Role |
|---|---|
| `backend.go` | tiny web app (loopback `127.0.0.1:3000`) that renders a page based on `X-Skywire-Remote-PK` |
| `Caddyfile` | Caddy fronting the backend: public `:80` strips the header, loopback `127.0.0.1:8080` trusts it |

You can run the backend **with or without Caddy**. Caddy is only needed if you
also serve the site on clearnet and/or host multiple vhosts; for a single
skynet-only site you can point the visor straight at the backend.

## Run it (visor → backend, no Caddy)

```bash
# 1. start the backend (loopback only)
go run backend.go

# 2. forward port 80 over skynet, injecting the caller PK, to the backend
skywire cli serve 80 --inject-pk --proxy-addr 127.0.0.1:3000
```

Now from another machine with the resolving proxy configured
(see [resolving-proxy.md](../../guides/resolving-proxy.md)):

```bash
# authenticated — the page shows your PK
curl -x socks5h://127.0.0.1:4446 http://<visor-pk>.skynet/
```

Reach the backend directly on the host and you get the anonymous page (no PK):

```bash
curl http://127.0.0.1:3000/        # X-Skywire-Remote-PK absent → "Anonymous"
```

## Run it with Caddy (clearnet + skynet, multi-site capable)

```bash
go run backend.go                       # 127.0.0.1:3000
caddy run --config Caddyfile            # :80 public, 127.0.0.1:8080 skywire-only
skywire cli serve 80 --inject-pk --preserve-host --proxy-addr 127.0.0.1:8080
```

- Clearnet visitors hit Caddy `:80` → header stripped → anonymous.
- skynet visitors are forwarded to Caddy `127.0.0.1:8080` → header authentic →
  per-PK page.

## Recognize specific PKs

Edit the `allowlist` map in `backend.go` to map full 66-hex public keys to
names. Known PKs get a personalized page; other authenticated PKs get a generic
signed-in page; clearnet gets the anonymous page. Find your PK with
`skywire cli visor pk`.

## Why a loopback listener and not `trusted_proxies 127.0.0.1`?

Because source-IP trust **fails open**: put a tunnel (cloudflared/ngrok) or a
container port-proxy in front of Caddy and every external request suddenly
appears to come from `127.0.0.1`, so a forged header would be trusted. A
loopback-bound listener is unreachable from off-host by construction, so it
fails *closed*. See the [guide](../../guides/skynet-website-auth.md#the-trust-boundary-read-this).
