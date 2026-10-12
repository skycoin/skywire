# Host bridge

A visor running in a browser tab cannot listen on a port of its computer
or open a raw TCP or UDP socket. It can still reach the network over the
carriers it opens itself, but nothing outside the tab can connect to it at
an address of its own, and programs on the computer cannot use its proxy.

The host bridge is a small program that does that for it. The tab connects
to the bridge over a WebSocket on loopback, and the bridge listens, dials and
binds UDP on the tab's behalf. The bridge keeps no keys and no state of its
own beyond its token.

With a bridge connected, a browser visor:

- runs the stcpr and sudph transports on the computer's ports, so other
  visors can reach it and sudph can hole-punch through NAT;
- forwards ports of the computer to addresses inside the visor, for example
  to its SOCKS proxy.

## Get the bridge

A browser visor carries the bridge for Linux (amd64, arm64, arm, 386),
macOS (amd64, arm64) and Windows (amd64). Open `/hostbridge/` on its
dashboard: in the desk that is `https://<desk>/vnet/8001/hostbridge/`. The
page offers the build for your computer and shows the command to run it.

Each build is a single file of about 750 KiB, built with TinyGo. On Linux
and macOS make it executable first. The builds are not signed, so macOS and
Windows may ask before running them.

A computer with skywire installed already has it as `skywire hostbridge`.

## Run it

```
./skywire-hostbridge-linux-amd64 -origin https://skywire.dev
```

`-origin` names the pages allowed to connect, comma separated. It defaults
to `https://skywire.dev`; the page shows the command with its own origin.
The bridge listens on `127.0.0.1:3438` (`-addr`) and prints a URL with a
secret token:

```
connect the visor to ws://127.0.0.1:3438/<token>
```

The token is kept in `<user config dir>/skywire/hostbridge-token`, so the
URL stays the same when the bridge restarts. `-token` sets one, and
`-token-file none` makes a new one each run.

The first visor to connect with the token pairs with the bridge. The bridge
records the visor's key and a new pairing secret in `hostbridge-visor` beside
the token, and the visor keeps the secret in its config. Later sessions prove
the secret with an HMAC of a random challenge, so it is never sent again.
After pairing the bridge serves only that visor. Run it with `-unpair` to
pair another.

## Connect the visor

Paste the URL into the page and press Connect, or run in the desk:

```
skywire cli visor hostbridge connect ws://127.0.0.1:3438/<token>
```

A page on a public origin such as skywire.dev that reaches a port of the
computer makes the browser ask about access to the local network. Allow it,
or the visor cannot reach the bridge.

The visor keeps the URL in its config and reconnects at boot, and redials
whenever the session ends. `skywire cli visor hostbridge status` shows it,
and `disconnect` ends it and removes it from the config.

## Forward a port

```
skywire cli visor hostbridge forward 127.0.0.1:1080 127.0.0.1:1080
```

listens on `127.0.0.1:1080` of the computer and passes each connection to
`127.0.0.1:1080` inside the visor, its SOCKS proxy. An address without a
host, such as `1080`, is on 127.0.0.1. Give `0.0.0.0:1080` to reach the
port from other computers. Programs on the computer can then use it:

```
curl --socks5-hostname 127.0.0.1:1080 https://ip.skycoin.com
```

Forwards are kept in the config with the URL and reopened on each
reconnect. `unforward <host addr>` stops one. The page has the same form.

## Transports

On its first session the browser visor starts stcpr and sudph on the
bridge's host and registers them with the address resolver at the
computer's public IP. Their ports stay the same across reconnects, so the
resolver entries stay valid. Inbound stcpr needs a forwarded port, as for a
native visor behind NAT. sudph needs none.

## Security

- The bridge accepts only a loopback Host, which stops DNS rebinding, and
  only the origins in `-origin`. The token in the URL must match, and after
  pairing only the paired visor's key is served.
- The browser's local network permission covers the whole local network
  for that origin, not just the bridge: once granted, any script on that
  origin can reach the router and other devices on the LAN.
- The bridge runs with the user's privileges. It needs no root.
