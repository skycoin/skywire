# Address Resolver Privacy Controls

## Overview

Visors gain optional configuration to control their address resolver (AR) registration, allowing users to limit their network discoverability for privacy or resource management purposes.

## Motivation

The address resolver stores a visor's network address (IP + port) so other visors can establish transports to it. This is necessary for inbound connections but exposes the visor's location on the network. Some users may want to:

- Limit inbound connections after establishing enough transports
- Operate as a "dark" visor reachable only through already-established transports and DMSG
- Prevent their IP address from being stored in the address resolver entirely

The RSN (Route Setup Node) also uses this mechanism: it does not register with AR because it initiates all its own transports outward and does not accept unsolicited inbound connections.

## Configuration

New field in the visor config `transport` section:

```json
{
  "transport": {
    "ar_transport_limit": 0
  }
}
```

### Behavior

| Value | Behavior |
|-------|----------|
| `0` (default) | Normal — register with AR and stay registered indefinitely |
| `N > 0` | Register with AR initially. Deregister after `N` transports are established. Stop accepting new inbound transports. |
| `N < 0` | Never register with AR. The visor is invisible to the address resolver. Only outbound transports and DMSG connections are possible. |

### What "deregistered" means

When a visor deregisters from AR (or never registers):
- Its address is removed from (or never added to) the AR database
- Other visors cannot resolve its network address via AR
- No new STCPR or SUDPH inbound connections can be established
- Existing transports remain functional and are maintained normally
- The visor can still INITIATE outbound transports to other visors
- The visor is still reachable via DMSG (DMSG sessions are independent of AR)
- The visor is still reachable through already-established transports

### Interaction with Public Visor Mode

A visor configured as a public visor (`public_autoconnect: true`) advertises itself in service discovery so other visors can autoconnect to it. This requires AR registration — otherwise no visor can establish a transport to it.

| `public_autoconnect` | `ar_transport_limit` | Behavior |
|----------------------|---------------------|----------|
| `false` | `0` | Normal visor, stays in AR |
| `false` | `N > 0` | Deregister after N transports |
| `false` | `N < 0` | Never register (dark mode) |
| `true` | `0` | Public visor, stays in AR (normal) |
| `true` | `N > 0` | Public visor, deregister after N transports. Log info message. Useful for bandwidth-limited public visors that want to serve a limited number of peers. |
| `true` | `N < 0` | **Conflict.** Log warning, ignore `ar_transport_limit`. Public visors must be discoverable. |

### Interaction with Autoconnect

Autoconnect (the visor's own outbound transport creation to public visors) is unaffected by AR deregistration. A visor that has deregistered from AR can still:
- Initiate STCPR connections to public visors (using their AR-registered addresses)
- Initiate SUDPH connections via the address resolver's relay (the visor sends but doesn't need to be registered itself)
- Maintain all existing transports

### Transport Count

The transport count for the `ar_transport_limit` threshold includes all active managed transports (STCPR + SUDPH), regardless of label or direction. The count is checked:
- After each new transport is established (inbound or outbound)
- Periodically (in case transports were closed and the count dropped below the threshold — the visor does NOT re-register; deregistration is permanent until visor restart)

### Re-registration

Once deregistered, the visor does NOT automatically re-register if transports are lost. Deregistration is a one-way action for the lifetime of the visor process. The rationale: if the user configured a limit, they want to control their visibility. Automatic re-registration would undermine that intent.

To re-register, the visor must be restarted (or the config changed and reinitialize triggered).

## CLI Support

```
skywire cli config show .transport.ar_transport_limit
```

Runtime changes (future):
```
skywire cli visor ar-limit <N>
```

This would update the limit at runtime without restart. Setting it to 0 would re-register with AR. Setting it to -1 would deregister immediately.

## Implementation

### Config

Add `ARTransportLimit` to the transport config struct:

```go
type Transport struct {
    // ... existing fields ...
    ARTransportLimit int `json:"ar_transport_limit,omitempty"`
}
```

### Transport Manager

After each transport is established, check the count against the limit:

```go
func (tm *Manager) checkARLimit() {
    if tm.conf.ARTransportLimit <= 0 {
        return // 0 = no limit, negative = never registered
    }
    if tm.TransportCount() >= tm.conf.ARTransportLimit {
        tm.deregisterFromAR()
    }
}
```

### Initialization

During visor startup, if `ARTransportLimit < 0`:
- Skip AR registration entirely
- Log: "Address resolver registration disabled (ar_transport_limit < 0)"

If `ARTransportLimit > 0`:
- Register normally
- Start monitoring transport count

### Address Resolver Client

Add a `Deregister()` method to the AR client that removes the visor's entry:
- DELETE request to the AR's deregistration endpoint
- Clear the local registration state
- Stop the periodic re-registration heartbeat
