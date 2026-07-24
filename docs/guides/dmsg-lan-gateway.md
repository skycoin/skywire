# A `.dmsg` / `.skynet` gateway for your LAN (OpenWRT / DD-WRT)

Skywire's deployment services — the reward system, transport discovery, route
finder, address resolver, uptime tracker — are reachable **only over dmsg**;
there is no plain-HTTP endpoint. A `.dmsg` / `.skynet` name is not a normal
hostname either: it encodes a public key, and the destination is a routing port
on that key reachable only over the mesh.

This guide turns **one board running a visor into a `.dmsg` gateway for a whole
LAN**, so any phone, laptop, or TV behind your OpenWRT / DD-WRT router can reach
`http://reward.dmsg/` (and any `<pk>.dmsg`) — **with no skywire install on each
device**. You do **not** need a special skywire build or a port on the router
itself; the router only forwards to a board that already runs skywire.

There are two ways to do it, and they are genuinely different:

| | **Transparent (mesh gateway)** | **SOCKS5 (resolving proxy)** |
|---|---|---|
| Per-device setup | **None** — any LAN device just works | One SOCKS5 setting per device / browser |
| How a name resolves | DNS → synthetic IP → transparent redirect | The client speaks SOCKS5 and passes the name |
| Works for | **Every app** (curl, apps, TVs, phones) | SOCKS-aware apps + browsers |
| Board requirement | Linux visor + root (netfilter) | Any visor |

**If you want "any device on the LAN just works, with nothing to configure per
device," use the transparent mesh gateway (Section 1).** A SOCKS5 proxy *cannot*
be transparent — SOCKS is a per-application protocol, the client has to speak it
and hand over the hostname in-band, so there is always a per-device (or
per-browser) setting. Nothing on the router changes that. The SOCKS5 method
(Section 2) is the right choice when you only want one or two devices, or the
board can't run the mesh gateway.

---

## 1. Transparent: the mesh gateway (zero per-device config)

The mesh gateway answers `*.dmsg` / `*.skynet` DNS with a **synthetic IP** from a
private pool (`100.64.0.0/16`), remembers `IP → public key`, and transparently
**redirects** TCP aimed at that pool into a mesh dial (reading the original
destination via `SO_ORIGINAL_DST`). A LAN device never knows it happened — it
just connects to a name. This is exactly what `vpn-router --mesh-gateway`
provides for a board that *is* the router; `--mesh-gateway-only` provides the
same thing for a board that sits **behind** your existing OpenWRT / DD-WRT
router.

### 1a. On the board — run the standalone mesh gateway

The board needs a running visor (so it has mesh transports). Start the gateway
as a visor app — it serves the mesh-name DNS and the transparent proxy, and does
**nothing else** (no DHCP, no NAT, no tunnel):

```
skywire cli visor app start vpn-router --mesh-gateway-only --lan-ifc eth0
```

or, launched directly by the visor from its config (add `vpn-router` to the
app list with args `--mesh-gateway-only --lan-ifc eth0`). Replace `eth0` with
the board's LAN interface. By default the DNS listens on `0.0.0.0:53` and the
synthetic pool is `100.64.0.0/16`; override with `--mesh-bind`,
`--mesh-dns-port`, and `--mesh-gateway-cidr` if those collide with something on
the board.

For HTTPS to `.dmsg` hosts, add `--mesh-gateway-tls` (the gateway TLS-terminates
with a self-generated CA that LAN clients must trust) and, for friendly names,
`--mesh-alias reward=<pk>`.

Confirm the DNS answers with a synthetic IP:

```
dig +short reward.dmsg @<board-ip>          # → a 100.64.x.x address
```

### 1b. On the router — forward DNS + route the pool to the board

Two settings, both supported on **OpenWRT and DD-WRT**. Devices keep the router
as their DHCP server, DNS, and default gateway; only `.dmsg` / `.skynet` names
and the synthetic pool detour to the board.

**OpenWRT** (`<board-ip>` = the board, e.g. `192.168.1.50`):

1. DNS forward — in `/etc/config/dhcp` under the `dnsmasq` section:

   ```
   list server '/dmsg/<board-ip>'
   list server '/skynet/<board-ip>'
   ```
   ```
   /etc/init.d/dnsmasq restart
   ```

2. Static route for the synthetic pool — in `/etc/config/network`:

   ```
   config route
       option interface 'lan'
       option target '100.64.0.0/16'
       option gateway '<board-ip>'
   ```
   ```
   /etc/init.d/network reload
   ```

**DD-WRT**:

1. DNS forward — *Services → Services*, in **Additional DNSMasq Options**:

   ```
   server=/dmsg/<board-ip>
   server=/skynet/<board-ip>
   ```

