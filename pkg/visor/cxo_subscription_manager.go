// Package visor pkg/visor/cxo_subscription_manager.go c3-vis-core
//
// Thin visor-side shim over the reusable pkg/cxo/cxosub cycle-based
// CXO sync manager. The implementation lives in cxosub, decoupled
// from *Visor via cxosub.Deps; this file only:
//
//   - re-exports the cxosub types/consts under their historical
//     visor names (CXOFeed, CXOTab, CXOSubscriptionManager, the
//     FeedXxx/TabXxx consts, FeedStatus) so existing callers compile
//     unchanged, and
//   - supplies the visor's dependency wiring: the live dmsg client
//     accessor and the feed → (peerPK, port, prefix) resolver that
//     reads service publisher PKs out of the visor config.
//
// The manager's cycle semantics are documented in the cxosub package.
package visor

import (
	"errors"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgcurl"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// Re-exported types so existing pkg/visor callers keep working.
type (
	// CXOFeed identifies one upstream CXO publisher feed.
	CXOFeed = cxosub.Feed
	// CXOTab identifies a consumer of one or more CXO feeds.
	CXOTab = cxosub.Tab
	// CXOSubscriptionManager is the visor's alias for cxosub.Manager.
	CXOSubscriptionManager = cxosub.Manager
	// FeedStatus is one row of CXOSubscriptionManager.Status.
	FeedStatus = cxosub.FeedStatus
)

// Re-exported feed constants.
const (
	FeedTPDMetrics           = cxosub.FeedTPDMetrics
	FeedTPDUptime            = cxosub.FeedTPDUptime
	FeedSDServices           = cxosub.FeedSDServices
	FeedDMSGDClientsByServer = cxosub.FeedDMSGDClientsByServer
	FeedTPDAllTransports     = cxosub.FeedTPDAllTransports
)

// Re-exported tab constants.
const (
	TabNetworkVisualizer = cxosub.TabNetworkVisualizer
	TabMetrics           = cxosub.TabMetrics
	TabUptime            = cxosub.TabUptime
	TabAutoconnect       = cxosub.TabAutoconnect
	TabCLIServices       = cxosub.TabCLIServices
	TabCLITransports     = cxosub.TabCLITransports
	TabRoutingPolicy     = cxosub.TabRoutingPolicy
)

// feedFirstSyncTimeout forwards to cxosub so the RPC fetch/refresh default
// timeout is per-feed (the all-transports feed needs longer than the rest).
var feedFirstSyncTimeout = cxosub.FeedFirstSyncTimeout

// CXOFeedString maps a feed constant to its stable string label. Kept as a
// visor-package alias for existing callers (helpers_test.go round-trips it).
var CXOFeedString = cxosub.FeedString

// CXOFeedFromString is the inverse of the feed string label. Kept as a
// visor-package alias for existing callers.
var CXOFeedFromString = cxosub.FeedFromString

// NewCXOSubscriptionManager constructs a manager wired to the visor:
// the live dmsg client accessor and the visor's feed resolver. interval
// comes from hypervisor.cxo_subscribe_interval; <= 0 falls back to the
// cxosub default. log nil falls back to a default package logger.
func NewCXOSubscriptionManager(v *Visor, interval time.Duration, log *logging.Logger) *CXOSubscriptionManager {
	return cxosub.NewManager(cxosub.Deps{
		Dmsg:     func() *dmsg.Client { return v.dmsgC },
		FeedSpec: v.cxoFeedSpec,
		Log:      log,
	}, interval)
}

// CXOSubMgr returns the visor's manager, constructing it lazily on
// first use. Returns nil when the visor has no DMSG client (early
// startup, or DMSG disabled). Callers treat nil as "no manager;
// fall through to whatever non-CXO source you'd otherwise use" —
// every method on a nil receiver is a safe no-op.
func (v *Visor) CXOSubMgr() *CXOSubscriptionManager {
	v.initLock.Lock()
	defer v.initLock.Unlock()
	if v.cxoSubMgr != nil {
		return v.cxoSubMgr
	}
	if v.dmsgC == nil {
		return nil
	}
	var interval time.Duration // 0 => cxosub default
	if v.conf != nil && v.conf.Hypervisor != nil {
		if d := time.Duration(v.conf.Hypervisor.CXOSubscribeInterval); d > 0 {
			interval = d
		}
	}
	v.cxoSubMgr = NewCXOSubscriptionManager(v, interval, v.MasterLogger().PackageLogger("cxo_subscription_manager"))
	return v.cxoSubMgr
}

// cxoFeedSpec returns the (peerPK, dmsgPort, pathPrefix) tuple for a
// feed. Returns an error if the visor doesn't have the peer's PK
// configured. This is the FeedSpec resolver injected into cxosub.
func (v *Visor) cxoFeedSpec(fk cxosub.Feed) (cipher.PubKey, uint16, string, error) {
	switch fk {
	case FeedTPDMetrics:
		pk, ok := tpdCXOPeer(v)
		if !ok {
			return cipher.PubKey{}, 0, "", errors.New("no TPD CXO peer (transport.discovery_dmsg unset)")
		}
		return pk, skyenv.DmsgTPDMetricsCXOPort, "metrics/days/", nil
	case FeedTPDUptime:
		pk, ok := tpdCXOPeer(v)
		if !ok {
			return cipher.PubKey{}, 0, "", errors.New("no TPD CXO peer (transport.discovery_dmsg unset)")
		}
		return pk, skyenv.DmsgTPDUptimeCXOPort, "uptimes/days/", nil
	case FeedSDServices:
		pk, ok := sdCXOPeer(v)
		if !ok {
			return cipher.PubKey{}, 0, "", errors.New("no SD CXO peer (launcher.service_discovery_dmsg unset)")
		}
		return pk, skyenv.DmsgSDServicesCXOPort, "services/", nil
	case FeedDMSGDClientsByServer:
		pk, ok := dmsgdCXOPeer(v)
		if !ok {
			return cipher.PubKey{}, 0, "", errors.New("no DMSG-D CXO peer (dmsg.discovery_dmsg unset)")
		}
		return pk, skyenv.DmsgDMSGDClientsByServerCXOPort, "clients-by-server/", nil
	case FeedTPDAllTransports:
		pk, ok := tpdCXOPeer(v)
		if !ok {
			return cipher.PubKey{}, 0, "", errors.New("no TPD CXO peer (transport.discovery_dmsg unset)")
		}
		return pk, skyenv.DmsgTPDAllTransportsCXOPort, "transports/all/", nil
	}
	return cipher.PubKey{}, 0, "", fmt.Errorf("unknown feed: %d", fk)
}

// sdCXOPeer extracts the SD publisher PK from the visor's
// launcher.service_discovery_dmsg URL.
func sdCXOPeer(v *Visor) (cipher.PubKey, bool) {
	return parseDmsgPeer(v.conf.Launcher.ServiceDiscDmsg)
}

// dmsgdCXOPeer extracts the DMSG-D publisher PK from the visor's
// dmsg.discovery_dmsg URL.
func dmsgdCXOPeer(v *Visor) (cipher.PubKey, bool) {
	if v.conf.Dmsg == nil {
		return cipher.PubKey{}, false
	}
	return parseDmsgPeer(v.conf.Dmsg.DiscoveryDmsg)
}

func parseDmsgPeer(raw string) (cipher.PubKey, bool) {
	if raw == "" {
		return cipher.PubKey{}, false
	}
	var u dmsgcurl.URL
	if err := u.Fill(raw); err != nil {
		return cipher.PubKey{}, false
	}
	if u.Scheme != "dmsg" {
		return cipher.PubKey{}, false
	}
	return u.Addr.PK, true
}
