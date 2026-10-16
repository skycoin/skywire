// Package visor pkg/visor/system_feed_registrations.go c3-vis-core
//
// Registrations on the system feed. The deployment's services config lists in
// feed_ingest the services that run in the transport discovery's process and
// take visor registrations from its system feed subscription. For those the
// visor puts its registration leaves on the system feed (dmsg port 50, under
// the sysfeed prefixes) and runs no CXO feed of their own, so it holds one CXO
// connection to the deployment instead of one per service. Every other service
// keeps its own feed. See docs/design/cxo-one-feed-per-visor.md.
package visor

import (
	"path/filepath"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/sysfeed"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
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
	if !systemFeedCarries(v.deploymentServicesDoc(log), service, tpdPK, servicePK) {
		return systemFeedRoute{}, false
	}
	v.noteSystemFeedService(service)
	return systemFeedRoute{
		sink:    prefixedSink{pub: pub, prefix: sysfeed.PrefixOf(service)},
		healthy: cxoKeepaliveHealthy(pub, tpdPK, &v.sysFeedAnnounceOK, log),
	}, true
}

// systemFeedCarries reports whether doc has service ride the system feed for a
// visor configured with tpdPK and servicePK. The configured keys must be the
// deployment's own: a visor pointed at another instance of either keeps the
// service's own feed, since that instance is not in the deployment's process.
func systemFeedCarries(doc *visorconfig.Services, service string, tpdPK, servicePK cipher.PubKey) bool {
	if doc == nil || !doc.RidesSystemFeed(service) || sysfeed.PrefixOf(service) == "" {
		return false
	}
	if pk, ok := firstDmsgPeer(doc.TransportDiscoveryDmsg, doc.TransportDiscovery); !ok || pk != tpdPK {
		return false
	}
	var dmsgURL, url string
	switch service {
	case sysfeed.DmsgDiscovery:
		dmsgURL, url = doc.DmsgDiscoveryDmsg, doc.DmsgDiscovery
	case sysfeed.AddressResolver:
		dmsgURL, url = doc.AddressResolverDmsg, doc.AddressResolver
	case sysfeed.ServiceDiscovery:
		dmsgURL, url = doc.ServiceDiscoveryDmsg, doc.ServiceDiscovery
	}
	pk, ok := firstDmsgPeer(dmsgURL, url)
	return ok && pk == servicePK
}

func firstDmsgPeer(raws ...string) (cipher.PubKey, bool) {
	for _, raw := range raws {
		if pk, ok := parseDmsgPeer(raw); ok {
			return pk, true
		}
	}
	return cipher.PubKey{}, false
}

// deploymentServicesDoc is the deployment's services config as last applied,
// else as saved by the previous run, else the embedded default. Read at
// startup, so a change to feed_ingest takes effect when the visor restarts.
func (v *Visor) deploymentServicesDoc(log *logging.Logger) *visorconfig.Services {
	v.deploySvcMu.Lock()
	defer v.deploySvcMu.Unlock()
	if v.deploySvcLast != nil {
		return v.deploySvcLast
	}
	if v.conf.LocalPath != "" {
		if doc := loadDeploymentServices(filepath.Join(v.conf.LocalPath, deploymentServicesFile), log); doc != nil {
			doc.BackfillClearnetFromDmsg()
			return doc
		}
	}
	doc := deployment.Prod
	return &doc
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
