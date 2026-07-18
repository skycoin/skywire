# vpn-router — Skywire VPN gateway / WiFi-AP role

`vpn-router` turns a visor into a **router**: downstream LAN/WiFi clients reach
the internet through the Skywire mesh VPN without running any Skywire software
themselves. A laptop or phone joins the router's WiFi (or plugs into its LAN
port) and its traffic is NATed into the mesh tunnel.

## Architecture

`vpn-router` is a **companion** to the existing `vpn-client` app, not a
replacement:

```
   downstream clients            this visor                    mesh
  ┌──────────────┐        ┌───────────────────────┐
  │ laptop/phone │  DHCP  │  vpn-router (this app) │
  │              │◀──────▶│   • hostapd (WiFi)     │
  │ 192.168.42.x │        │   • dnsmasq (DHCP/DNS) │        ┌────────────┐
  └──────────────┘        │   • NAT downstream→tun │        │ vpn-server │
        wlan0/eth1        │                        │        │ (exit)     │
                          │  vpn-client (companion)│──tun0──▶│            │──▶ internet
                          │   • owns tun0 tunnel   │  mesh   └────────────┘
                          └───────────────────────┘
```

- **`vpn-client`** owns the tunnel interface (`tun0`) to a chosen VPN server —
  unchanged.
- **`vpn-router`** owns the *downstream* side: it brings up the LAN/WiFi
  interface, runs DHCP+DNS, optionally beacons a WiFi AP, and NATs the client
  subnet into `tun0`.

Enable **both** apps (AutoStart) on the gateway visor. `vpn-router` waits for the
tunnel interface to appear (up to 90 s), so start order doesn't matter.

### Two variants, one code path

| Variant | `LANInterface` | Extra daemon | Hardware |
|---|---|---|---|
| **Ethernet-out** | a 2nd wired NIC (`eth1`) | dnsmasq only | any Linux SBC incl. the existing Skyminer boards |
| **WiFi-out** | AP-capable wireless NIC (`wlan0`) | dnsmasq + **hostapd** | Pi onboard radio, or a MT7612U USB dongle |

The WiFi variant is the ethernet variant **plus** hostapd beaconing an SSID on
the same interface. See `reference_vpn_router_hardware` for chipset notes — many
cheap onboard radios (AIC8800 fullMAC) can't do AP mode; a MT7612U dongle is the
reliable fallback.

## Code map

| Piece | File |
|---|---|
| Config + pure config-file generators (`DnsmasqConf`, `HostapdConf`) | `pkg/vpn/router.go` |
| Linux orchestration (interface, daemons, NAT, teardown) | `pkg/vpn/router_linux.go` |
| Non-Linux stub | `pkg/vpn/router_other.go` |
| Launcher app (`RunVPNRouter`, flags) | `cmd/apps/vpn-router/commands/vpn-router.go` |
| NAT/forwarding helpers (reused from vpn-server) | `pkg/vpn/os_server_linux.go` |

The NAT is exactly the vpn-server primitives applied to the tunnel:
`EnableIPv4Forwarding()`, `EnableIPMasquerading(tun0)`, plus targeted
`FORWARD` rules `LAN↔tun0`. On shutdown everything is reversed (rules removed,
masquerade dropped, forwarding restored, downstream address flushed, daemons
reaped via context).

## Enabling it on a visor

`vpn-router` is registered as a launcher app but is **not** in the default
generated config yet (config-gen default inclusion is a follow-up). Add it to
`skywire.json` under `launcher.apps`, alongside an auto-started `vpn-client`:

```json
{
  "name": "vpn-client",
  "auto_start": true,
  "port": 43,
  "args": ["-srv", "<VPN_SERVER_PK>"]
},
{
  "name": "vpn-router",
  "auto_start": true,
  "port": 0,
  "args": ["--lan-ifc", "wlan0", "--subnet", "192.168.42.1/24",
           "--wifi", "--ssid", "Skywire", "--passphrase", "changeme8+"]
}
```

Ethernet-out is the same without the WiFi flags:
`["--lan-ifc", "eth1", "--subnet", "192.168.42.1/24"]`.

### Flags

`--lan-ifc` (required), `--tun-ifc` (auto-detect if empty), `--subnet
<gateway-ip>/<prefix>`, `--dhcp-start/-end`, `--dns`, `--lease`; WiFi:
`--wifi --ssid --passphrase [--band 2.4|5] [--channel N] [--country XX] [--open]`.

## Privileges

Like the other VPN apps, `vpn-router` needs `CAP_NET_ADMIN` / root: it runs
`ip`, `iptables`, `sysctl`, `hostapd`, `dnsmasq`. It shells these out via the
same `osutil.RunElevated` path vpn-server uses. Requires `hostapd` and `dnsmasq`
installed on the host.

## Test status

- **Unit-tested (CI):** config validation, DHCP-pool defaults, and the generated
  `dnsmasq.conf` / `hostapd.conf` content (`pkg/vpn/router_test.go`).
- **Build-verified:** compiles on Linux and (as a stub) non-Linux; the app
  registers and its CLI flags parse.
- **Needs hardware:** the live interface/daemon/NAT path is only exercisable on a
  real router box. Validation procedure:
  1. Board with two interfaces (e.g. Pi 4: `eth0` uplink + `wlan0` AP, or an SBC
     with `eth0`+`eth1`), `hostapd`+`dnsmasq` installed, running as root.
  2. Configure `vpn-client` to a working `vpn-server`; confirm `tun0` is up and
     the visor itself has mesh internet.
  3. Enable `vpn-router` as above; join the SSID / plug into the LAN port from a
     second device.
  4. Expect: DHCP lease in `192.168.42.0/24`, DNS resolves, and the client's
     public IP is the **vpn-server's** exit IP (not the uplink's).

## Follow-ups

- Add `vpn-router` to config-gen behind a flag (and the internal/external app
  templates), updating the golden config fixtures.
- HV UI settings page (mirror the vpn-client component).
- Optional self-contained mode (embed the tunnel so it's one app/lifecycle
  instead of a vpn-client companion).
- Client-isolation / per-PK ACL (reuse vpn-server's `--secure` / whitelist idea).
