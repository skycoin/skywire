# Mesh addresses

Every visor and deployment service is reachable by its public key, under two
suffixes:

- `.dmsg` goes over dmsg, through a dmsg server both sides use.
- `.skynet` goes over skywire transports and routes, the same way
  `skywire cli skynet` port forwarding does.

You do not register a name. The key is the address, and the connection is
encrypted and authenticated by that key.

## The key, in two forms

A public key is 33 bytes. It can be written two ways:

| Form | Length | Example |
|---|---|---|
| hex | 66 characters | `0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb` |
| base32 | 53 characters | `aka2cawifaqoqejwrsgqfdhrdmnjqucdw4tldpg3r7hitmttqszmw` |

Both name the same visor. Convert with `skywire cli pk dnslabel <hex-pk>`.

Use base32 for SkyDNS, since a DNS label holds at most 63 characters. Use it
for HTTPS in a browser too: Chromium-based browsers refuse a certificate for
the 66-character hex name, though curl accepts it. Hex works everywhere else.

## Ports

`http://<base32-pk>.skynet:8092/` connects to port 8092 on that visor. With no port,
`http://` means port 80 and `https://` means 443. A visor serves what it has
published on a port:

- port 80 is the visor's landing page unless something else is published there;
- other ports are whatever `skywire cli serve add` published;
- deployment services serve their HTTP API on dmsg port 80.

## Site names

A name to the left of the key picks a site on that visor:

```
http://status.skywire.dev.<base32-pk>.skynet/
```

This connects to port 80 of that visor and sends `Host: status.skywire.dev`. If
that visor forwards port 80 to a web server such as Caddy, the web server
serves its `status.skywire.dev` site. One visor can serve many sites this way.
[publish-a-site.md](publish-a-site.md) shows the visor side.

## Short names

The resolving proxy also answers a few names that are not keys:

| Name | Reaches |
|---|---|
| `skywire.dmsg`, `skywire.skynet` | your own visor |
| `home.dmsg` | a page listing every short name below |
| `status.dmsg`, `status.skynet` | the proxy's own status page |
| `tpd.dmsg`, `sd.dmsg`, `ar.dmsg`, `rf.dmsg`, `dmsgd.dmsg` | the deployment services your visor uses |
| `rewards.dmsg` | the reward system |
| `rsn.dmsg`, `dmsg0.dmsg`, … | route setup nodes and dmsg servers |

Short names exist only inside the proxy. SkyDNS and other visors do not know
them. [resolving-proxy.md](resolving-proxy.md#service-aliases-configured-deployment-services)
has the full list.

## Choosing the path

Two prefixes pin how a connection travels. Most people never need them.

- `<server-pk>.<dest-pk>.dmsg` uses that dmsg server.
- `<tpid-1>.<tpid-2>.<dest-pk>.skynet` routes over exactly those transports.
  `skywire cli tp route-addr <src-pk> <hop-pk>... <dest-pk>` builds it.

A label that looks like a key or a transport ID is read as a prefix. Any other
label is a site name.

## Where these names work

| Where | Hex | Base32 | Short names |
|---|:-:|:-:|:-:|
| [resolving proxy](resolving-proxy.md), `127.0.0.1:4445` | ✓ | ✓ | ✓ |
| skysocks-client, `127.0.0.1:1080` | ✓ | ✓ | ✓ |
| [SkyDNS](resolving-proxy.md#skydns-no-proxy-at-all), no proxy | ✗ | ✓ | ✗ |
| the desk browser and [desk links](browser-visor.md#links-that-open-something) | ✓ | ✓ | ✓ |
| HTTPS in a browser, with the [local CA](resolving-proxy.md#https) | ✗ | ✓ | ✓ |

## Try it

The project's status page, served by a visor:

- `http://status.skywire.dev.aka2cawifaqoqejwrsgqfdhrdmnjqucdw4tldpg3r7hitmttqszmw.skynet/`
- `http://aka2cawifaqoqejwrsgqfdhrdmnjqucdw4tldpg3r7hitmttqszmw.skynet:8092/`
- `http://rewards.dmsg/`
