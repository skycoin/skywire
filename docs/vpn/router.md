# VPN router (vpn-router)

`vpn-router` turns a Skywire visor into a **VPN gateway**: the devices behind it
(over WiFi or ethernet) get an address by DHCP and have their traffic NAT'd into
the mesh-VPN tunnel that the companion [`vpn-client`](client.md) maintains. A
skyminer — or any Linux board running Skywire — becomes a plug-in VPN router for
everything on its LAN, with no per-device configuration.

```
 client devices ──(WiFi or ethernet)──▶  vpn-router  ──▶  tun0 (vpn-client) ──mesh──▶  vpn-server ──▶ internet
                     downstream                             uplink
```

!!! warning "Linux + root + a running vpn-client"

    The router creates interfaces, runs hostapd, and installs `iptables`/`ip`
    rules, so it is **Linux-only** and needs **root** (`CAP_NET_ADMIN`). It NATs
    into the tunnel the [`vpn-client`](client.md) app owns, so a vpn-client must
    be connected to a [vpn-server](README.md) as well.

---

## Two interfaces: uplink vs downstream

A router needs **two** network paths, on **different** interfaces:

- **Uplink** — how the board itself reaches the internet / the mesh. It can be
  **ethernet** (plugged into an upstream router) *or* **WiFi in station mode**
  (joined to an existing WiFi network). This carries the mesh connection to the
  vpn-server. **The uplink is ordinary OS networking, not a vpn-router setting**
  — you configure it the normal way (DHCP on ethernet, or `nmcli`/
  `wpa_supplicant` to join a WiFi network). The router keeps the host default
  route on the uplink and only policy-routes the *downstream* traffic through
  the tun, so the tunnel's own carrier survives.
- **Downstream** (`--lan-ifc`) — the interface clients connect to: an
  **ethernet NIC** (wired clients) or the **WiFi radio in AP mode** (wireless
  clients). This is what you point the vpn-router at.

The uplink and downstream must be **different** interfaces. Which interface
plays which role gives the deployment modes below.

---

## Deployment modes

The three standard modes cover the useful uplink × downstream combinations:

### Mode 1 — Ethernet uplink + WiFi downstream  (the "WiFi VPN router")

The board plugs into your existing router by **ethernet** for internet, and
serves clients over **WiFi**.

```
 upstream router ──eth──▶ [ eth0=uplink | wlan0=AP ] board ──WiFi──▶ phones / laptops
```

```bash
# /etc/skywire.conf   (uplink eth0 = ordinary DHCP, nothing to set here)
VPNROUTER=true
VPNROUTERLANIFC='wlan0'
VPNROUTERWIFI=true
VPNROUTERSSID='skywire-vpn'
VPNROUTERPASSPHRASE='choose-a-strong-passphrase'   # 8–63 chars, or VPNROUTEROPEN=true
VPNROUTERBAND='2.4'        # or '5'
VPNROUTERCHANNEL=0         # 0 = default for the band
VPNROUTERCOUNTRY='US'
```

This is the classic single-box "plug it in, connect to its WiFi, you're on the
VPN." Needs a working WiFi radio in AP mode — see
[WiFi-out prerequisites](#wifi-out-prerequisites) and the rtl8723bs note below.

### Mode 2 — WiFi uplink + Ethernet downstream

The board joins an existing **WiFi network** for internet (station mode) and
serves clients out its **ethernet** port. Handy where the board sits where only
WiFi reaches, but you want to plug devices in by cable.

```
 existing WiFi ──WiFi(station)──▶ [ wlan0=uplink | eth0=downstream ] board ──eth──▶ wired clients / switch
```

```bash
# First, join the upstream WiFi the normal way (this is the UPLINK, not a
# vpn-router setting):
nmcli device wifi connect "UpstreamSSID" password "…"      # or wpa_supplicant

# /etc/skywire.conf
VPNROUTER=true
VPNROUTERLANIFC='eth0'     # serve wired clients out ethernet
# (no VPNROUTERWIFI — the downstream is wired)
```

Plug a client (or a switch / dumb AP) into `eth0`.

### Mode 3 — Ethernet uplink + Ethernet downstream  (all-wired)

The most robust option — no WiFi at all. Needs **two ethernet interfaces**
(onboard + a USB-ethernet dongle, or a two-port board).

```
 upstream router ──eth──▶ [ eth0=uplink | eth1=downstream ] board ──eth──▶ wired clients / switch
```

```bash
# /etc/skywire.conf
VPNROUTER=true
VPNROUTERLANIFC='eth1'     # the SECOND NIC (NOT the uplink eth0)
```

!!! note "The fourth combination (WiFi uplink + WiFi downstream)"

    Using WiFi for *both* uplink and downstream needs **two radios** (one in
    station mode, one in AP mode) — a single radio can't reliably do both at
    once. With a USB WiFi dongle added it's possible (onboard radio = AP,
    dongle = station, or vice-versa), but it's not a standard single-box mode.

