// Package visor pkg/visor/system_feed_registrations.go c3-vis-core
//
// Registrations on the system feed. A host visor labels its local link to each
// service that runs in the transport discovery's process and takes visor
// registrations from its system feed subscription (transport.LabelSysFeed).
// For those the visor puts its registration leaves on the system feed (dmsg
// port 50, under the sysfeed prefixes) and runs no CXO feed of their own, so it
// holds one CXO connection to the deployment instead of one per service. Every
// other service keeps its own feed. See docs/design/cxo-one-feed-per-visor.md.
package visor

import (
	"context"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/sysfeed"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
)

// regLeafSink is where a registration publisher puts its leaves: its own CXO
// feed, or the system feed under the service's prefix.
type regLeafSink interface {
	Put(path string, value []byte) error
	Delete(path string) error
}

// prefixedSink writes a service's leaves onto the system feed.
type prefixedSink struct {
	pub    *treestore.Publisher
	prefix string
}

func (p prefixedSink) Put(path string, value []byte) error { return p.pub.Put(p.prefix+path, value) }
func (p prefixedSink) Delete(path string) error            { return p.pub.Delete(p.prefix + path) }

// systemFeedRoute is a service's registrations on the system feed.
type systemFeedRoute struct {
	sink regLeafSink
	// healthy is the keepalive predicate: the transport discovery holds a
	// subscription to the system feed and answers its announces.
	healthy func() (bool, uint64)
}

// systemFeedFor returns the route for service, whose PK the visor is
// configured with, when the deployment carries it on the system feed. It
// returns false, and the service keeps its own feed, unless the system feed
// runs and both the transport discovery and the service are the deployment's.
func (v *Visor) systemFeedFor(service string, servicePK cipher.PubKey, log *logging.Logger) (systemFeedRoute, bool) {
	v.cxoUserFeedsMu.Lock()
	pub := v.systemCXOPub
	v.cxoUserFeedsMu.Unlock()
	if pub == nil {
		return systemFeedRoute{}, false
	}
	tpdPK, ok := tpdCXOPeer(v)
	if !ok {
		return systemFeedRoute{}, false
	}
	if sysfeed.PrefixOf(service) == "" || !v.readsSystemFeed(tpdPK, servicePK) {
		return systemFeedRoute{}, false
	}
	v.noteSystemFeedService(service)
	return systemFeedRoute{
		sink:    prefixedSink{pub: pub, prefix: sysfeed.PrefixOf(service)},
		healthy: cxoKeepaliveHealthy(pub, tpdPK, &v.sysFeedAnnounceOK, log),
	}, true
}

// systemFeedTimeout bounds the TPD lookups that decide, at startup, whether a
// registration rides the system feed.
const systemFeedTimeout = 10 * time.Second

// readsSystemFeed reports whether the service with key servicePK takes its
// registrations off the system feed of the transport discovery tpdPK. Both
// must be hosted by one visor, and that host must label the link to the
// service LabelSysFeed, which it does while TPD in its process delivers to it.
// When TPD cannot say, the service keeps its own feed.
func (v *Visor) readsSystemFeed(tpdPK, servicePK cipher.PubKey) bool {
	ctx, cancel := context.WithTimeout(context.Background(), systemFeedTimeout)
	defer cancel()
	host, sysFeed, err := v.serviceFeedHost(ctx, servicePK)
	if err != nil || !sysFeed {
		return false
	}
	tpdHost, _, err := v.serviceFeedHost(ctx, tpdPK)
	return err == nil && tpdHost == host
}

// noteSystemFeedService records a service riding the system feed, for
// `visor state`.
func (v *Visor) noteSystemFeedService(service string) {
	v.cxoUserFeedsMu.Lock()
	defer v.cxoUserFeedsMu.Unlock()
	for _, s := range v.systemFeedServices {
		if s == service {
			return
		}
	}
	v.systemFeedServices = append(v.systemFeedServices, service)
}