2. Static route — *Setup → Advanced Routing → Static Routing*: destination
   `100.64.0.0`, mask `255.255.0.0`, gateway `<board-ip>`, interface LAN. (Or,
   from *Administration → Commands*: `ip route add 100.64.0.0/16 via <board-ip>`.)

That's it. From **any** LAN device, with nothing configured on the device:

```
curl http://reward.dmsg/health
```

resolves through the router → the board's mesh-gateway DNS → a synthetic IP,
whose traffic is routed to the board and dialed over the mesh.

> **Security:** every device on the LAN can now reach dmsg/skynet through the
> board. Only do this on a trusted network. The gateway deliberately bypasses
> any VPN tunnel — mesh names are dialed directly over the visor's transports.

> **DNS port already taken?** If something on the board already owns `:53`, start
> the gateway with `--mesh-dns-port 5353` and point the router at it:
> `server=/dmsg/<board-ip>#5353`.

---

## 2. SOCKS5: the resolving proxy (per-device, but any visor)

If you only need one or two devices — or the board can't run the transparent
gateway — use the embedded [resolving proxy](resolving-proxy.md)
(`skywire cli resolver`). It's a SOCKS5 proxy that resolves `*.dmsg` / `*.skynet`
and tunnels them over the visor's dmsg client. Because SOCKS5 carries the
hostname itself, **every** `.dmsg` name works through it — short aliases
(`reward.dmsg`, `tpd.dmsg`) *and* full `<66-hex-pk>.dmsg` addresses.

### 2a. On the board — bind the resolver to the LAN

The proxy binds `127.0.0.1` by default, so only the board can use it. Bind it to
a LAN address instead. The simplest way, on a **running** visor, is at runtime —
it persists, so it survives a restart:

```
skywire cli resolver up --bind 0.0.0.0
```

`--bind 0.0.0.0` serves every interface (or give a specific LAN IP). This sets
and **persists** the bind (`proxy_addr` + enabled) in `skywire.json`, and brings
the `.dmsg` proxy up on **port 4445** and `.skynet` on **4446**.

Confirm it's listening on the LAN (replace `192.168.1.50` with the board's IP):

```
ss -ltn | grep 4445                                          # on the board: 0.0.0.0:4445
curl -x socks5h://192.168.1.50:4445 http://tpd.dmsg/health   # from another LAN host
```

> **On an `skywire autoconfig`-managed host** (the usual Linux install): a
> later `autoconfig` run regenerates `skywire.json` from `skywire.conf` and
> would reset a runtime `--bind`. There, set it in `skywire.conf` instead so it
> persists across autoconfig:
>
> ```
> DMSGWEB=true
> DMSGWEBADDR='0.0.0.0'
> SKYNETWEB=true
> SKYNETWEBADDR='0.0.0.0'
> ```
> ```
> sudo skywire autoconfig
> ```
>
> (Equivalently in one shot:
> `sudo skywire autoconfig --dmsgweb --dmsgweb-addr 0.0.0.0 --skynetweb --skynetweb-addr 0.0.0.0`.)

> **Security:** a non-loopback bind exposes the proxy to everyone on the LAN.
> Only do this on a trusted network.

### 2b. On each device (or browser) — point at the proxy

**Option A — per-device SOCKS5 (works on both OpenWRT and DD-WRT).** Set the
SOCKS5 proxy on the device or browser to the board:

- **Host:** the board's LAN IP (e.g. `192.168.1.50`) — **Port:** `4445`
- **Proxy DNS when using SOCKS v5:** ON (so `.dmsg` names resolve at the proxy)

Firefox: *Settings → Network → Manual proxy → SOCKS v5*, tick *Proxy DNS when
using SOCKS v5*. Chrome/OS-level: `--proxy-server="socks5://192.168.1.50:4445"`.
`curl -x socks5h://192.168.1.50:4445 http://reward.dmsg/`. (`.skynet` is on 4446.)

**Option B — browser auto-config via WPAD/PAC (OpenWRT, no per-device setup for
browsers).** Serve a PAC file from the router so browsers pick up the SOCKS proxy
for `*.dmsg` automatically:

1. `/www/wpad.dat` (symlink `proxy.pac` → it) on the router:

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
and `.skynet` through the board. DD-WRT can serve a PAC file the same way from
its `/jffs` web root (`dhcp-option=252,http://<router>/proxy.pac`).

Note this only covers *browsers* (and only ones set to auto-detect); a
non-SOCKS-aware app still needs Option A. That per-app limit is the reason to
prefer the transparent gateway (Section 1) when you want everything to work.

---

## See also

- [resolving-proxy.md](resolving-proxy.md) — the `skywire cli resolver` proxies Section 2 builds on
- [VPN router](../vpn/router.md) — turning a board into a full mesh router / gateway / WiFi AP (where `--mesh-gateway` is the built-in, board-is-the-router form of Section 1)
