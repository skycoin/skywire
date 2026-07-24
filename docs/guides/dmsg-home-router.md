# A `.dmsg` gateway for a home router (OpenWRT / DD-WRT)

Skywire's deployment services — the reward system, transport discovery, route
finder, address resolver, uptime tracker — are reachable **only over dmsg**;
there is no plain-HTTP endpoint. The supported way to reach them from a normal
program is the [resolving proxy](resolving-proxy.md) (`skywire cli resolver`),
which resolves `*.dmsg` / `*.skynet` hostnames and tunnels them over the visor's
dmsg client.

By default that proxy binds `127.0.0.1`, so only the board it runs on can use
it. This guide sets up **one board with a visor as a `.dmsg` gateway for a whole
LAN**, so any phone, laptop, or TV behind your OpenWRT / DD-WRT router can open
`http://reward.dmsg/` (and any `<pk>.dmsg`) with **no skywire install on each
device**.

> You do **not** need a special skywire build or a port to the router itself —
> the router only forwards to a board that already runs skywire. (A native
> router port is tracked separately; see the end of this guide.)

## 1. On the board — bind the resolver to the LAN

Generate (or regenerate) the visor config with the resolver enabled and bound
to all interfaces instead of loopback:

```
skywire cli config gen --dmsgweb --dmsgweb-addr 0.0.0.0 \
                       --skynetweb --skynetweb-addr 0.0.0.0 \
                       -bo /opt/skywire/skywire.json
sudo systemctl restart skywire
```

`--dmsgweb-addr 0.0.0.0` binds the `.dmsg` SOCKS5 proxy on **port 4445** on every
interface (`--skynetweb-addr` does the same for `.skynet` on 4446). You can bind
a specific LAN IP instead of `0.0.0.0` if you prefer. On an installed system you
can also set it via autoconfig:

```
sudo skywire autoconfig --dmsgweb --skynetweb   # then edit proxy_addr, or use the flags above
```

Or set it directly in `skywire.json`:

```json
"dmsgweb":   { "enable": true, "proxy_addr": "0.0.0.0" },
"skynetweb": { "enable": true, "proxy_addr": "0.0.0.0" }
```

Confirm it is listening on the LAN (replace `192.168.1.50` with the board's IP):

```
ss -ltn | grep 4445           # on the board: 0.0.0.0:4445
curl -x socks5h://192.168.1.50:4445 http://tpd.dmsg/health   # from another LAN host
```

> **Security:** a non-loopback bind exposes the proxy to everyone on the LAN.
> Only do this on a trusted network. Anyone who can reach `board:4445` can
> browse dmsg through your visor.

Because SOCKS5 carries the hostname itself (no DNS lookup), **every** `.dmsg`
name works through it — the short service aliases (`reward.dmsg`, `tpd.dmsg`,
`ar.dmsg`, `dmsgd.dmsg`) **and** full `<66-hex-pk>.dmsg` addresses.

## 2. On the router — point clients at the gateway

### Option A — per-device SOCKS5 (simplest, works on both OpenWRT and DD-WRT)

Set the SOCKS5 proxy on each device (or just in the browser) to the board:

- **Host:** the board's LAN IP (e.g. `192.168.1.50`)
- **Port:** `4445`
- **Proxy DNS when using SOCKS v5:** ON (so `.dmsg` names resolve at the proxy)

Firefox: *Settings → Network → Manual proxy → SOCKS v5*, tick *Proxy DNS when
using SOCKS v5*. Chrome/OS-level: `--proxy-server="socks5://192.168.1.50:4445"`.
`curl -x socks5h://192.168.1.50:4445 http://reward.dmsg/`.

This is one setting per device and immediately gives that device all of
`.dmsg` (and `.skynet` via 4446).

### Option B — browser auto-config via WPAD/PAC (OpenWRT, zero per-device setup)

Serve a proxy auto-config file from the router so browsers pick up the SOCKS
proxy for `*.dmsg` automatically.

1. Create `/www/wpad.dat` (and symlink `proxy.pac`) on the router:

   ```
   function FindProxyForURL(url, host) {
     if (dnsDomainIs(host, ".dmsg") || dnsDomainIs(host, ".skynet"))
       return "SOCKS5 192.168.1.50:4445";
     return "DIRECT";
   }
   ```
   ```
   ln -s /www/wpad.dat /www/proxy.pac
   ```

2. Advertise it via DHCP option 252 in `/etc/config/dhcp`:

   ```
   config dhcp 'lan'
       list dhcp_option '252,http://192.168.1.1/wpad.dat'
   ```
   ```
   /etc/init.d/dnsmasq restart
   ```

Browsers set to *Use system proxy settings* / *Auto-detect* then route `.dmsg`
and `.skynet` through the board with no manual configuration.

### DD-WRT

DD-WRT has no package manager for extra proxy tooling, but **Option A works
unchanged** — set the device/browser SOCKS5 proxy to `board-ip:4445`. For
auto-config, DD-WRT can serve a PAC file from its `/jffs` web root and advertise
it with a DHCP option the same way as Option B (Administration → Commands, or
the DNSMasq additional-options box: `dhcp-option=252,http://<router>/proxy.pac`).

## Scope and limits

- **All TCP `.dmsg`/`.skynet` traffic** flows through the board over dmsg — HTTP,
  HTTPS (the browser's TLS is end-to-end to the target), WebSockets, `curl`.
- This is a **SOCKS5** gateway. It is transparent to *browsers* (Option B) but a
  non-SOCKS-aware application still needs the proxy set (Option A) — it is not a
  fully transparent, any-app, zero-config layer.
- A fully transparent model (`.dmsg` resolves like any domain for every app with
  no proxy at all) is the **mesh-gateway** (`vpn-router --mesh-gateway`, see
  [VPN router](../vpn/router.md)) and, on the router itself, native skywire
  support — tracked as a feature request in
  [skycoin/skywire](https://github.com/skycoin/skywire/issues).

## See also

- [resolving-proxy.md](resolving-proxy.md) — the `skywire cli resolver` proxies this builds on
- [VPN router](../vpn/router.md) — turning a board into a full mesh router / gateway
