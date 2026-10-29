// Package visor pkg/visor/cxo_announce.go c3-vis-core
//
// The announce loop and keepalive predicate shared by the visor's
// registration feeds: dmsg-discovery registration, service-discovery
// registration and address resolver binds.
package visor

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
)

// cxoAnnounceInterval is how often the visor pokes a registration feed's conn
// to the consuming service. Short enough that a recovered visor or service
// re-links within cxoKeepaliveHealthyWindow.
const cxoAnnounceInterval = 30 * time.Second

// cxoKeepaliveHealthyWindow is how long after the last successful announce the
// registration-over-CXO keepalive is still considered healthy. ~3 announce
// intervals of slack, so one missed announce doesn't flap the HTTP-keepalive
// fallback, while a genuinely dropped feed conn falls back within a minute or
// two.
const cxoKeepaliveHealthyWindow = 95 * time.Second

// cxoKeepaliveHealthy is the predicate every registration client (dmsg entry,
// SD services, AR binds) consults before a no-change HTTP re-registration.
// The service keeps this feed's entries alive when it answered an announce
// within cxoKeepaliveHealthyWindow AND holds a live subscription to the feed,
// so the publisher's heartbeat Roots reach its ingest, which refreshes the
// stored entries' TTL. A reachable service that never subscribed is not.
//
// The epoch counts the conns that subscription has arrived on. A new one means
// the service reconnected, possibly after a restart that lost its store, and
// the clients re-register once over HTTP: that path can create entries the
// CXO ingest cannot (a type=visor SD entry needs the observed IP).
func cxoKeepaliveHealthy(pub *treestore.Publisher, peer cipher.PubKey, lastOK *atomic.Int64, log *logging.Logger) func() (bool, uint64) {
	var (
		mu    sync.Mutex
		conn  *node.Conn
		epoch uint64
	)
	return func() (bool, uint64) {
		mu.Lock()
		defer mu.Unlock()
		last := lastOK.Load()
		if last == 0 || time.Since(time.Unix(0, last)) >= cxoKeepaliveHealthyWindow {
			return false, epoch
		}
		subs := pub.Subscriptions(peer)
		if len(subs) == 0 {
			return false, epoch
		}
		// Stay on the current conn while it is still subscribed: the peer
		// can hold two (announce-dialed and self-dialed), listed in no
		// particular order.
		if slices.Contains(subs, conn) {
			return true, epoch
		}
		conn = subs[0]
		epoch++
		log.WithField("peer", peer).WithField("epoch", epoch).WithField("conns", len(subs)).
			Debug("CXO keepalive: subscription on a new conn; next registration goes over HTTP")
		return true, epoch
	}
}

// announceFeed announces pub to peer on a ticker until ctx ends.
// AnnounceTo is idempotent: a live conn is a no-op and a dropped one redials.
// Each success stamps lastOK, which drives cxoKeepaliveHealthy.
func announceFeed(ctx context.Context, pub *treestore.Publisher, peer cipher.PubKey, lastOK *atomic.Int64, label string, log *logging.Logger) {
	t := time.NewTicker(cxoAnnounceInterval)
	defer t.Stop()
	announce := func() {
		dctx, cancel := context.WithTimeout(ctx, cxoAnnounceInterval/2)
		defer cancel()
		if err := pub.AnnounceTo(dctx, peer); err != nil {
			log.WithError(err).WithField("peer", peer).Tracef("%s: announce failed", label)
			return
		}
		lastOK.Store(time.Now().UnixNano())
	}
	announce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			announce()
		}
	}
}
