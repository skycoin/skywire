// Package visor pkg/visor/cxo_user_feeds.go
//
// CXO user-feed registry. Each visor publishes one always-on system
// feed (the telemetry feed wired up by initStats on
// skyenv.DmsgCXOPort) and any number of user feeds — distinct CXO
// TreeStore publishers running on distinct DMSG ports under the
// visor's PK. Subscribers discover them via the dmsghttp logserver's
// /feeds endpoint, then connect their CXO subscriber to
// (visor PK, feed.DmsgPort) directly.
//
// The system feed is reserved at skyenv.DmsgCXOPort. User feeds must
// pick a different port; collisions with system DMSG ports listed in
// pkg/skyenv (hypervisor=46, transport-setup=47, http=80, dht=100,
// etc.) are rejected at registration so a user feed can't shadow a
// reserved listener.
package visor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/logserver"
)

// systemCXOFeedName is the well-known name of the visor's telemetry
// feed (the one initStats wires up on skyenv.DmsgCXOPort). Reserved
// so user-registered feeds can't collide with it.
const systemCXOFeedName = "stats"

// cxoUserFeed wraps a treestore.Publisher with the metadata needed for
// /feeds discovery and clean teardown.
type cxoUserFeed struct {
	name        string
	port        uint16
	description string
	pub         *treestore.Publisher
}

// reservedDmsgPorts enumerates DMSG ports already bound by visor
// subsystems. Registering a CXO feed on one of these would either
// silently fail to listen or shadow the existing service, so we reject
// the request up front.
//
// Kept in code rather than discovered at runtime because the listeners
// come up after init in a way that races RegisterCXOFeed; a static
// table is simpler and the cost of staying in sync with skyenv is one
// line per new system port.
func reservedDmsgPorts() map[uint16]string {
	return map[uint16]string{
		skyenv.DmsgCtrlPort:                  "dmsgctrl",
		skyenv.DmsgPingPort:                  "dmsg-ping",
		skyenv.DmsgPtyPort:                   "dmsgpty",
		skyenv.DmsgSetupPort:                 "setup-node",
		skyenv.DmsgHypervisorPort:            "hypervisor-rpc",
		skyenv.DmsgTransportSetupPort:        "transport-setup",
		skyenv.DmsgTransportSetupServicePort: "transport-setup-service",
		skyenv.DmsgGRPCPort:                  "grpc",
		skyenv.DmsgCXOPort:                   "stats", // system CXO feed
		80:                                   "dmsg-http",
		skyenv.DmsgDHTPort:                   "dht",
		skyenv.DmsgAwaitSetupPort:            "await-setup",
	}
}

// RegisterCXOFeed creates and starts a user CXO feed under name on
// dmsgPort. Idempotent on (name, port) — registering an existing feed
// returns nil without re-creating it. Conflicts (same name with a
// different port, or a port collision with another feed/system port)
// return errors.
func (v *Visor) RegisterCXOFeed(name string, dmsgPort uint16, description string) error {
	if name == "" {
		return errors.New("cxo feed: name required")
	}
	if name == systemCXOFeedName {
		return fmt.Errorf("cxo feed: name %q reserved for the system telemetry feed", name)
	}
	if dmsgPort == 0 {
		return errors.New("cxo feed: dmsg port required")
	}
	if used, ok := reservedDmsgPorts()[dmsgPort]; ok {
		return fmt.Errorf("cxo feed: dmsg port %d reserved for %s", dmsgPort, used)
	}
	if v.dmsgC == nil {
		return errors.New("cxo feed: dmsg client not available")
	}

	v.cxoUserFeedsMu.Lock()
	defer v.cxoUserFeedsMu.Unlock()

	if v.cxoUserFeeds == nil {
		v.cxoUserFeeds = make(map[string]*cxoUserFeed)
	}
	if existing, ok := v.cxoUserFeeds[name]; ok {
		if existing.port != dmsgPort {
			return fmt.Errorf("cxo feed %q already registered on port %d", name, existing.port)
		}
		if description != "" && existing.description != description {
			existing.description = description
		}
		return nil
	}
	for _, fd := range v.cxoUserFeeds {
		if fd.port == dmsgPort {
			return fmt.Errorf("cxo feed: dmsg port %d already used by feed %q", dmsgPort, fd.name)
		}
	}

	log := v.MasterLogger().PackageLogger("cxo_user_feed:" + name)
	dataDir := filepath.Join(v.conf.LocalPath, "cxo-user-feeds", name)
	pub, err := treestore.NewWithDMSG(v.dmsgC, v.conf.SK, treestore.PubConfig{
		BatchWindow: 1 * time.Second,
		Logger:      log,
		DataDir:     dataDir,
		DmsgPort:    dmsgPort,
	})
	if err != nil {
		return fmt.Errorf("cxo feed %q: start publisher: %w", name, err)
	}
	v.cxoUserFeeds[name] = &cxoUserFeed{
		name:        name,
		port:        dmsgPort,
		description: description,
		pub:         pub,
	}
	log.WithField("port", dmsgPort).WithField("data_dir", dataDir).
		Info("CXO user feed registered")
	return nil
}

