# Public Autoconnect

The *Public Autoconnect* module is responsible for automatically establishing transports between visors on the Skywire network. It runs as a background process within each visor and periodically connects to public visors and other reachable nodes.

## Overview

The autoconnect system ensures network connectivity by:

- **Bootstrap connections**: Connecting new visors to public visors from *Service Discovery*
- **Mesh expansion**: Establishing SUDPH transports to visors connected to the same public visors
- **Fallback discovery**: Finding well-connected visors via *Transport Discovery* when no public visors are available
- **Connection maintenance**: Periodically checking and re-establishing connections

## Code Structure

The autoconnect logic is implemented in:

- `/pkg/visor/autoconnect.go` - Main autoconnect implementation
- `/pkg/visor/init.go` - Autoconnect initialization (`initPublicAutoconnect`)
- `/pkg/servicedisc/` - Service discovery client for fetching public visors
- `/pkg/skyenv/skyenv.go` - Configuration constants

## Configuration

Autoconnect behavior is configured in the visor's JSON configuration:

```json
{
  "public_autoconnect": true,
  "transport": {
    "public_autoconnect_interval": "5m"
  }
}
```

### Configuration Constants

Defined in `pkg/skyenv/skyenv.go`:

| Constant | Value | Description |
|----------|-------|-------------|
| `PublicAutoconnect` | `true` | Default autoconnect enabled |
| `PublicAutoconnectInterval` | 300s (5 min) | Interval between autoconnect cycles |

Defined in `pkg/visor/autoconnect.go`:

| Constant | Value | Description |
|----------|-------|-------------|
| `maxPublicVisors` | 5 | Maximum STCPR connections to public visors |
| `maxSUDPH` | 30 | Maximum SUDPH connections to other visors |
| `minFallbackTransports` | 5 | Minimum transports for fallback visor selection |

## Autoconnect Cycle

The autoconnect module runs a continuous loop with the following phases:

```
┌─────────────────────────────────────────────────────────────────┐
│                    Autoconnect Cycle                           │
├─────────────────────────────────────────────────────────────────┤
│  1. Fetch public visors from Service Discovery                 │
│  2. Filter out self and already-connected visors               │
│  3. Phase 1: STCPR to public visors (up to 5)                  │
│  4. Phase 2: SUDPH to connected visors (up to 30)              │
│  5. Sleep for PublicAutoconnectInterval (5 minutes)            │
│  6. Repeat                                                      │
└─────────────────────────────────────────────────────────────────┘
```

### Phase 1: STCPR to Public Visors

Connect to public visors via STCPR (Skywire TCP Relay):

```
┌──────────────────────────────────────────────────────────────┐
│  Fetch public visors from Service Discovery                 │
│  ↓                                                           │
│  Filter: Remove self, already connected                      │
│  ↓                                                           │
│  Shuffle remaining visors (randomize selection)             │
│  ↓                                                           │
│  For each visor (up to maxPublicVisors=5):                  │
│    ├─ Attempt STCPR transport                                │
│    ├─ On success: increment counter                          │
│    └─ On failure: log and continue                          │
│  ↓                                                           │
│  Result: Up to 5 STCPR connections to public visors         │
└──────────────────────────────────────────────────────────────┘
```

### Phase 2: SUDPH to Connected Visors

After establishing STCPR connections, attempt SUDPH (UDP Hole Punch) to other visors:

```
┌──────────────────────────────────────────────────────────────┐
│  Check if local visor supports SUDPH                        │
│  ↓                                                           │
│  Query Transport Discovery for per-key transport stats      │
│  ↓                                                           │
│  Find visors connected to the same public visors            │
│  ↓                                                           │
│  Filter: Remove self, already connected, public visors      │
│  ↓                                                           │
│  For each candidate (up to maxSUDPH=30):                    │
│    ├─ Attempt SUDPH transport                                │
│    ├─ On success: increment counter                          │
│    └─ On failure: log and continue                          │
│  ↓                                                           │
│  Result: Up to 30 SUDPH connections to other visors         │
└──────────────────────────────────────────────────────────────┘
```

## Transport Selection Logic

### STCPR (TCP Relay)

Used for connections to public visors:

- **Reliability**: TCP provides guaranteed delivery
- **NAT Traversal**: Works through most firewalls when public visor is reachable
- **Use Case**: Bootstrap connections, reliable fallback

### SUDPH (UDP Hole Punch)

Used for peer-to-peer connections:

- **Efficiency**: Direct UDP connection, lower latency
- **NAT Traversal**: Works with most NAT types (except symmetric)
- **Use Case**: Mesh connections between regular visors

### Transport Priority

```
1. STCPR to public visors (bootstrap)
2. SUDPH to peers (mesh expansion)
3. DMSG fallback (when direct transports unavailable)
```

## Fallback Visor Discovery

When *Service Discovery* returns no public visors, the autoconnect module falls back to querying *Transport Discovery* for well-connected visors:

