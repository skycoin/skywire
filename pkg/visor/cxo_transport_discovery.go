// Package visor pkg/visor/cxo_transport_discovery.go c3-vis-core
//
// CXO-aware wrapper around the visor's transport.DiscoveryClient. The
// only override is GetAllTransports — the network-wide snapshot used
// by calculateLocalRoutes, autoconnect, the hypervisor's calc tools,
// and a few RPC paths. Every other DiscoveryClient call (per-edge
// lookups, registrations, deletes) keeps going to HTTP because the
// CXO publisher doesn't carry that fan-out and a stale single-edge
// answer can produce route-setup failures that don't self-correct.
//
// The CXO snapshot is refreshed on the manager's 5min cycle while the
// feed is held; for read-heavy paths (mux-bw probes, hvui Transports
// tab, route calc on every Dial) that's tighter than the per-call HTTP
// fetch was. AcquireFor on each call keeps the cycle alive across
// bursts via cxoTabCloseGrace.
package visor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/deployment/tpd/tpdpaths"
	"github.com/skycoin/skywire/pkg/transport"
)

// cxoAwareTPD wraps a transport.DiscoveryClient, intercepting
// GetAllTransports to consult the visor's CXO subscription manager
// first and falling back to the embedded client (HTTP / DMSG-HTTP) on
// miss. The embed pattern preserves every other DiscoveryClient
// method unchanged — only the bulk-fetch path benefits from the
// cache.
type cxoAwareTPD struct {
	transport.DiscoveryClient
	v *Visor
}

// GetAllTransports returns the network-wide transport list. Reads the
// CXO snapshot of tpd-all-transports/without-self when the manager
// has a non-empty cache; otherwise delegates to the wrapped HTTP
// client. "without-self" matches the historical HTTP path's omission
// of self-transports from the bulk listing.
//
// AcquireFor + ReleaseFor on every call so a burst of route-calc
// dials inside the close-grace window reuses one live cycle instead
// of re-tearing-down between dials. JSON unmarshal of the snapshot
// is the same code the HTTP path runs; cost is negligible compared
// to a DMSG-HTTP round-trip.
func (c *cxoAwareTPD) getAllTransportsBase(ctx context.Context) ([]*transport.Entry, error) {
	if c.v != nil {
		if mgr := c.v.CXOSubMgr(); mgr != nil {
			// Keep the routing subscription up from the first route calculation
			// on: letting the ~10s-grace teardown drop it between dials forces a
			// fresh subscription — a new dmsg dial and handshake — on the next
			// one. It is pinned here, on first use, not when the manager is built:
			// autoconnect and dmsg lookups build the manager on every visor, and
			// pinning there made every visor hold the whole transport graph
			// whether it ever routed or not.
			c.v.routingPinOnce.Do(func() { mgr.Pin(FeedTPDRouting) })
			mgr.AcquireFor(TabCLITransports)
			defer mgr.ReleaseFor(TabCLITransports)
			if entries, ok := routingTransports(mgr); ok {
				return entries, nil
			}
		}
	}
	return c.DiscoveryClient.GetAllTransports(ctx)
}

// routingTransports assembles TPD's routing feed (one gzipped leaf per
// visor, see RoutingCXOPublisher in pkg/deployment/tpd/api) into the network's transport list:
// the transports that exist now, with the latency and throughput routes are
// weighed by. The publisher drops the derivable t_id; it is recomputed from
// (edges, type). ok is false until the feed has synced.
func routingTransports(mgr *CXOSubscriptionManager) ([]*transport.Entry, bool) {
	var entries []*transport.Entry
	mgr.Walk(FeedTPDRouting, tpdpaths.RoutingPathPrefix, func(_ string, body []byte) bool {
		var shard []*transport.Entry
		if err := json.Unmarshal(cxoutils.Gunzip(body), &shard); err != nil {
			return true
		}
		for _, e := range shard {
			if e == nil {
				continue
			}
			if e.ID == (uuid.UUID{}) {
				e.ID = transport.MakeTransportID(e.Edges[0], e.Edges[1], e.Type)
			}
			entries = append(entries, e)
		}
		return true
	})
	return entries, len(entries) > 0
}

// AllTransportsSyncedAt reports when the CXO routing feed last advanced, so the router's route-calc cache can invalidate on the
// CXO sync cadence instead of an independent wall-clock TTL. ok=false
// (feed not yet primed / CXO unavailable) tells the caller to keep its
// TTL fallback. Copies no body — a cheap version probe.
func (c *cxoAwareTPD) allTransportsSyncedAtCXO() (time.Time, bool) {
	if c.v == nil {
		return time.Time{}, false
	}
	mgr := c.v.CXOSubMgr()
	if mgr == nil {
		return time.Time{}, false
	}
	t := mgr.LastSync(FeedTPDRouting)
	return t, !t.IsZero()
}

// wrapDiscoveryClientWithCXO returns a CXO-aware wrapper around dc.
// Returns dc unchanged when v is nil (shouldn't happen in production
// but keeps the helper safe for tests that pass a bare DC).
func wrapDiscoveryClientWithCXO(dc transport.DiscoveryClient, v *Visor) transport.DiscoveryClient {
	if v == nil || dc == nil {
		return dc
	}
	return &cxoAwareTPD{DiscoveryClient: dc, v: v}
}

// GetAllTransports is the CXO-or-HTTP network view (getAllTransportsBase)
// plus the visor's local graph source, deduplicated by transport ID with the
// network view preferred. The merge is what lets route calculation and the
// router's local BFS see a hypervisor's attached visors without a TPD query.
func (c *cxoAwareTPD) GetAllTransports(ctx context.Context) ([]*transport.Entry, error) {
	entries, err := c.getAllTransportsBase(ctx)
	if err != nil {
		return entries, err
	}
	local, _ := c.v.localGraphEntries()
	return mergeTransportEntries(entries, local), nil
}

// AllTransportsSyncedAt is the CXO sync time with the local graph's
// version folded in: the later of the two, so a change on either side
// advances it. Only reported when the CXO side is primed — without that the
// TTL fallback must keep refetching the network view.
func (c *cxoAwareTPD) AllTransportsSyncedAt() (time.Time, bool) {
	ts, ok := c.allTransportsSyncedAtCXO()
	if !ok {
		return time.Time{}, false
	}
	_, localAt := c.v.localGraphEntries()
	return laterTime(ts, localAt), true
}