!!! tip "rtl8723bs on the original skyminer boards (Modes 1 & 4)"

    The onboard **rtl8723bs** is a 2.4 GHz-only SDIO radio whose AP mode is
    firmware-fragile. The router disables its power-save before starting hostapd
    (the single most effective stability fix), and in testing the AP then
    beacons stably and serves clients. If a given board still flaps AP mode, pin
    power management off persistently (see
    [WiFi-out prerequisites](#wifi-out-prerequisites)) or use a **USB WiFi
    dongle** (e.g. an MT7612U — turns any board into a stable dual-band AP).
    Modes 2 and 3 avoid the AP path entirely.

---

## Configuring it

All variants are settable three equivalent ways:

- **`/etc/skywire.conf`** — set the `VPNROUTER*` variables above; the packaged
  auto-config (postinstall) runs `config gen` from them. The template in
  `skywire.conf` documents every variable.
- **`skywire autoconfig`** — e.g.
  `skywire autoconfig --vpnrouter --vpnrouter-lan-ifc wlan0 --vpnrouter-wifi --vpnrouter-ssid skywire-vpn --vpnrouter-passphrase … --vpnrouter-band 2.4`
  writes those lines and regenerates the config.
- **`skywire cli config gen`** — the same flags directly emit a config.

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
| `--vpnrouter-mesh-gateway` | `VPNROUTERMESHGW` | resolve `.dmsg`/`.skynet` for clients (see below) |
| `--vpnrouter-mesh-gateway-cidr` | `VPNROUTERMESHGWCIDR` | synthetic-IP pool (default `100.64.0.0/16`) |
| `--vpnrouter-mesh-gateway-tls` | `VPNROUTERMESHTLS` | TLS-MITM HTTPS to mesh names |

The app also accepts `--tun-ifc` (which tunnel to NAT into; default = first
`tun*`), and `--dhcp-start` / `--dhcp-end` / `--dns` / `--lease` for the DHCP
pool.

You still need a **vpn-client** pointed at a vpn-server
(`skywire cli vpn start -k <server-pk>`, or the VPN config knobs) — the router
NATs into whatever tunnel it brings up.

---

## What the router does at startup

1. Assigns the gateway address to `--lan-ifc` and brings it up.
2. WiFi-out only: disables interface power-save, writes `hostapd.conf`, starts hostapd.
3. Serves **DHCP + DNS** on the downstream subnet (an embedded pure-Go engine — no dnsmasq).
4. Waits for the vpn-client `tun`, then:
    - masquerades the downstream subnet out the tun,
    - **policy-routes** traffic forwarded in from the downstream interface through the tun (keeping the host uplink and the router's own local services on the main table),
    - **clamps forwarded TCP MSS** to the path MTU (so clients need no MTU tweaks).

All of it is reversed on shutdown.

---

## WiFi-out prerequisites

For the WiFi-out variant the downstream radio must be free for hostapd:

- **Release it from NetworkManager.** If NM manages the radio it fights hostapd.
  Mark it unmanaged:

    ```bash
    nmcli device set wlan0 managed no
    ```

    (or add it to `[keyfile] unmanaged-devices` in NetworkManager.conf).

- **Persistent power-save off (rtl8723bs and relatives).** The router disables
  power-save at runtime, but some drivers reset it on link-up. To pin it:

    ```bash
    echo 'options rtl8723bs rtw_power_mgnt=0 rtw_ips_mode=0' \
      | sudo tee /etc/modprobe.d/rtl8723bs.conf
    ```

- **hostapd installed** (`apt install hostapd` / equivalent).

---

## Verifying

From a downstream client (after it gets a DHCP lease):

```bash
ip -4 addr show                 # a 192.168.42.x lease
ping 192.168.42.1               # the router gateway answers
nslookup skycoin.com 192.168.42.1   # the router's embedded DNS resolves
ping 8.8.8.8                    # forward path: client → router → tun → server → internet
curl https://api.ipify.org      # your public IP == the vpn-server's IP
```

!!! note "Checking the VPN exit IP"

    Skywire-infrastructure hosts — the dmsg servers, discovery services, and
    `skycoin.com` — are **exempted** from the tunnel by design, so an IP check
    against `ip.skycoin.com` shows your *real* IP. Use a neutral service (e.g.
    `api.ipify.org`) to see the VPN exit IP.

---

## Mesh gateway — reach `.dmsg` / `.skynet` by name

The router can also let downstream clients reach mesh services **by name** —
`curl http://<pk>.dmsg`, or open `http://<pk>.skynet` in a browser — with no
per-device setup and no SOCKS proxy. Enable it with `VPNROUTERMESHGW=true`
(`--vpnrouter-mesh-gateway`).

A `.dmsg` / `.skynet` name is not an ordinary hostname: it encodes a **public
key**, and the destination is a **routing port reachable only over the mesh** —
often there is no externally-listening TCP port and no IP to route to. So the
gateway can't just NAT it. It works in two halves:

1. **DNS** — the router's resolver answers `*.dmsg` / `*.skynet` with a leased
   **synthetic IP** from a private pool (`VPNROUTERMESHGWCIDR`, default
   `100.64.0.0/16`).
2. **Transparent proxy** — an `iptables` REDIRECT sends TCP aimed at that pool to
   a local proxy, which recovers the original destination (the synthetic IP → the
   PK; the **original port is the mesh routing port**), dials it over the mesh,
   and splices the streams.

The mesh path **bypasses the VPN tunnel** — mesh services are reached directly
over the visor's own transports, not through the exit server.

**Names.** By default a client uses the raw key label, e.g.
`http://<pk-in-dns-label-form>.dmsg`. Friendly aliases are configurable on the
app (`--mesh-alias name=<pk>`, repeatable) so `http://<name>.dmsg` works.

**HTTPS.** Set `VPNROUTERMESHTLS=true` (`--vpnrouter-mesh-gateway-tls`) to reach
mesh sites over `https://`. The router terminates TLS with a leaf it mints on the
fly from a **self-generated CA**, persisted under `<local>/mesh-gateway-ca/`. Its
path + fingerprint are logged on first start; install that CA as **trusted** on
the LAN clients that need HTTPS, or browsers will (correctly) warn.

```bash
# from a downstream client, with the mesh gateway enabled:
curl http://<pk-label>.dmsg/            # plaintext, works out of the box
curl --cacert mesh-gateway-ca.pem https://<pk-label>.dmsg/   # after trusting the CA
```

!!! note "Same capability on a single machine"

    The `vpn-client` carries the same mesh gateway (`--mesh-gateway`, opt-in,
    Linux only) for a host with no downstream clients: it runs a loopback
    resolver and intercepts the host's own traffic via the `OUTPUT` chain instead
    of serving a LAN. It is off by default because hijacking a personal machine's
    DNS is more intrusive than doing so on a dedicated router.

---

## Troubleshooting

!!! failure "A WiFi client can't scan / sees no networks"

    A stuck Realtek staging driver can stop scanning entirely. Reload it:

    ```bash
    sudo modprobe -r r8723bs && sudo modprobe r8723bs
    ```

    Then rescan (`nmcli device wifi rescan`). Confirmed to restore a client
    radio that was seeing 0 networks.

!!! failure "Client associates + gets a lease but can't reach the gateway/DNS"

    Fixed in the router itself (the LAN→tun policy rule now matches by input
    interface, so the gateway's own replies aren't misrouted into the tun).
    Ensure you're on a build that includes that fix. If you carry a custom
    policy rule, match `iif <lan-ifc>`, not `from <subnet>` — the gateway IP is
    inside the subnet, so a `from`-rule black-holes the router's replies.

!!! failure "AP mode flaps (AP-ENABLED → AP-DISABLED) on rtl8723bs"

    Pin power management off persistently (see
    [prerequisites](#wifi-out-prerequisites)) or switch to a USB WiFi dongle /
    the ethernet-out variant.

!!! failure "Large downloads stall while ping works"

    The router MSS-clamps forwarded TCP automatically; make sure you're on a
    build that includes that. Otherwise lower the client MTU (~1360).

!!! failure "vpn-router exits: `no VPN tunnel interface appeared`"

    The `vpn-client` isn't connected. Start it (`skywire cli vpn start -k
    <server-pk>`) so a `tun` exists for the router to NAT into.
