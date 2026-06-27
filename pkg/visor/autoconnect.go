// Package visor pkg/visor/autoconnect.go
package visor

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorcore"
)

// PublicServiceDelay defines the interval for checking service discovery and adding transports to public visors.
const PublicServiceDelay = skyenv.PublicAutoconnectInterval

// sudphCacheTTL defines how long the cached SUDPH-capable visors list remains valid.
const sudphCacheTTL = 5 * time.Minute

// ConnectFn provides a way to connect to remote service
type ConnectFn func(context.Context, cipher.PubKey) error

// Autoconnector continuously tries to connect to services
type Autoconnector interface {
	Run(context.Context, *Visor) error
}

type autoconnector struct {
	client         *servicedisc.HTTPClient
	maxConns       int
	log            *logging.Logger
	tm             *transport.Manager
	dmsgC          *dmsg.Client // for reachability probes
	visorIsPublic  bool
	clientPublicIP string

	// conn is the platform-neutral connect-to-visors primitive (extracted to
	// pkg/visor/visorcore so the wasm-visor can share it). The autoconnector
	// owns the native-coupled loop + public-visor sourcing and delegates the
	// per-target transport establishment to conn.
	conn *visorcore.Connector

	sudphVisors        map[cipher.PubKey]struct{}
	sudphVisorsFetched time.Time
}

// MakeConnector returns a new connector that will try to connect to at most maxConns
// services
func MakeConnector(conf servicedisc.Config, maxConns int, tm *transport.Manager, dmsgC *dmsg.Client, httpC *http.Client, clientPublicIP string,
	log *logging.Logger, mLog *logging.MasterLogger) Autoconnector {
	// Extract just the IP from clientPublicIP (may include port)
	publicIP := clientPublicIP
	if host, _, err := net.SplitHostPort(publicIP); err == nil {
		publicIP = host
	}

	connector := &autoconnector{}
	connector.client = servicedisc.NewClient(log, mLog, conf, httpC, clientPublicIP)
	connector.maxConns = maxConns
	connector.log = log
	connector.tm = tm
	connector.dmsgC = dmsgC
	connector.clientPublicIP = publicIP
	connector.conn = &visorcore.Connector{
		Tm:             tm,
		DmsgC:          dmsgC,
		ClientPublicIP: publicIP,
		Log:            log,
	}
	return connector
}

// isContextError returns true if the error is a context cancellation/deadline.
// net/http and url.Error wrap context errors with %w, so errors.Is unwraps to
// the original context.Canceled / context.DeadlineExceeded sentinel.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

