# VPN client (vpn-client)

`vpn-client` is the client half of the [Skywire VPN](README.md). It opens a
persistent Skywire connection to a configured remote [VPN server](README.md)
and uses it as a tunnel, forwarding **all** of the machine's traffic through
the server. Your public IP becomes the VPN server's IP.

!!! note "Separate machines"

    The VPN client and VPN server must run on **different machines** (see the
    [server page](README.md)).

## Usage

The client is controlled via `skywire cli vpn`.

```bash
# List available VPN servers (from service discovery)
skywire cli vpn list

# Start the VPN against a remote server
skywire cli vpn start --pk <server-public-key>

# Status
skywire cli vpn status

# Stop
skywire cli vpn stop

# Open / print the VPN UI
skywire cli vpn ui
skywire cli vpn url
```

## Configuration

`vpn-client` ships in a generated config (port `43`, `auto_start: false`).
To start it automatically against a fixed server, set `-srv` (the server's
public key) and `-passcode` if required, and flip `auto_start`:

```json5
{
  "name": "vpn-client",
  "args": [
    "-srv", "03e9019b3caa021dbee1c23e6295c6034ab4623aec50802fcfdd19764568e2958d",
    "-passcode", "1234"
  ],
  "auto_start": false,
  "port": 43
}
```

- `-srv` (required) — public key of the remote VPN server.
- `-passcode` (optional) — passcode to authenticate the connection; omit if
  the server has none.

## Verifying

With the VPN running, you should see an extra hop in `traceroute`, and your
detected public IP should be the VPN server's:

```bash
traceroute google.com
curl https://api.ipify.org
```

## See also

- [VPN server](README.md)
- [Command reference: `skywire cli vpn`](../skywire/README.md)
