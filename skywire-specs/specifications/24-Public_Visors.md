# Public Visors

A *Public Visor* is a visor configured to serve as a bootstrap node for other visors on the Skywire network. Public visors advertise themselves in the *Service Discovery* and accept incoming STCPR (Skywire TCP Relay) transports from other visors.

## Overview

Public visors serve a critical role in network bootstrapping:

- **Bootstrap nodes**: New visors connect to public visors to establish initial network presence
- **Relay capability**: Provide transport endpoints for visors behind NAT or firewalls
- **Network mesh expansion**: Facilitate SUDPH (Skywire UDP Hole Punch) connections between visors

Unlike regular visors, public visors must be externally reachable, either via:
- A public IP address with no NAT
- Port forwarding configured on the router (forwarding the STCPR port, default 7777)

## Code Structure

The public visor logic is distributed across several packages:

- `/pkg/visor/init.go` - `initPublicVisor()` initializes public visor registration
- `/pkg/app/appdisc/discovery_manager.go` - `PublicVisorUpdater` handles registration lifecycle
- `/pkg/transport/network/client.go` - External STCPR detection logic
- `/pkg/visor/visorconfig/v1.go` - `PublicVisorConfig` configuration structure

## Configuration

Public visor behavior is configured in the visor's JSON configuration:

```json
{
  "is_public": true,
  "public_visor_config": {
    "registration_timeout": "10m",
    "max_transports": 1000
  },
  "stcpr_port": 7777
}
```

### Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `is_public` | boolean | `false` | Enables public visor mode |
| `registration_timeout` | duration | `10m` | Time to wait for external connection validation |
| `max_transports` | integer | `1000` | Maximum transport count before deregistering |
| `stcpr_port` | integer | `7777` | Port for incoming STCPR connections |

### Configuration Constants

Defined in `pkg/skyenv/skyenv.go`:

| Constant | Value | Description |
|----------|-------|-------------|
| `PublicVisorRegistrationTimeout` | 10 minutes | Default registration timeout |
| `STCPAddr` | `:7777` | Default STCPR listen address |
| `IsPublic` | `false` | Default public visor setting |

Defined in `pkg/visor/visorconfig/values.go`:

| Variable | Value | Description |
|----------|-------|-------------|
| `PublicVisorMaxTransports` | 1000 | Default max transports |

## Registration Lifecycle

### Startup

When a visor starts with `is_public: true`:

1. The `initPublicVisor()` function is called during visor initialization
2. A `PublicVisorUpdater` is created with the configured timeout and max transports
3. The visor registers with the *Service Discovery* as a public visor
4. A monitoring loop begins to validate external reachability

```
┌─────────────────────────────────────────────────────────────────┐
│                    Public Visor Startup                        │
├─────────────────────────────────────────────────────────────────┤
│  1. initPublicVisor() called                                   │
│  2. Create PublicVisorUpdater(timeout=10m, maxTp=1000)        │
│  3. Register with Service Discovery                            │
│  4. Start monitoring loop                                       │
│  5. Wait for external STCPR connection...                      │
└─────────────────────────────────────────────────────────────────┘
```

### External Connection Validation

The visor must receive at least one STCPR connection from an external (non-private) IP address within the registration timeout to be considered "validated."

**Detection Logic** (in `pkg/transport/network/client.go`):

```go
// When accepting STCPR connection:
if !isPrivateIP(remoteAddr.IP) {
    // External connection detected - visor is validated
    onExternalSTCPR()
}
```

**Private IP Ranges** (excluded from validation):

| Range | Description |
|-------|-------------|
| `10.0.0.0/8` | RFC1918 Private |
| `172.16.0.0/12` | RFC1918 Private |
| `192.168.0.0/16` | RFC1918 Private |
| `100.64.0.0/10` | RFC6598 CGNAT |
| `127.0.0.0/8` | Loopback |
| `fc00::/7` | IPv6 Unique Local |
| Link-local addresses | IPv4/IPv6 link-local |

### Deregistration Conditions

A public visor will automatically deregister from *Service Discovery* under two conditions:

