# Testing In-Process App Execution

## Quick Test

You can now run the visor directly with `go run` without installing binaries:

```bash
# Generate config with in-process apps
go run . cli config gen -n > config.json

# Run the visor
go run . visor -c config.json
```

## Verify In-Process Execution

Check that apps are running as function calls:

```bash
# Generate and inspect config
go run . cli config gen -n | jq '.launcher.apps[] | {name, binary, args}'
```

Expected output - **no binary field**:
```json
{
  "name": "vpn-client",
  "binary": null,
  "args": ["--dns", "1.1.1.1"]
}
{
  "name": "skychat",
  "binary": null,
  "args": ["--addr", ":8001"]
}
```

## Test Individual Apps

### Skychat (auto-starts by default)
1. Generate config and start visor
2. Navigate to http://localhost:8001
3. Should see skychat UI

### VPN Client (manual start)
```bash
# Start visor
go run . visor -c config.json

# In another terminal, start vpn-client
go run . cli visor app start vpn-client --srv <server-pk>
```

### Skysocks (auto-starts by default)
Check that port 3 is listening for SOCKS5 connections.

## Compare: In-Process vs External

### In-Process (Default Now)
```json
{
  "name": "skychat",
  "args": ["--addr", ":8001"],
  "port": 1
}
```
- No binary field
- Args don't include "app", "skychat"
- Runs as goroutine in visor process

### External (For Custom Apps)
```json
{
  "name": "custom-app",
  "binary": "/path/to/custom-app",
  "args": ["--custom-flag"],
  "port": 50
}
```
- Has binary field
- Runs as separate process

## Troubleshooting

If apps don't start:
1. Check `go run . cli visor app ls` to see app status
2. Check visor logs for errors
3. Verify app is registered in registry (all 5 built-in apps should be)

## Android Testing

This implementation enables Android app development:
```go
// In Android app, apps run in-process
visor.Start() // All apps as goroutines
```

No external process execution required!
