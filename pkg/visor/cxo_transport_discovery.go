// Package visor pkg/visor/cxo_transport_discovery.go
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

	"github.com/skycoin/skywire/pkg/transport"
	tpdapi "github.com/skycoin/skywire/pkg/transport-discovery/api"
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
func (c *cxoAwareTPD) GetAllTransports(ctx context.Context) ([]*transport.Entry, error) {
	if c.v != nil {
		if mgr := c.v.CXOSubMgr(); mgr != nil {
			mgr.AcquireFor(TabCLITransports)
			defer mgr.ReleaseFor(TabCLITransports)
			body, _, ok := mgr.Get(FeedTPDAllTransports, tpdapi.AllTransportsPathWithoutSelf)
			if ok && len(body) > 0 {
				var entries []*transport.Entry
				if err := json.Unmarshal(body, &entries); err == nil {
					return entries, nil
				}
				// Unmarshal failure: most likely a schema drift between
				// publisher and subscriber binaries — let HTTP serve so
				// the caller still gets a usable answer.
			}
		}
	}
	return c.DiscoveryClient.GetAllTransports(ctx)
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