// Run implements Autoconnector interface
func (a *autoconnector) Run(ctx context.Context, v *Visor) (err error) {
	publicServiceTicker := time.NewTicker(PublicServiceDelay)

	for {
		select {
		case <-ctx.Done():
			return context.Canceled
		case <-publicServiceTicker.C:

			a.log.Infoln("Fetching public visors")

			// fetch public visors
			var addrs []cipher.PubKey
			addrs, err = a.fetchPubAddresses(ctx, v)
			if err != nil {
				a.log.Errorf("Cannot fetch public visors from service discovery: %s", err)
				v.isServicesHealthy.unset()
				v.isAutoconnectHealthy.unset()
				continue
			}
			v.isServicesHealthy.set()
			v.isAutoconnectHealthy.set()

			if len(addrs) == 0 {
				a.log.Debugln("No public visors in service discovery, trying TPD fallback")
				// Fallback: query TPD per-key-stats for well-connected visors
				fallbackAddrs, err := a.fetchFallbackVisors(ctx, v)
				if err != nil {
					a.log.WithError(err).Debug("TPD fallback failed")
					continue
				}
				if len(fallbackAddrs) == 0 {
					a.log.Debugln("No fallback visors found either")
					continue
				}
				a.log.WithField("count", len(fallbackAddrs)).Debug("Using TPD fallback visors")
				addrs = fallbackAddrs
			}

			a.log.WithField("public visors", len(addrs)).Debugln("Found")

			absent1 := a.filterDuplicates(addrs, a.tm.GetTransportsByLabel(transport.LabelAutomatic))

			// Check which transport types are supported locally
			localSupportsSUDPH := a.tm.IsKnownNetwork(tptypes.SUDPH)
			localSupportsSTCPR := a.tm.IsKnownNetwork(tptypes.STCPR)

			if !localSupportsSUDPH && !localSupportsSTCPR {
				a.log.Warn("No supported network types available locally (SUDPH and STCPR both unavailable)")
				continue
			}

			// Check if this visor is configured as public
			// This uses the same config flag that initPublicVisor uses to decide whether to register in SD
			visorIsPublic := v.conf.IsPublic
			a.visorIsPublic = visorIsPublic
			if visorIsPublic {
				a.log.Debug("This visor is configured as public")
			}

			// Fetch ALL transport discovery data once per cycle to reduce API load
			// This replaces individual DiscoverTransportsByPK calls throughout the cycle
			a.log.Debug("Fetching all transport discovery data for caching")
			transportCache, err := a.buildTransportCache(ctx, v)
			if err != nil {
				a.log.WithError(err).Warn("Failed to fetch transport discovery cache, continuing without it")
				transportCache = &transportDiscoveryCache{
					entriesByPK:     make(map[cipher.PubKey][]*transport.Entry),
					transportCounts: make(map[cipher.PubKey]int),
				}
			} else {
				a.log.WithField("cached_keys", len(transportCache.transportCounts)).
					Debug("Successfully cached transport discovery data")
			}

			// auto-transport logic - for non-public visors connecting to public visors
			var absent2 []cipher.PubKey
			if !visorIsPublic {
				absentSet := make(map[cipher.PubKey]struct{}, len(addrs))
				for _, pk := range addrs {
					absentSet[pk] = struct{}{}
				}

				autoTransports := a.tm.GetTransportsByLabel(transport.LabelAutomatic)
				absent2Set := make(map[cipher.PubKey]struct{})

				// Use cached transport data instead of individual API calls
				for _, pk := range addrs {
					entries := transportCache.entriesByPK[pk]

					seen := make(map[cipher.PubKey]struct{})
					for _, entry := range entries {
						for _, edge := range entry.Edges {
							if edge != v.conf.PK {
								seen[edge] = struct{}{}
							}
						}
					}

					entryKeys := make([]cipher.PubKey, 0, len(seen))
					for edge := range seen {
						entryKeys = append(entryKeys, edge)
					}

					filtered := a.filterDuplicates(entryKeys, autoTransports)
					for _, newPK := range filtered {
						if _, inAbsent := absentSet[newPK]; inAbsent {
							continue
						}
						if _, seen := absent2Set[newPK]; seen {
							continue
						}

						absent2Set[newPK] = struct{}{}
						absent2 = append(absent2, newPK)
					}
				}
			}

			a.log.WithField("total", len(append(absent1, absent2...))).
				Debugln("Found visors to connect to")

			// Public autoconnect logic:
			// Phase 1: SUDPH to public visors (if SUDPH available)
			// Phase 2: STCPR to public visors (both transport types to same visors)
			// Phase 3: SUDPH to other connected visors

			const maxPublicVisors = 5 // Connect to up to 5 public visors
			const maxSUDPH = 30       // Max SUDPH transports to other visors

			// Count existing automatic transports by type and remote PK
			countSTCPR := 0
			countSUDPH := 0
			existingByPK := make(map[cipher.PubKey]map[tptypes.Type]bool)
			for _, autoconnTP := range a.tm.GetTransportsByLabel(transport.LabelAutomatic) {
				remotePK := autoconnTP.Remote()
				if existingByPK[remotePK] == nil {
					existingByPK[remotePK] = make(map[tptypes.Type]bool)
				}
				existingByPK[remotePK][autoconnTP.Type()] = true
				switch autoconnTP.Type() {
				case tptypes.STCPR:
					countSTCPR++
				case tptypes.SUDPH:
					countSUDPH++
				}
			}

			// Track which public visors we connect to
			connectedPublicVisors := make([]cipher.PubKey, 0, maxPublicVisors)

			// Fetch SUDPH-capable visors from address resolver (cached for 5 minutes)
			var sudphCapable map[cipher.PubKey]struct{}
			if localSupportsSUDPH {
				sudphCapable = a.fetchSUDPHVisors(ctx)
			}

			// Phase 1: SUDPH to public visors (if supported)
			if localSupportsSUDPH {
				a.log.Debug("Phase 1: Connecting to public visors via SUDPH")
				phase1, err := a.conn.ConnectToVisors(ctx, v.conf.PK, absent1, tptypes.SUDPH,
					existingByPK, sudphCapable, maxPublicVisors, 0, true)
				if err != nil {
					return err
				}
				countSUDPH += phase1.Count
				connectedPublicVisors = phase1.Connected
			} else {
				// If no SUDPH, just pick public visors for STCPR
				for _, pk := range visorcore.ShufflePubKeys(absent1) {
					if len(connectedPublicVisors) >= maxPublicVisors {
						break
					}
					if pk != v.conf.PK {
						connectedPublicVisors = append(connectedPublicVisors, pk)
					}
				}
			}

			// Phase 2: STCPR to the same public visors
			if localSupportsSTCPR {
				a.log.Debug("Phase 2: Connecting to public visors via STCPR")
				phase2, err := a.conn.ConnectToVisors(ctx, v.conf.PK, connectedPublicVisors, tptypes.STCPR,
					existingByPK, nil, maxPublicVisors, 0, false)
				if err != nil {
					return err
				}
				countSTCPR += phase2.Count
			}

			// Phase 3: SUDPH to other connected visors
			if localSupportsSUDPH && countSUDPH < maxSUDPH && len(absent2) > 0 {
				a.log.Debug("Phase 3: Connecting to other visors via SUDPH")
				phase3, err := a.conn.ConnectToVisors(ctx, v.conf.PK, absent2, tptypes.SUDPH,
					existingByPK, sudphCapable, maxSUDPH, countSUDPH, false)
				if err != nil {
					return err
				}
				countSUDPH += phase3.Count
			}

			a.log.WithField("stcpr", countSTCPR).WithField("sudph", countSUDPH).
				WithField("public_visors", len(connectedPublicVisors)).
				Debug("Public autoconnect cycle completed")
		}
	}
}

