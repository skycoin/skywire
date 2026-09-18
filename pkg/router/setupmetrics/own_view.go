// Package setupmetrics pkg/router/setupmetrics/own_view.go c2-net-routing
//
// The caller-scoped view of the route-setup snapshot.
//
// /stats splits in two: the aggregate half is public (anyone depends on this
// shared layer and deserves to see whether it works), the topology half —
// top_destinations, top_failed_destinations, recent_failures — is gated on the
// survey whitelist, because the setup node participates in EVERY route setup
// and its record of who sets up routes to whom is traffic-analysis material.
//
// The gap that left: an ordinary visor asking its own setup node "which
// destination is failing FOR ME" got `"top_destinations": null` and
// `"recent_failures": null`, so `pk`, `src_pk` and `dst_pk` were empty for
// every operator not on the whitelist — which is every operator, since the
// whitelist holds seven deployment keys. The structured fields exist and are
// filled in correctly; they were simply never sent.
//
// A caller does not need privilege to be told about its OWN route setups: it
// already knows which destinations it dialed and which of them failed. So the
// non-whitelisted view is no longer empty — it is the caller's own rows, and
// nobody else's. The whitelisted view is unchanged (every row, every key).
package setupmetrics

import (
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
)

// pairKey is the map key for per-(source, destination) counters. The separator
// cannot appear in a hex public key, so the two halves are recoverable.
const pairKeySep = "|"

func makePairKey(src, dst string) string { return src + pairKeySep + dst }

func splitPairKey(k string) (src, dst string, ok bool) {
	i := strings.IndexByte(k, pairKeySep[0])
	if i < 0 {
		return "", "", false
	}
	return k[:i], k[i+1:], true
}

// touchPair returns (creating if necessary) the per-(src,dst) counter. Shares
// the destination capacity: a setup node that has already filled its
// destination table stops tracking new pairs rather than growing without bound.
// Returns nil when the key is new and the table is full.
func (c *Collector) touchPair(src, dst string) *DestStat {
	if src == "" || dst == "" {
		return nil
	}
	if c.pairs == nil {
		c.pairs = make(map[string]*DestStat, 16)
	}
	k := makePairKey(src, dst)
	if d, ok := c.pairs[k]; ok {
		return d
	}
	if len(c.pairs) >= c.destCapacity {
		return nil
	}
	d := &DestStat{PK: dst}
	c.pairs[k] = d
	return d
}

// SnapshotFor returns the snapshot a specific caller may see. A whitelisted
// caller gets the full snapshot; everyone else gets the aggregate plus their
// OWN rows — the destinations they themselves set up routes to, and their own
// failures, error strings included (the keys in those strings are the caller's
// own peers).
//
// A null caller PK — an unparseable RemoteAddr — gets the aggregate alone.
//
// What a non-whitelisted caller still never sees is the who-talks-to-whom half
// for anyone else: the other visors this node set up routes for, the
// destinations they used, and the FailureEvent.Error strings that embed the
// same keys in prose. The RSN participates in EVERY route setup, so its view of
// which visors route to which is unusually complete, and publishing that is
// traffic-analysis material a routing overlay must not hand out.
func (c *Collector) SnapshotFor(caller cipher.PubKey, whitelisted bool) StatsSnapshot {
	snap := c.Snapshot()
	if whitelisted {
		return snap
	}
	// Everyone else: start from the aggregate with the topology removed, then
	// add back the caller's own rows.
	snap.TopDestinations = nil
	snap.TopFailedDestinations = nil
	snap.RecentFailures = nil
	snap.Breakers = nil
	if caller.Null() {
		return snap
	}
	self := caller.Hex()

	c.mu.Lock()
	defer c.mu.Unlock()
	byTotal := make([]DestStat, 0, 8)
	byFailed := make([]DestStat, 0, 8)
	for k, d := range c.pairs {
		src, _, ok := splitPairKey(k)
		if !ok || src != self {
			continue
		}
		row := *d
		// The breaker is per DESTINATION, not per pair, and it is what tells
		// the caller "route setup to this peer is currently being refused" —
		// the single most useful field here.
		if b, ok := c.breakers[row.PK]; ok {
			row.Circuit = string(b.state)
		}
		byTotal = append(byTotal, row)
		if row.Failed > 0 {
			byFailed = append(byFailed, row)
		}
	}
	snap.TopDestinations = sortDestStats(byTotal, false, 10)
	snap.TopFailedDestinations = sortDestStats(byFailed, true, 10)

	for _, ev := range c.recentFailuresLocked() {
		if ev.SrcPK != self {
			continue
		}
		snap.RecentFailures = append(snap.RecentFailures, ev)
	}
	return snap
}
