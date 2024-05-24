# Routing Table

A *Routing Table* (located within the `skywire/pkg/node` module) is unique for a given Node's public key. It is basically a key-value store in which the key is the *Route ID* and the value is the *Routing Rule* for the given *Route ID*.

Initially, there will be two types of Routing Rules: *App* and *Forward*.

- *App* rules are identified by their unique `<r-type>` value of `0x00`. A packet which contains a *Route ID* that associates with a *App* rule is to be sent to a local App.
- *Forward* rules are identified by their unique `<r-type>` value of `0x01`. A packet which contains a *Route ID* that associates with a *Forward* rule is to be forwarded.

| Action | Key (Route ID) | Value (Routing Rule) |
| ------ | -------------- | -------------------- |
| *App* | `<rid>`<br>*4 bytes* | `<expiry><r-type><resp-rid><loop-data>`<br>*48 bytes* |
| *Forward* | `<rid>`<br>*4 bytes* | `<expiry><r-type><next-rid><next-tid>`<br>*29 bytes* |

- `<rid>` is the *Route ID* `uint32` key (represented by 4 bytes) that is used to obtain the routing rules for the Packet.
- `<expiry>` contains the epoch time (8 bytes) of when the rule is to be discarded (or becomes invalid).
- `<r-type>` specifies the type of Routing Rule (1 byte). Currently there are two possible routing rule types; *App* (`0x00`) and *Forward* (`0x01`).
- `<resp-rid>` is the *Route ID* (4 bytes) that is the Route ID key for the reserve Route of the loop.
- `<loop-data>` identifies and classifies the loop. It contains the following sub-fields; `<remote-pk><remote-port><local-port>`.
    - `<remote-pk>` is the remote edge public key in which this route/loop is associated with. It is represented by 33 bytes.
    - `<remote-port>` is the remote port in which this route/loop is associated with. It is represented by 2 bytes.
    - `<local-port>` is the local port in which this route/loop is associated with. It is represented by 2 bytes.
- `<next-rid>` is the *Route ID* that is to replace the `<rid>` before the Packet is to be forwarded.
- `<next-tid>` represents the transport which the packet is to be forwarded to. A Transport ID is 16 bytes long.

Every time a Skywire Node receives a packet, it performs the following steps:

1. Obtain the `<rid>` from the Packet, and uses this value to obtain a routing rule entry from the routing table. If no routing rule is found, or the routing rule has already expired (via checking the `<expiry>` field), the Packet is then discarded.
2. Obtains the `<r-type>` value to determine how the packet is to be dealt with. If the `<r-type>` value is `0x00`, the packet is then to be sent to the local *App Server* with the Routing Rule. If the `<r-type>` value is `0x01`, the packet is to be forwarded; continue on to step 3.
3. Obtain the `<next-rid>` from the *Routing Rule* and replace the `<rid>` from the *Route ID* field of the Packet.
4. Forward the Packet to the referenced transport specified within `<next-tid>`.

The routing table is to be an interface.

```golang
package node

// RangeFunc is used by RangeRules to iterate over rules.
type RangeFunc func(routeID transport.RouteID, rule RoutingRule) (next bool)

// RoutingTable represents a routing table implementation.
type RoutingTable interface {
	// AddRule adds a new RoutingRules to the table and returns assigned RouteID.
	AddRule(rule RoutingRule) (routeID transport.RouteID, err error)

	// SetRule sets RoutingRule for a given RouteID.
	SetRule(routeID transport.RouteID, rule RoutingRule) error

	// Rule returns RoutingRule with a given RouteID.
	Rule(routeID transport.RouteID) (RoutingRule, error)

	// DeleteRules removes RoutingRules with a given a RouteIDs.
	DeleteRules(routeIDs ...transport.RouteID) error

	// RangeRules iterates over all rules and yields values to the rangeFunc until `next` is false.
	RangeRules(rangeFunc RangeFunc) error

	// Count returns the number of RoutingRule entries stored.
	Count() int
}
```

Potential improvement we could consider is to move ports from the rules into the data packet header, aligning this with `tcp`. By doing so we will be able to re-use intermediate forward rules across multiple loops which can drastically improve loop establishment time for complex loops.