func (a *autoconnector) fetchPubAddresses(ctx context.Context, v *Visor) ([]cipher.PubKey, error) {
	// CXO-first: when the on-demand subscription manager has a fresh
	// snapshot of SD's services tree, use it. The manager's cycle
	// runs at most once per `hypervisor.cxo_subscribe_interval`
	// (default 5min), and a fresh AcquireFor here kicks off that
	// cycle if no other consumer has already done so. Release on
	// return; the manager's grace period handles the next
	// autoconnect tick reusing the running cycle.
	if mgr := v.CXOSubMgr(); mgr != nil {
		mgr.AcquireFor(TabAutoconnect)
		defer mgr.ReleaseFor(TabAutoconnect)
		if pks := pubVisorsFromCXOSnapshot(mgr); len(pks) > 0 {
			a.log.WithField("count", len(pks)).Debug("Autoconnect: resolved public visors from CXO snapshot")
			return pks, nil
		}
	}

	// Fall back to HTTP service discovery — only when it's configured.
	// With service_discovery dropped (dmsg-only), there is no HTTP client; the
	// CXO snapshot above is the only public-visor source, so return cleanly
	// instead of nil-dereferencing the absent client.
	if !a.client.Configured() {
		a.log.Debug("Autoconnect: HTTP service discovery not configured; relying on CXO snapshot only")
		return nil, nil
	}
	var services []servicedisc.Service
	// Bounded, fail-fast fetch. The Run loop's ticker (PublicServiceDelay)
	// already retries this every cycle, so the per-tick fetch must NOT retry
	// forever: NewDefaultRetrier uses DefaultTries=0 (infinite), so against an
	// unreachable service-discovery — e.g. a clearnet SD that's dead on a
	// dmsg-only deployment — retrier.Do never returns, and fetchPubAddresses
	// WEDGES the entire autoconnect loop: no public visors are ever fetched and
	// no transports are ever created (observed on a v1.3.77 visor stuck here).
	// A few bounded tries + a per-fetch timeout make a dead SD degrade to "no
	// public visors this tick"; the loop moves on and re-checks the CXO snapshot
	// next cycle.
	retrier := netutil.NewRetrier(a.log, time.Second, 5*time.Second, 3, netutil.DefaultFactor)
	fetch := func() (err error) {
		fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		services, err = a.client.Services(fctx, a.maxConns, "", "")
		return err
	}
	if err := retrier.Do(ctx, fetch); err != nil {
		return nil, err
	}
	pks := make([]cipher.PubKey, len(services))
	for i, service := range services {
		pks[i] = service.Addr.PubKey()
	}
	return pks, nil
}

// pubVisorsFromCXOSnapshot walks the SD services tree under
// services/visor/<pk>/entry and returns the PKs as a slice. Empty
// slice (rather than nil) when the snapshot exists but has no visor
// entries; nil when the snapshot is missing entirely (caller falls
// through to HTTP).
func pubVisorsFromCXOSnapshot(mgr *CXOSubscriptionManager) []cipher.PubKey {
	var pks []cipher.PubKey
	mgr.Walk(FeedSDServices, "services/visor/", func(path string, _ []byte) bool {
		// path = services/visor/<pk>/{entry,tombstone}.
		// Skip tombstones; live entries only.
		if !strings.HasSuffix(path, "/entry") {
			return true
		}
		// Strip the prefix and trailing "/entry" to recover the PK.
		core := strings.TrimSuffix(strings.TrimPrefix(path, "services/visor/"), "/entry")
		if core == "" {
			return true
		}
		var pk cipher.PubKey
		if err := pk.Set(core); err != nil {
			return true
		}
		pks = append(pks, pk)
		return true
	})
	return pks
}

