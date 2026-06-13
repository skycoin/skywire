# Apps

Skywire visors host **apps** — programs that ride the encrypted Skywire
network instead of the open internet. Each app is addressed by the hosting
visor's public key (not an IP), runs over whatever transport the router
selected (STCPR, SUDPH, or DMSG), and is gated by the visor's whitelist
where access control applies.

Most apps come in a **server**/**client** pair: one visor hosts the service,
another connects to it. They are configured in `skywire-config.json` under
`apps` (start on boot with `auto_start`) and controlled at runtime through
`skywire cli` or the hypervisor UI.

## Which app do I want?

| You want to… | Use | Why |
|---|---|---|
| Route **all** of a machine's traffic through a remote exit | **[VPN](vpn/README.md)** | IP-level tunnel; your public IP becomes the server's |
| Proxy **one application's** traffic (browser, curl, …) | **[SOCKS5 proxy](skysocks/README.md)** | Per-application SOCKS5; lighter than a full VPN |
| Expose/reach a **specific TCP port** on a remote visor | **[SkyNet](skynet/README.md)** | Peer-to-peer port forwarding (web servers, SSH, databases) |
| Open a **remote shell / transfer files** on a visor | **[PTY](pty/README.md)** | Key-addressed shell + sftp over the pty subsystem |
| **Chat** with other visors | **[Skychat](skychat/README.md)** | Direct and group messaging |
| Reach a visor or deployment service **by public key** from a browser / `curl` over dmsg | **[Resolving proxy](guides/resolving-proxy.md)** | `.dmsg` / `.skynet` SOCKS5 resolver (`skywire cli resolver`); the supported way to view deployment-service data without a clearnet hop |

VPN vs. SOCKS5 is the most common decision: the **VPN** captures everything
at the IP layer (and must run server and client on *different machines*),
while the **SOCKS5 proxy** only carries traffic from applications you point
at its local port — no host-network reconfiguration, and both ends can even
be the same machine.

## App reference

| App | Role | CLI | Default port |
|---|---|---|---|
| [PTY](pty/README.md) | shell / sftp host | `skywire cli pty` | dmsg `22` |
| [Skychat](skychat/README.md) | messaging | `skywire cli skychat` | `1` |
| [SkyNet server](skynet/README.md) | port-forward host | `skywire cli skynet srv` | forwarding `57` |
| [SkyNet client](skynet/client.md) | port-forward client | `skywire cli skynet` | `57` |
| [SOCKS5 server](skysocks/README.md) | SOCKS5 proxy host | `skywire cli proxy server` | `3` |
| [SOCKS5 client](skysocks/client.md) | SOCKS5 proxy client | `skywire cli proxy` | `13` |
| [VPN server](vpn/README.md) | VPN host | `skywire cli vpn server` | `44` |
| [VPN client](vpn/client.md) | VPN client | `skywire cli vpn` | `43` |

Ports are the visor's internal app-routing ports (set per app in
`skywire-config.json`), not host network ports.

## Configuring an app

Every app entry in `skywire-config.json` has the same shape:

```json
{
  "name": "skysocks",
  "args": ["-passcode", "123456"],
  "auto_start": true,
  "port": 3
}
```

- `name` — the app binary.
- `args` — app-specific flags (see each app's page).
- `auto_start` — start the app when the visor boots.
- `port` — the visor's internal routing port for the app.

See each app's page for its `args`, CLI commands, and examples. For the full
flag set of every command, see the
[Command Reference](skywire/README.md).
