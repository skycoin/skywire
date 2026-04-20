# Hypervisor

The *Hypervisor* is a web-based management interface for remotely managing one or more visors. It is identified by its public key and serves an HTTP UI over DMSG.

## Architecture

The hypervisor runs within a visor process when `hypervisor.enable` is true in the visor config. It serves a web UI on the configured HTTP address (default `:8000`) and listens on DMSG port 46 (`DmsgHypervisorPort`) for incoming RPC connections from remote visors.

Remote visors connect to the hypervisor by including its public key in their `hypervisors` config field. On startup, each remote visor dials the hypervisor on DMSG port 46 and serves an RPC interface, allowing the hypervisor to manage it.

## Authentication

- The DMSG connection between visor and hypervisor is authenticated and encrypted via the Noise protocol (XK handshake pattern) with the visor as initiator.
- The web UI supports optional password authentication (`hypervisor.enable_auth`). Passwords are stored as bcrypt hashes in a local database (`hypervisor.db_path`).
- CSRF token protection is enabled by default for sensitive API endpoints.

## Web UI Features

The hypervisor web UI provides:

- **Node overview** — list of connected visors with status, PK, version, uptime
- **Transport management** — view, add, remove transports on any connected visor
- **App management** — start, stop, configure applications
- **Route management** — view routing rules, route groups
- **Skynet tab** — forwarded ports, reverse proxy connections
- **Proxy settings** — resolving proxy enable/disable, upstream configuration
- **Log viewer** — visor runtime logs
- **Terminal** — dmsgpty pseudoterminal access to remote visors

## Configuration

```json
{
  "hypervisor": {
    "enable": true,
    "db_path": "/path/to/users.db",
    "enable_auth": true,
    "dmsg_port": 46,
    "http_addr": ":8000",
    "enable_tls": false,
    "tls_cert_file": "./ssl/cert.pem",
    "tls_key_file": "./ssl/key.pem"
  },
  "hypervisors": ["<remote-hypervisor-pk>"]
}
```

| Field | Description |
|---|---|
| `hypervisor.enable` | Enable the hypervisor web UI on this visor |
| `hypervisor.db_path` | Path to the user/password database |
| `hypervisor.enable_auth` | Require password login for the web UI |
| `hypervisor.dmsg_port` | DMSG port for remote visor RPC connections (default 46) |
| `hypervisor.http_addr` | HTTP listen address for the web UI (default `:8000`) |
| `hypervisors` | List of remote hypervisor PKs this visor connects to |

## Runtime Management

| Action | CLI command |
|---|---|
| Enable hypervisor | `skywire cli visor hv enable` |
| Disable hypervisor | `skywire cli visor hv disable` |
| Check status | `skywire cli visor hv status` |
| Add remote hypervisor at runtime | `skywire cli visor hv add <pk>` |
| View connected hypervisor PKs | `skywire cli visor hv cpk` |
| Open UI in browser | `skywire cli visor hv ui` |
