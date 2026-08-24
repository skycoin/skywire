// Package visor pkg/visor/cxo_user_feeds.go c3-vis-core
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

	"github.com/google/uuid"

	"github.com/skycoin/skywire/cmd/apps/skychat/pairing"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/logserver"
)

// systemCXOFeedName is the well-known name of the visor's telemetry
// feed (the one initStats wires up on skyenv.DmsgCXOPort). Reserved
// so user-registered feeds can't collide with it.
const systemCXOFeedName = "stats"

// tplistCXOFeedName is the well-known name of the visor's dedicated
// transport-list discovery feed (the second CXO node initStats wires
// up on skyenv.DmsgVisorTPListCXOPort).
const tplistCXOFeedName = "tp-list"

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
		DmsgForwardProxyPort:                 "dmsg-forward-proxy",
		skyenv.DmsgDHTPort:                   "dht",
		skyenv.DmsgAwaitSetupPort:            "await-setup",
		skyenv.SkychatGroupProbePort:         "skychat-group-probe",
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
	// Reject ports inside the chat-pair allocator's deterministic
	// range. A user feed there would shadow some pair's CXO node,
	// causing silent, hard-to-debug failures for the affected pair.
	if dmsgPort >= pairing.PortBase && dmsgPort < pairing.PortBase+pairing.PortSpan {
		return fmt.Errorf("cxo feed: dmsg port %d reserved for chat-pair feeds (range [%d, %d))",
			dmsgPort, pairing.PortBase, pairing.PortBase+pairing.PortSpan)
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
	// Same 10s coalescing window as the stats publisher — user-feed
	// puts are typically operator-driven (skychat hub broadcasts,
	// custom RPC writes) where ~10s latency between publish and
	// downstream visibility is fine, and it cuts CXO bbolt commit
	// frequency by 10x relative to the historic 1s default.
	pub, err := treestore.NewWithDMSG(v.dmsgC, v.conf.SK, treestore.PubConfig{
		BatchWindow: 10 * time.Second,
		Logger:      log,
		DataDir:     dataDir,
		DmsgPort:    dmsgPort,
		// User-feed CXDS is content-addressed cache; the publisher's
		// pub.Put callers re-feed values from RPC / app state on
		// restart, so a torn write at crash time self-heals.
		NoSyncCXDS: true,
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

// setSystemCXOPub retains the telemetry/tp-list feed publisher so
// CXOFeedStates can read its live PublishState. Called once by initStats.
func (v *Visor) setSystemCXOPub(pub *treestore.Publisher) {
	v.cxoUserFeedsMu.Lock()
	v.systemCXOPub = pub
	v.cxoUserFeedsMu.Unlock()
}

// setTPListCXOPub retains the dedicated tp-list discovery feed publisher
// so CXOFeedStates can read its live PublishState. Called once by
// initStats; pub is nil when the dedicated publisher didn't start (the
// combined-feed fallback), in which case no second .cxo entry is emitted.
func (v *Visor) setTPListCXOPub(pub *treestore.Publisher) {
	v.cxoUserFeedsMu.Lock()
	v.tplistCXOPub = pub
	v.cxoUserFeedsMu.Unlock()
}

// CXOFeedState pairs a feed's identity (name + dmsg port) with its live
// publish-health snapshot. Surfaced by StateSnapshot under .cxo.
type CXOFeedState struct {
	Name string `json:"name"`
	Port uint16 `json:"port,omitempty"`
	treestore.PublishState
	// CurrentLeaves is populated only for the "stats" telemetry feed: it
	// breaks down the transports/<id>/current leaves the feed carries by
	// whether each leaf's transport is currently live. Dead > 0 quantifies
	// the stale-leaf bloat that Fix 1 (pruneDeadCurrentLeaves) removes at
	// startup and reconcileCurrentLeaves keeps out at steady state.
	CurrentLeaves *CurrentLeafStats `json:"current_leaves,omitempty"`
}

// CurrentLeafStats is the live/dead breakdown of the telemetry feed's
// transports/<id>/current leaf set, computed as the feed's current-leaf
// path list intersected with the visor's live transport set.
type CurrentLeafStats struct {
	Total int `json:"total"`
	Live  int `json:"live"`
	Dead  int `json:"dead"`
}

// currentLeafStats walks the publisher's transports/<id>/current leaves
// and classifies each by whether its transport is in liveIDs. Cheap: an
// in-memory tree walk plus a map lookup per leaf.
func currentLeafStats(pub *treestore.Publisher, liveIDs map[uuid.UUID]struct{}) CurrentLeafStats {
	var st CurrentLeafStats
	pub.Walk("transports", func(path string, _ []byte) bool {
		id, ok := currentLeafUUID(path)
		if !ok {
			return true
		}
		st.Total++
		if _, live := liveIDs[id]; live {
			st.Live++
		} else {
			st.Dead++
		}
		return true
	})
	return st
}

// CXOFeedStates returns the live PublishState of the system telemetry
// feed plus every user feed. The system feed (the one TPD subscribes to
// for a visor's transport list) is first. Empty when no publisher is
// wired (Stats.Disabled or pre-init). Reads are cheap and concurrency-
// safe — see treestore.Publisher.PublishState.
func (v *Visor) CXOFeedStates() []CXOFeedState {
	v.cxoUserFeedsMu.Lock()
	sysPub := v.systemCXOPub
	tplistPub := v.tplistCXOPub
	users := make([]*cxoUserFeed, 0, len(v.cxoUserFeeds))
	for _, fd := range v.cxoUserFeeds {
		users = append(users, fd)
	}
	v.cxoUserFeedsMu.Unlock()

	var out []CXOFeedState
	if sysPub != nil {
		// Live/dead current-leaf breakdown for the telemetry feed: the
		// feed's own current-leaf paths ∩ the live transport set. Directly
		// quantifies the dead-leaf bloat that starves TPD's Root fill.
		cls := currentLeafStats(sysPub, liveTransportIDs(v))
		out = append(out, CXOFeedState{
			Name:          systemCXOFeedName,
			Port:          skyenv.DmsgCXOPort,
			PublishState:  sysPub.PublishState(),
			CurrentLeaves: &cls,
		})
	}
	// The dedicated tp-list discovery feed (a second CXO node under the
	// same visor PK). nil when initStats fell back to the combined feed —
	// then there is only the one telemetry entry above.
	if tplistPub != nil {
		out = append(out, CXOFeedState{
			Name:         tplistCXOFeedName,
			Port:         skyenv.DmsgVisorTPListCXOPort,
			PublishState: tplistPub.PublishState(),
		})
	}
	for _, fd := range users {
		if fd.pub == nil {
			continue
		}
		out = append(out, CXOFeedState{
			Name:         fd.name,
			Port:         fd.port,
			PublishState: fd.pub.PublishState(),
		})
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
