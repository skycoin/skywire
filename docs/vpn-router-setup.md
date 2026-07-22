# Skywire VPN router — setup & variants

The **vpn-router** turns a skywire visor into a **gateway**: downstream LAN/WiFi
clients get an address by DHCP and have their traffic NAT'd into the mesh-VPN
tunnel that the companion **vpn-client** app maintains. A skyminer (or any Linux
board running skywire) becomes a plug-in VPN router for the devices behind it —
no per-device configuration.

```
 client devices ──(WiFi or ethernet)──▶ vpn-router ──▶ tun0 (vpn-client) ──mesh──▶ vpn-server ──▶ internet
                     downstream                        uplink
```

Everything below is Linux-only and requires **root** (it creates interfaces,
runs hostapd, and sets iptables/`ip` rules) and a **running vpn-client**
(the router NATs into the tunnel that app owns).

---

## Two interfaces: uplink vs downstream

A router needs **two** network paths, on **different** interfaces:

- **Uplink** — the visor's normal interface (e.g. `end0`/`eth0`, or its own
  WiFi in station mode). This carries the mesh connection to the vpn-server.
  The router **must not** disturb it — the router keeps the host's default route
  on the uplink and only policy-routes the *downstream subnet* through the tun,
  so the tunnel's own carrier survives.
- **Downstream** — the interface clients connect to (`--lan-ifc`). This is a
  **second/USB ethernet NIC** (ethernet-out) or the **WiFi radio** (WiFi-out).

On a single-ethernet board (e.g. Orange Pi Prime) the onboard ethernet is the
uplink, so the downstream is either the WiFi radio or a USB-ethernet dongle.

---

## Variants

### 1. Ethernet-out (wired clients)
Serve a downstream ethernet interface (a second NIC or a USB-ethernet dongle).
Rock-solid; no WiFi driver involved.

```
VPNROUTER=true
VPNROUTERLANIFC='eth1'      # the downstream NIC (NOT the uplink)
```

### 2. WiFi-out (wireless clients)
Run an access point (hostapd) on the WiFi radio; clients associate over WiFi.

```
VPNROUTER=true
VPNROUTERLANIFC='wlan0'
VPNROUTERWIFI=true
VPNROUTERSSID='skywire-vpn'
VPNROUTERPASSPHRASE='choose-a-strong-passphrase'   # 8–63 chars; or VPNROUTEROPEN=true
VPNROUTERBAND='2.4'         # or '5'
VPNROUTERCHANNEL=0          # 0 = default for the band
VPNROUTERCOUNTRY='US'
```

> **rtl8723bs caveat (original skyminer boards).** The onboard rtl8723bs radio
> is a 2.4 GHz-only SDIO chip whose AP mode is firmware-fragile. skywire
> disables its power-save before starting hostapd (the #1 stability fix), and
> for boards that reset PM on link-up you can pin it persistently:
> `echo 'options rtl8723bs rtw_power_mgnt=0 rtw_ips_mode=0' > /etc/modprobe.d/rtl8723bs.conf`.
> If it still flaps, the robust option is a **USB WiFi dongle** (e.g. an
> MT7612U) — it turns any board into a stable dual-band AP — or use the
> ethernet-out variant.

### 3. Combined
Run both: an ethernet-out router *and* a WiFi AP by bridging the downstream NIC
and `wlan0` into one interface and pointing `--lan-ifc` at the bridge. (Create
the bridge with your OS's network config; skywire serves whatever `--lan-ifc`
names.)

---

## Configuring it

All variants are settable three equivalent ways:

- **skywire.conf** — set the `VPNROUTER*` variables above; the packaged
  auto-config/postinstall runs `config gen` from them. The template in
  `skywire.conf` documents every variable.
- **`skywire autoconfig`** — `skywire autoconfig --vpnrouter --vpnrouter-lan-ifc wlan0 --vpnrouter-wifi --vpnrouter-ssid skywire-vpn --vpnrouter-passphrase … --vpnrouter-band 2.4` writes those lines to skywire.conf and regenerates.
- **`skywire cli config gen`** — the same flags (`--servevpnrouter --vpnrouter-lan-ifc … --vpnrouter-wifi …`) directly emit a config.

Full knob list (config-gen flag → env var):

| flag | env | meaning |
| --- | --- | --- |
| `--servevpnrouter` | `VPNROUTER` | autostart the vpn-router |
| `--vpnrouter-lan-ifc` | `VPNROUTERLANIFC` | downstream interface |
| `--vpnrouter-subnet` | `VPNROUTERSUBNET` | gateway+subnet (default `192.168.42.1/24`) |
| `--vpnrouter-wifi` | `VPNROUTERWIFI` | WiFi-out (run hostapd) |
| `--vpnrouter-ssid` | `VPNROUTERSSID` | WiFi SSID |
| `--vpnrouter-passphrase` | `VPNROUTERPASSPHRASE` | WPA2 passphrase (8–63) |
| `--vpnrouter-open` | `VPNROUTEROPEN` | open (no-passphrase) network |
| `--vpnrouter-band` | `VPNROUTERBAND` | `2.4` or `5` |
| `--vpnrouter-channel` | `VPNROUTERCHANNEL` | channel (0 = default) |
| `--vpnrouter-country` | `VPNROUTERCOUNTRY` | regulatory country code |

The app also accepts `--tun-ifc` (which tunnel to NAT into; default = first
`tun*`), `--dhcp-start` / `--dhcp-end` / `--dns` / `--lease` for the DHCP pool.

You still need a **vpn-client** pointed at a vpn-server (`skywire cli vpn start
-k <server-pk>`, or the VPN config knobs) — the router NATs into whatever tunnel
it brings up.

---

## What the router does at startup

1. Assigns the gateway address to `--lan-ifc` and brings it up.
2. WiFi-out only: disables interface power-save, writes hostapd.conf, starts hostapd.
3. Serves **DHCP + DNS** on the downstream subnet (embedded pure-Go engine — no dnsmasq).
4. Waits for the vpn-client `tun`, then:
   - masquerades the downstream subnet out the tun,
   - **policy-routes** the downstream subnet through the tun (keeping the host uplink),
   - **clamps forwarded TCP MSS** to the path MTU (so clients need no MTU tweaks).

All of it is reversed on shutdown.

---

## Verifying

From a downstream client (after getting a DHCP lease):

- `ping 8.8.8.8` — confirms the forward path (client → router → tun → server → internet).
- Check your public IP (e.g. `curl https://api.ipify.org`) — it should be the
  **vpn-server's** IP, not your real one. (Note: skywire-infrastructure hosts —
  dmsg servers, discovery, and skycoin.com — are *exempted* from the tunnel by
  design, so an IP check against `ip.skycoin.com` shows your real IP; use a
  neutral service to see the VPN exit IP.)
