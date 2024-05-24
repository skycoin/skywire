# `router2`

The goal of this proposal is to split out and define the responsiblities within `router` to allow for easier integration of future features, as well as make the codebase easier to understand.

The current implementation of `routing.Rule` has fields that have different meanings, based on the rule type. A clearer definition of rule types are addressed in this proposal.

Currently, setup nodes create *loops* (which is the combination of a forward and a reverse route). We want setup nodes to create *routes* (which is a uni-directional line of communication across x-number of hops). A single `CreateRoute` packet can request to create multiple routes. How routes are to be used is to be handled in an additional structure called `multiplexer`.

## `routing.Rule` changes

As the `Router` is to be split up into router/multiplexer, we are to remove the concept of `App` rule types, and work with the concepts of forward/consume.

### New fields for `routing.Rule`

A routing rule is structured as follows (note that the `body` field is action-specific).
```
| keepAlive | rType  | keyRtID | body   |
| 8[0:8]    | 1[8:9] | 4[9:13] | [13:~] |
```

#### field `keepAlive` (8 bytes)

`keepAlive` is the duration of non-use before the rule is to be disregarded/deleted. It is the responsibility of the src edge to send ping packets and the dst edge to respond with pong packets.

#### field `rType` (1 byte)

`rType` is the `RuleType`.

```golang
type RuleType byte    `

const (
    // ConsumeRule represents a hop to the route's destination node.
    // A packet referencing this rule is to be consumed localy.
    ConsumeRule = RuleType(0)

    // ForwardRule represents a hop from the route's source node.
    // A packet referencing this rule is to be sent to a remote node.
    ForwardRule = RuleType(1)

    // IntermediaryForwardRule represents a hop which is not from the route's source,
    // nor to the route's destination.
    IntermediaryForwardRule = RuleType(2)
)
```

#### field `keyRtID` (4 bytes)

`keyRtID` is the route ID that is used as the key to retreive the rule.

#### field `body` (~ bytes)

The contents and size of this field is dependent on the `rType`.

- `ConsumeRule` `body` field contents:
    ```
    | rDesc     |
    | 70[13:83] |
    ```

- `ForwardRule` `body` field contents:
    ```
    | rDesc     | nxtRtID  | nxtTpID    |
    | 70[13:83] | 4[83:87] | 16[87:103] |
    ```
- `IntermediaryForwardRule` `body` field contents:
    ```
    | nxtRtID  | nxtTpID   |
    | 4[13:17] | 16[17:33] |
    ```

#### field `rDesc` (70 bytes)

```golang
// RouteDescriptor describes a route (from the perspective of the source and destination edges).
type RouteDescriptor [70]byte

func (d RouteDescriptor) SrcPK() cipher.PubKey { /*TODO*/ }
func (d RouteDescriptor) DstPK() cipher.PubKey { /*TODO*/ }
func (d RouteDescriptor) SrcPort() uint16 { /*TODO*/ }
func (d RouteDescriptor) DstPort() uint16 { /*TODO*/ }
```

The decision to go with src/dst instead of local/remote is because it will have the same values independent of the residing edge.

#### field `nxtRtID` (4 bytes)

Provides the next `routeID` value for `Forward`-esk type rules.

#### field `nxtTpID` (16 bytes)

Provides the next transport ID for `Forward`-esk type rules. 

## `routing.Table` changes

The nature of rules is going to be temporary and there will no longer be any need to store rules to disk. The only implementation for `routing.Table` should be in memory.

The new structure of `routing.Table` should have an internal garbage collector, removing the need for `router.managedRoutingTable`. The internal garbage collector should only delete `IntermediaryForwardRule` types. All other rule types should be deleted by external structures.

```golang
type Table interface {
    ReserveKey() (key RouteID, err error)
    SaveRule(r Rule) error
    Rule(key RouteID) (r Rule, err error)
    RulesWithDesc(desc RouteDescriptor) (rs []Rule, err error)
    AllRules() []Rule
    DelRules([]RouteID)
    Count() int
}

type memTable struct {
    // All rules, referenced with 'keyRtID'.
    rules map[RouteID]Rule
}
```

## Setup node changes

### Packet types

We should only have the following packet types:

| Packet Type | Packet Body |
| --- | --- |
| `CreateRoutes` | `routes []Route` |
| `RoutesCreated` | `routes []Route` |
| `ReserveRtIDs` | `n int` |
| `RtIDsReserved` | `rtIDs []routing.RouteID` |
| `AddRules` | `rules []routing.Rule` |
| `RulesAdded` | `rtIDs []routing.RouteID` |
| `Failure` | `failureCode byte, msg string` |

Notes:
- We are removing `Close~` packet types as it's no longer needed. Instead, we will use shorter `keepAlive` durations and introduce ping/pong to keep routes alive.

### `Route`

```golang
type Hop struct {
    TpID uuid.UUID
    From cipher.PubKey
	To   cipher.PubKey
}

type Route struct {
    Desc      RouteDescriptor
    Hops      []Hop
    KeepAlive time.Duration
}
```

### Use one connection per visor, per request

Previously, a single request is split between multiple connections per visor. We should only use one connection per request per visor.

### Failure codes

We need to define a new type.

```golang
type FailureCode byte