// return public keys from pks that are absent in given list of transports
func (a *autoconnector) filterDuplicates(pks []cipher.PubKey, trs []*transport.ManagedTransport) []cipher.PubKey {
	var absent []cipher.PubKey
	for _, pk := range pks {
		found := false
		for _, tr := range trs {
			if tr.Entry.HasEdge(pk) {
				found = true
				break
			}
		}
		if !found {
			absent = append(absent, pk)
		}
	}
	return absent
}

// fetchSUDPHVisors returns the set of visors registered for SUDPH in the address resolver,
// using a cached result if it is less than sudphCacheTTL old.
func (a *autoconnector) fetchSUDPHVisors(ctx context.Context) map[cipher.PubKey]struct{} {
	if a.sudphVisors != nil && time.Since(a.sudphVisorsFetched) < sudphCacheTTL {
		return a.sudphVisors
	}

	arClient := a.tm.ARClient()
	if arClient == nil {
		a.log.Warn("Address resolver client not available for SUDPH visor lookup")
		return a.sudphVisors
	}

	result, err := arClient.TransportsType(ctx, tptypes.SUDPH)
	if err != nil {
		a.log.WithError(err).Warn("Failed to fetch SUDPH visors from address resolver")
		return a.sudphVisors
	}

	sudphSet := make(map[cipher.PubKey]struct{}, len(result))
	for pk := range result {
		sudphSet[pk] = struct{}{}
	}
	a.sudphVisors = sudphSet
	a.sudphVisorsFetched = time.Now()
	a.log.WithField("count", len(sudphSet)).Debug("Cached SUDPH-capable visors from address resolver")
	return sudphSet
}

// transportDiscoveryCache holds cached transport discovery data to reduce API calls
type transportDiscoveryCache struct {
	entriesByPK     map[cipher.PubKey][]*transport.Entry
	transportCounts map[cipher.PubKey]int
}

// fetchFallbackVisors queries TPD per-key-stats to find well-connected visors
// when service discovery returns no public visors.
// It returns visors with at least minFallbackTransports transports.
func (a *autoconnector) fetchFallbackVisors(ctx context.Context, v *Visor) ([]cipher.PubKey, error) {
	const minFallbackTransports = 5 // Visors with >= 5 transports are considered well-connected

	tpD := v.tpDiscClient()
	if tpD == nil {
		return nil, errors.New("transport discovery client not available")
	}

	perKeyStats, err := tpD.GetAllTransportsPerKeyStats(ctx)
	if err != nil {
		return nil, err
	}

	var candidates []cipher.PubKey
	for pkHex, counts := range perKeyStats {
		total, ok := counts["total"]
		if !ok || total < minFallbackTransports {
			continue
		}

		// Skip self
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
			continue
		}
		if pk == v.conf.PK {
			continue
		}

		candidates = append(candidates, pk)
	}

	a.log.WithField("candidates", len(candidates)).
		Debug("Found fallback visor candidates from TPD per-key-stats")

	return candidates, nil
}

// buildTransportCache fetches all transport discovery data once and builds lookup maps.
// This replaces hundreds of individual DiscoverTransportsByPK API calls with a single
// GetAllTransports call, dramatically reducing load on the transport discovery service.
func (a *autoconnector) buildTransportCache(ctx context.Context, v *Visor) (*transportDiscoveryCache, error) {
	tpD := v.tpDiscClient()

	// Fetch ALL transports in one API call (instead of per-key calls)
	allEntries, err := tpD.GetAllTransports(ctx)
	if err != nil {
		return nil, err
	}

	cache := &transportDiscoveryCache{
		entriesByPK:     make(map[cipher.PubKey][]*transport.Entry),
		transportCounts: make(map[cipher.PubKey]int),
	}

	// Build lookup maps: pk -> entries and pk -> count
	for _, entry := range allEntries {
		for _, edge := range entry.Edges {
			cache.entriesByPK[edge] = append(cache.entriesByPK[edge], entry)
			cache.transportCounts[edge]++
		}
	}

	return cache, nil
}