**1. Validation Timeout**

If no external STCPR connection is received within `registration_timeout`:

```
┌──────────────────────────────────────────────────────────────┐
│  Timer starts at registration                                │
│  ↓                                                           │
│  Wait up to 10 minutes for external STCPR...                │
│  ↓                                                           │
│  No external connection received?                            │
│  ↓                                                           │
│  Log: "Public visor validation timeout: no external STCPR   │
│        connection received. Deregistering from service      │
│        discovery."                                           │
│  ↓                                                           │
│  Deregister (reason: "no_external_stcpr")                   │
└──────────────────────────────────────────────────────────────┘
```

**2. Maximum Transports Reached**

If the transport count reaches `max_transports`:

```
┌──────────────────────────────────────────────────────────────┐
│  Transport count checked every 1 minute                     │
│  ↓                                                           │
│  count >= max_transports (default 1000)?                    │
│  ↓                                                           │
│  Log: "Public visor reached max transports (X/1000).        │
│        Deregistering from service discovery."               │
│  ↓                                                           │
│  Deregister (reason: "max_transports")                      │
└──────────────────────────────────────────────────────────────┘
```

### State Diagram

```
                    ┌─────────────┐
                    │   Startup   │
                    └──────┬──────┘
                           │
                           ▼
                    ┌─────────────┐
                    │ Registered  │◄────────────────┐
                    │ (Unvalidated)│                │
                    └──────┬──────┘                │
                           │                        │
            ┌──────────────┼──────────────┐        │
            │              │              │        │
            ▼              ▼              ▼        │
    ┌───────────┐  ┌───────────┐  ┌───────────┐   │
    │  Timeout  │  │ External  │  │ Max Tps   │   │
    │ (10 min)  │  │  STCPR    │  │ Reached   │   │
    └─────┬─────┘  └─────┬─────┘  └─────┬─────┘   │
          │              │              │         │
          │              ▼              │         │
          │       ┌─────────────┐       │         │
          │       │ Registered  │───────┘         │
          │       │ (Validated) │                 │
          │       └──────┬──────┘                 │
          │              │                        │
          │              │ Max Tps Reached        │
          │              ▼                        │
          │       ┌─────────────┐                 │
          └──────►│Deregistered │                 │
                  └─────────────┘                 │
                                                  │
                  (Restart required to           │
                   re-register)                   │
```

## Service Discovery Registration

Public visors register as service type `visor` with the *Service Discovery*:

**Registration Request:**

```
POST /api/services
Content-Type: application/json
SW-Public: <visor-public-key>
SW-Nonce: <nonce>
SW-Sig: <signature>

{
    "address": "<pk>:<stcpr_port>",
    "type": "visor",
    "geo": { ... },
    "version": "<visor-version>"
}
```

**Heartbeat Updates:**

The `PublicVisorUpdater` sends periodic heartbeat updates to maintain registration. The update interval is controlled by `ServiceDiscUpdateInterval` (default 90 seconds).

## Network Requirements

### Port Forwarding

For visors behind NAT, the STCPR port must be forwarded:

| Protocol | Internal Port | External Port | Description |
|----------|--------------|---------------|-------------|
| TCP | 7777 | 7777 | STCPR transport connections |

### Firewall Rules

The following inbound connections must be allowed:

| Port | Protocol | Source | Description |
|------|----------|--------|-------------|
| 7777 | TCP | Any | STCPR connections from other visors |

## Design Considerations

### Validation Requirements

- A public visor requires exactly ONE external STCPR connection to be validated
- There is no minimum transport count requirement for continued registration
- Connections from private IP ranges (RFC1918, RFC6598, loopback, link-local) do not count as external
- If all connecting visors are on the same LAN, no external connections will be detected

### Disabling Validation

Setting `registration_timeout: 0` disables the external connection validation check entirely. The visor will remain registered regardless of whether it receives external connections.

### Security Considerations

- Public visors are advertised in *Service Discovery* and accessible to any visor
- No authentication is required to establish transports (by design)
- Transport-level encryption is handled by the transport protocol