func (fc *FailureCode) String() string {
    // TODO: define.
}
```

And have a list of constants for failure codes.

## Route Finder Changes

Instead of finding *paired routes*, we should be able to search for routes independently.

```golang
type RouteOptions struct {
    MinHops int
    MaxHops int
}

// FindRoutes find routes specified in 'rts'.
// As routes are uni-directional, rts[i][0] is the source edge, and rts[i][1] is the destination edge.
// If 'opts' is nil, MinHops is 1 and MaxHops is 3.
func (c *apiClient) FindRoutes(ctx context.Context, rts [][2]cipher.PubKey, opts *RouteOptions)
```

Therefore the route finder API also needs modifications.

## Router Changes

The `router` is responsible for creating and keeping track of these uni-directional routes. Internally, it uses the *routing table*, *route finder client* and *setup client*.

```golang
// DialOptions are options when dialing routes.
type DialOptions struct {
    MinForwardRts int
    MaxForwardRts int
    MinConsumeRts int
    MaxConsumeRts int
}

type Router interface {
    io.Closer

    // DialRoutes dials to a given visor of 'rPK'.
    // 'lPort'/'rPort' specifies the local/remote ports respectively.
    // A nil 'opts' input results in a value of '1' for all DialOptions fields.
    // A single call to DialRoutes should perform the following:
    // - Find routes via RouteFinder (in one call).
    // - Setup routes via SetupNode (in one call).
    // - Save to routing.Table and internal RouteGroup map.
    // - Return RouteGroup if successful.
    DialRoutes(ctx context.Context, rPK cipher.PubKey, lPort, rPort uint16, opts *DialOptions) (*RouteGroup, error)

    // AcceptsRoutes should block until we recieve an AddRules packet from SetupNode that contains ConsumeRule(s) or ForwardRule(s). Then the following should happen:
    // - Save to routing.Table and internal RouteGroup map.
    // - Return the RoutingGroup.
    AcceptRoutes() (*RouteGroup, error)
}

type router {
    rt  RoutingTable                    // routing table
    tm  transport.Manager               // transport manager
    rfc rfclient.Client                 // route finder client 
    rsc rsclient.Client                 // route setup client
    rgs map[RouteDescriptor]*RouteGroup // route groups to push incoming reads from transports.
}
```

### `RouteGroup` structure

A `RouteGroup` is responsible for input/output via rules. In the future, we may read/write via multiple transports, hence we need a way of reordering the packets.

A `RouteGroup` is created either when we:
- Initiate a set a routes.
- Receive a set of routes.

```golang
// RouteGroup should implement 'io.ReadWriteCloser'.
type RouteGroup struct {
    desc RouteDescriptor // describes the route group
    fwd []Rule           // forward rules (for writing)
    rvs []Rule           // reverse rules (for reading)

    // The following fields are used for writing:
    // - fwd/tps should have the same number of elements.
    // - the corresponding element of tps should have tpID of the corresponding rule in fwd.
    // - rg.fwd references 'ForwardRule' rules for writes.

    // 'tps' is transports used for writing/forward rules.
    // It should have the same number of elements as 'fwd'
    // where each element corresponds with the adjacent element in 'fwd'.
    tps []*transport.ManagedTransport

    // 'readCh' reads in incoming packets of this route group.
    // - Router should serve call '(*transport.Manager).ReadPacket' in a loop,
    //      and push to the appropriate '(RouteGroup).readCh'.
    readCh  <-chan []byte // push reads from Router
    readBuf bytes.Buffer  // for read overflow
}
```

#### Routing packets

The unit of communication for routing/router is called packets.

```
| type (byte) | route ID (uint32) | payload size (uint16) | payload (~) |
| 1[0:1]      | 4[1:5]            | 2[5:7]                | [7:~]       |
```

packet types:
- `DataPacket` - Payload is just the underlying data.
- `ClosePacket` - Payload is a `type CloseCode byte`.
- `KeepAlivePacket` - Payload is empty.

#### Reading mechanism of `RouteGroup`

The `Router`, via `transport.Manager`, is responsible for reading incoming packets and pushing it to the appropriate `RouteGroup` via `(*RouteGroup).readCh`.

To help with implementing the read logic, within the `dmsg` repo, we have [`ioutil.BufRead`](https://github.com/skycoin/dmsg/blob/master/ioutil/buf_read.go), just in case the read buffer is short.

#### Writing mechanism of `RouteGroup`

For the first version, only the first `ForwardRule` (`fwd[0]`) is used for writing. 

#### Closing the `RouteGroup`

- Send `Close` packet for all `ForwardRule`s.
- Delete all rules (`ForwardRule`s and `ConsumeRule`s) from routing table.
- Close all go channels.

#### KeepAlive mechanism

The keepAlive value for routing rules is rather short to ensure that rules are deleted when unused. It is the responsibility for the source edge of each route (or the node with the `ForwardRule`) to send keepAlive packets.

The `RouteGroup` is responsible for ensuring that at least one packet is sent every `keepAlive/2`. This can be a data packet or a keepAlive packet. If data packets are delivered with a high frequency, we will not need to send keepAlive packets.