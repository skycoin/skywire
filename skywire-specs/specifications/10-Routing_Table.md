# Routing Table

The *Routing Table* (`pkg/routing/table.go`) is a per-visor key-value store mapping *Route IDs* to *Routing Rules*. It is used by the router to determine how incoming packets should be handled.

## Route IDs

A *Route ID* is a `uint32` value that uniquely identifies a routing rule within a single visor's table. Route IDs are allocated by the visor itself when the Route Setup Node calls `ReserveIDs`. Route ID 0 is reserved (invalid).

## Routing Rules

A routing rule is a variable-length byte slice with a fixed header:

| Field | Size | Description |
|---|---|---|
| KeepAlive | 8 bytes | Duration before the rule expires (nanoseconds) |
| RuleType | 1 byte | 0=Consume, 1=Forward, 2=IntermediaryForward |
| KeyRouteID | 4 bytes | The route ID this rule is associated with |

### Consume Rule

Delivers the packet to a local application. Additional fields:

| Field | Size | Description |
|---|---|---|
| SrcPK | 33 bytes | Source visor public key |
| DstPK | 33 bytes | Destination visor public key |
| SrcPort | 2 bytes | Source routing port |
| DstPort | 2 bytes | Destination routing port |

### Forward Rule

Forwards the packet to the next hop. Additional fields:

| Field | Size | Description |
|---|---|---|
| NextRouteID | 4 bytes | Route ID on the next hop |
| NextTransportID | 16 bytes | UUID of the transport to use |
| SrcPK | 33 bytes | Source visor public key |
| DstPK | 33 bytes | Destination visor public key |
| SrcPort | 2 bytes | Source routing port |
| DstPort | 2 bytes | Destination routing port |

### IntermediaryForward Rule

Forwards the packet through an intermediary visor. Additional fields:

| Field | Size | Description |
|---|---|---|
| NextRouteID | 4 bytes | Route ID on the next hop |
| NextTransportID | 16 bytes | UUID of the transport to use |

Intermediary rules do not contain source/destination PKs or ports — the intermediary visor only knows the previous and next hop.

## Rule Expiry

Each rule has a `KeepAlive` duration (default 24 hours). The routing table periodically sweeps for expired rules and removes them. Route groups send periodic `KeepAlivePacket` frames to refresh the rules at each hop.

## Rule Operations

| Operation | Description |
|---|---|
| `ReserveIDs(n)` | Allocates n unused route IDs (called by RSN during route setup) |
| `SaveRule(rule)` | Stores a routing rule at its key route ID |
| `Rule(id)` | Retrieves the rule for a given route ID |
| `DelRules(ids)` | Removes rules by route ID |
| `AllRules()` | Returns all rules (for debugging / status display) |
| `Count()` | Returns the number of active rules |