// UnregisterCXOFeed stops a previously-registered user feed.
// Removing the system feed by name is rejected. Unknown names are a
// no-op (idempotent).
func (v *Visor) UnregisterCXOFeed(name string) error {
	if name == systemCXOFeedName {
		return fmt.Errorf("cxo feed: cannot unregister system feed %q", name)
	}
	v.cxoUserFeedsMu.Lock()
	feed, ok := v.cxoUserFeeds[name]
	if ok {
		delete(v.cxoUserFeeds, name)
	}
	v.cxoUserFeedsMu.Unlock()
	if !ok {
		return nil
	}
	if err := feed.pub.Close(); err != nil {
		return fmt.Errorf("cxo feed %q: close: %w", name, err)
	}
	v.log.WithField("name", name).Info("CXO user feed unregistered")
	return nil
}

// ListCXOFeeds implements logserver.CXOFeedsLister. Returns the system
// telemetry feed plus every user-registered feed, sorted by port for
// stable output.
func (v *Visor) ListCXOFeeds() []logserver.CXOFeedEntry {
	out := []logserver.CXOFeedEntry{
		{
			Name:        systemCXOFeedName,
			DmsgPort:    skyenv.DmsgCXOPort,
			Description: "visor self-tracking telemetry (transports, uptime, services)",
			System:      true,
		},
	}
	v.cxoUserFeedsMu.Lock()
	for _, fd := range v.cxoUserFeeds {
		out = append(out, logserver.CXOFeedEntry{
			Name:        fd.name,
			DmsgPort:    fd.port,
			Description: fd.description,
		})
	}
	v.cxoUserFeedsMu.Unlock()
	// Stable order: system feed first (it's appended first), then user
	// feeds in port order so listing diffs are deterministic.
	if len(out) > 2 {
		sortFeedsByPort(out[1:])
	}
	return out
}

// closeAllCXOUserFeeds stops every user feed. Called from the visor
// close stack on shutdown.
func (v *Visor) closeAllCXOUserFeeds() error {
	v.cxoUserFeedsMu.Lock()
	feeds := v.cxoUserFeeds
	v.cxoUserFeeds = nil
	v.cxoUserFeedsMu.Unlock()

	var firstErr error
	for name, fd := range feeds {
		if err := fd.pub.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("cxo feed %q close: %w", name, err)
		}
	}
	return firstErr
}

func sortFeedsByPort(feeds []logserver.CXOFeedEntry) {
	for i := 1; i < len(feeds); i++ {
		for j := i; j > 0 && feeds[j-1].DmsgPort > feeds[j].DmsgPort; j-- {
			feeds[j-1], feeds[j] = feeds[j], feeds[j-1]
		}
	}
}

// initCXOUserFeeds wires the user-feed registry into the visor
// lifecycle. Runs after initStats so the logserver's CXOFeedsLister
// hookup happens after lsAPI is available. The registry itself starts
// empty — feeds are added at runtime via RPC.
func initCXOUserFeeds(_ context.Context, v *Visor, log *logging.Logger) error {
	v.initLock.RLock()
	lsAPI := v.logServer.api
	v.initLock.RUnlock()
	if lsAPI != nil {
		lsAPI.SetCXOFeedsLister(v)
	}
	v.pushCloseStack("cxo_user_feeds", v.closeAllCXOUserFeeds)
	log.Debug("CXO user-feed registry ready")
	return nil
}