```go
// fetchFallbackVisors queries TPD for visors with >= 5 transports
func fetchFallbackVisors(ctx context.Context, v *Visor) ([]cipher.PubKey, error) {
    const minFallbackTransports = 5

    perKeyStats, err := tpD.GetAllTransportsPerKeyStats(ctx)
    // Filter for visors with total >= minFallbackTransports
    // Return shuffled list of candidates
}
```

**Fallback Selection Criteria:**

| Criterion | Value | Description |
|-----------|-------|-------------|
| Minimum transports | 5 | Visor must have at least 5 transports |
| Exclude self | Yes | Don't include local visor |
| Randomization | Yes | Shuffle results for load distribution |

## Connection Limits

### Per-Visor Limits

| Transport Type | Limit | Description |
|----------------|-------|-------------|
| STCPR to public | 5 | Maximum STCPR connections to public visors |
| SUDPH to peers | 30 | Maximum SUDPH connections to other visors |
| Total transports | Unlimited | No hard limit on total transport count |

### Public Visor Limits

Public visors track their own limits (see [24-Public_Visors.md](24-Public_Visors.md)):

| Limit | Default | Description |
|-------|---------|-------------|
| `max_transports` | 1000 | Deregister from SD when reached |

## State Management

### Transport Cache

The autoconnect module maintains a cache of known transports to avoid redundant connection attempts:

```go
type transportCache struct {
    transports      map[uuid.UUID]*TransportSummary
    transportCounts map[cipher.PubKey]int
    mu              sync.RWMutex
}
```

### Connected Visor Tracking

Before attempting new connections, existing transports are checked:

```go
// Check if already connected to this visor
for _, tp := range existingTransports {
    if tp.Remote == targetPK {
        // Skip - already connected
        continue
    }
}
```

## Retry Logic

The autoconnect module uses exponential backoff for connection failures:

```go
// Default retrier configuration
retrier := netutil.NewDefaultRetrier()
// Initial backoff: 1 second
// Maximum backoff: 20 seconds
// Growth factor: 1.3x
// Max retries: unlimited
```

### Retry Behavior

| Scenario | Behavior |
|----------|----------|
| Transport creation fails | Log error, try next visor |
| Service Discovery unavailable | Retry with backoff |
| Transport Discovery unavailable | Fall back to cached data |
| All public visors unreachable | Wait for next cycle |

## Timing and Intervals

| Timer | Duration | Description |
|-------|----------|-------------|
| `PublicAutoconnectInterval` | 5 minutes | Main cycle interval |
| Initial delay | Random 0-60s | Stagger startup to avoid thundering herd |
| Connection timeout | 30 seconds | Per-transport creation timeout |

## Sequence Diagram

```
Visor                Service Discovery         Transport Discovery
  │                        │                          │
  │──GET /services/visor──►│                          │
  │◄─────[public visors]───│                          │
  │                        │                          │
  │ (Phase 1: STCPR)       │                          │
  │ Connect to PV1 ────────┼──────────────────────────┼──►
  │ Connect to PV2 ────────┼──────────────────────────┼──►
  │ ...up to 5             │                          │
  │                        │                          │
  │──────────────────────GET /transports/per-key-stats──►│
  │◄──────────────────────────[visor transport counts]───│
  │                        │                          │
  │ (Phase 2: SUDPH)       │                          │
  │ Connect to V1 (SUDPH)  │                          │
  │ Connect to V2 (SUDPH)  │                          │
  │ ...up to 30            │                          │
  │                        │                          │
  │ Sleep 5 minutes        │                          │
  │ Repeat...              │                          │
```

## Logging

The autoconnect module logs at various levels:

| Level | Message Pattern | Description |
|-------|-----------------|-------------|
| INFO | "Connecting to public visor" | Starting STCPR connection |
| INFO | "Established STCPR transport" | Successful connection |
| WARN | "Failed to connect" | Connection failure |
| DEBUG | "Skipping already connected" | Duplicate avoidance |
| DEBUG | "Fetched N public visors" | SD query result |

## Interaction with Other Modules

### Transport Manager

- Autoconnect calls `TransportManager.CreateTransport()` to establish connections
- Transport manager handles the low-level transport lifecycle

### Service Discovery Client

- Fetches list of available public visors
- Filters by service type (`visor`)

### Transport Discovery Client

- Queries per-key transport statistics for fallback selection
- Used when Service Discovery returns no public visors

## Design Considerations

### Load Distribution

- Public visors are shuffled before connection attempts
- Random initial delay prevents thundering herd on startup
- Connection limits prevent any single visor from being overwhelmed

### Network Resilience

- Multiple public visor connections provide redundancy
- SUDPH mesh connections reduce reliance on public visors
- Fallback to Transport Discovery when Service Discovery unavailable

### Small Network Behavior

On networks with fewer visors than the connection limits:

- Autoconnect connects to all available public visors (when < `maxPublicVisors`)
- SUDPH phase has fewer candidates than `maxSUDPH`
- Mesh formation completes within fewer cycles

### Large Network Behavior

On networks with many visors:

- Only `maxPublicVisors` (5) public visors are connected per cycle
- SUDPH limited to `maxSUDPH` (30) connections per cycle
- Public visors deregister at `max_transports` (1000), distributing load across multiple public visors
