# App Server

The *App Server* (`pkg/app/appserver/`) manages the lifecycle of Skywire applications within a visor. It handles process management, inter-process communication, and network connectivity for apps.

## Application Types

### Native Apps (In-Process)

Native apps run as goroutines within the visor process:
- **skychat** — messaging over Skywire routes
- **vpn-server** / **vpn-client** — VPN over Skywire routes
- **skysocks** / **skysocks-client** — SOCKS5 proxy over Skywire routes
- **skynet** — skynet forwarding server

### External Apps

External apps run as separate processes. Communication with the visor uses a pair of UNIX pipes (file descriptors 3 and 4) inherited by the forked process.

## App Configuration

Apps are configured in the `launcher.apps` section of the visor config:

```json
{
  "name": "skychat",
  "args": ["--addr", ":8001"],
  "auto_start": true,
  "port": 1
}
```

| Field | Description |
|---|---|
| `name` | App binary name (looked up in `launcher.bin_path`) |
| `args` | Command-line arguments passed to the app |
| `auto_start` | Whether to start the app on visor startup |
| `port` | Skywire routing port the app listens on |

## App Network Interface

Apps communicate over the Skywire network via the `appnet` package. When an app opens a connection to a remote visor, the visor's router establishes a route and provides the app with a bidirectional stream.

The app server tracks:
- Running app processes and their PIDs
- App connection states
- App logs (ring buffer accessible via `visor app log <name>`)

## Runtime App Management

| Action | CLI command |
|---|---|
| List apps | `skywire cli visor app ls` |
| Start app | `skywire cli visor app start <name>` |
| Stop app | `skywire cli visor app stop <name>` |
| Set autostart | `skywire cli visor app arg autostart <name> true\|false` |
| Set killswitch | `skywire cli visor app arg killswitch <name> true\|false` |
| Set secure mode | `skywire cli visor app arg secure <name> true\|false` |
| Set network interface | `skywire cli visor app arg netifc <name> <iface>` |
| View logs | `skywire cli visor app log <name>` |
