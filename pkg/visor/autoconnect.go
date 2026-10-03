// Package visor pkg/visor/autoconnect.go c3-vis-core
package visor

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
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

// initialAutoconnectDelay is how long after boot the FIRST public-autoconnect
// pass fires (vs the full PublicServiceDelay for every pass after it). Long
// enough for dmsg + transport clients to be up, short enough that a client
// visor reaches the mesh in seconds rather than 5 minutes.
const initialAutoconnectDelay = 3 * time.Second

// browserAutoconnectRetry is the next pass's delay for a visor that cannot make
// stcpr (a browser) while it holds fewer swtr transports than its cap. swtr is
// the best carrier such a visor has, and waiting PublicServiceDelay left the
// tab on webrtc first hops for its first five minutes.
const browserAutoconnectRetry = 30 * time.Second

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
		Tm:    tm,
		DmsgC: dmsgC,
		Log:   log,
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
	// Fire the first public-autoconnect pass a few seconds after boot instead
	// of waiting a full PublicAutoconnectInterval (5 min). A fresh non-public
	// (client) visor otherwise can't reach the public mesh via autoconnect for
	// five minutes — measured as the exact analog of the wasm visor's fixed
	// pre-delay. A self-resetting timer keeps the steady-state 5-min cadence
	// after the first pass; on a public hub inbound transports fill in fast
	// regardless, so this is purely upside for clients.
	publicServiceTimer := time.NewTimer(initialAutoconnectDelay)
	defer publicServiceTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return context.Canceled
		case <-publicServiceTimer.C:
			publicServiceTimer.Reset(PublicServiceDelay)

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
			// Autoconnect only ever creates DIRECT transports (sudph/stcpr/squicr/
			// webrtc), so honor the transport-creation policy here: with
			// no_direct_transports (or a per-type deny) set, CanCreateTransport is
			// false and the corresponding phase is skipped entirely — no wasted dials,
			// no per-cycle "creation disabled" warnings from the funnel. dmsg is not an
			// autoconnect phase, so the visor still keeps its dmsg baseline.
			localSupportsSUDPH := a.tm.IsKnownNetwork(tptypes.SUDPH) && a.tm.CanCreateTransport(tptypes.SUDPH)
			localSupportsSTCPR := a.tm.IsKnownNetwork(tptypes.STCPR) && a.tm.CanCreateTransport(tptypes.STCPR)
			localSupportsSQUICR := a.tm.IsKnownNetwork(tptypes.QUIC) && a.tm.CanCreateTransport(tptypes.QUIC)            // QUIC(squicr): 3rd distinct carrier family for route diversity
			localSupportsWEBRTC := a.tm.IsKnownNetwork(tptypes.WEBRTC) && a.tm.CanCreateTransport(tptypes.WEBRTC)        // NAT-traversing (ICE/STUN); reaches more NAT types than sudph hole-punch
			localSupportsWT := a.tm.IsKnownNetwork(tptypes.WT) && a.tm.CanCreateTransport(tptypes.WT)                    // WT(swtr): HTTP/3 carrier — one of the three a browser visor can dial
			localSupportsWS := a.tm.IsKnownNetwork(tptypes.WS) && a.tm.CanCreateTransport(tptypes.WS) && wsPageAllowed() // WS(swsr): WebSocket riding the peer's stcpr port — but an HTTPS page blocks ws:// as mixed content, so don't waste the dials there
			if !localSupportsSUDPH && !localSupportsSTCPR && !localSupportsSQUICR &&
				!localSupportsWT && !localSupportsWS && !localSupportsWEBRTC {
				a.log.Warn("No supported network types available locally — skipping autoconnect cycle")
				continue
			}

			// Check if this visor is configured as public
			// This uses the same config flag that initPublicVisor uses to decide whether to register in SD
			visorIsPublic := v.conf.IsPublic
			a.visorIsPublic = visorIsPublic
			if visorIsPublic {
				a.log.Debug("This visor is configured as public")
			}

			a.log.WithField("public", len(absent1)).Debugln("Public visors to connect to")

			// Public autoconnect logic:
			// Phase 1:  STCPR to public visors lacking one (first in the preference order)
			// Phase 2:  QUIC (squicr) to public visors lacking one (second carrier family)
			// Phase 2b: SUDPH to public visors that neither reached
			// Phase 3:  SUDPH to other connected visors (non-public visors only)
			// Phase 4:  WebRTC LAST-RESORT fallback to peers no direct carrier reached

			const maxPublicVisors = 5 // Connect to up to 5 public visors
			const maxWEBRTC = 20      // Max WebRTC transports to other (webrtc-capable) visors
			// A visor without stcpr (a browser) has swtr as its best carrier, so it
			// holds as many of those as webrtc rather than the native handful.
			maxSWTR := maxPublicVisors
			if !localSupportsSTCPR {
				maxSWTR = maxWEBRTC
			}

			// Count existing automatic transports by type and remote PK
			countSTCPR := 0
			countSUDPH := 0
			countSQUICR := 0
			countWEBRTC := 0
			countSWTR := 0
			countSWSR := 0
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
				case tptypes.QUIC:
					countSQUICR++
				case tptypes.WEBRTC:
					countWEBRTC++
				case tptypes.WT:
					countSWTR++
				case tptypes.WS:
					countSWSR++
				}
			}

			// What the address resolver knows about reaching each peer, per type
			// (autoconnect_reach.go). Read once per cycle.
			reach := a.loadReach(ctx, v)
			selfNAT := v.selfNAT()

			// Track which public visors we connect to
			connectedPublicVisors := make([]cipher.PubKey, 0, maxPublicVisors)

			// Public visors are reached over the carriers the transport preference
			// ranks first: stcpr, then squicr. sudph (hole-punched UDP) is for peers
			// that cannot be reached over TCP or QUIC; a NAT'd visor still dials a
			// public one over stcpr. Until now sudph came first and a peer with ANY
			// automatic transport was dropped from the later phases, so a public
			// visor that got a sudph never got stcpr or squicr — and a black-holed
			// sudph (a UDP dial cannot fail) was all it ever had.
			autoTPs := a.tm.GetTransportsByLabel(transport.LabelAutomatic)

			// Phase 1: STCPR to every public visor that lacks one.
			if localSupportsSTCPR {
				a.log.Debug("Phase 1: Connecting to public visors via STCPR")
				phase1, err := a.connectByReach(ctx, v.conf.PK, a.filterDuplicatesOfType(addrs, tptypes.STCPR, autoTPs), tptypes.STCPR,
					existingByPK, reach, selfNAT, 0, 0, 0, true)
				if err != nil {
					return err
				}
				countSTCPR += phase1.Count
				connectedPublicVisors = phase1.Connected
			} else {
				// No stcpr locally (a browser visor): every public visor it has no
				// transport to is a candidate for the WT/WS phases, which dial them in
				// reach-verdict order and explore at most reachExplorePerCycle
				// untested ones. A random handful used to be picked instead: with
				// swtr open on ~24 public visors and closed on ~800 (2026-09-30),
				// most picks could not be reached and the tab held one or two
				// transports, so losing one took its proxy down.
				// A peer it reaches only over webrtc or dmsg stays a candidate:
				// those are the carriers swtr should replace.
				direct := map[cipher.PubKey]bool{}
				for _, tp := range autoTPs {
					if t := tp.Type(); t != tptypes.WEBRTC && t != tptypes.DMSG {
						direct[tp.Remote()] = true
					}
				}
				for _, pk := range visorcore.ShufflePubKeys(addrs) {
					if pk != v.conf.PK && !direct[pk] {
						connectedPublicVisors = append(connectedPublicVisors, pk)
					}
				}
			}

			// Phase 2: QUIC (squicr) to public visors that lack one — a second
			// carrier family next to stcpr so route setup has a real per-hop choice.
			// Budgeted per cycle so the peer count grows gradually.
			if localSupportsSQUICR {
				a.log.Debug("Phase 2: Connecting to public visors via QUIC (squicr)")
				phase2, err := a.connectByReach(ctx, v.conf.PK, a.filterDuplicatesOfType(addrs, tptypes.QUIC, autoTPs), tptypes.QUIC,
					existingByPK, reach, selfNAT, maxPublicVisors, 0, 0, false)
				if err != nil {
					return err
				}
				countSQUICR += phase2.Count
			}

			// Phase 2b: SUDPH to public visors that still have NO direct transport
			// after the stcpr and squicr phases (re-read: those phases just added some).
			if localSupportsSUDPH {
				a.log.Debug("Phase 2b: Connecting to direct-unreachable public visors via SUDPH")
				phase2b, err := a.connectByReach(ctx, v.conf.PK, a.filterDuplicates(addrs, a.tm.GetTransportsByLabel(transport.LabelAutomatic)), tptypes.SUDPH,
					existingByPK, reach, selfNAT, 0, 0, reachExplorePerCycle, false)
				if err != nil {
					return err
				}
				countSUDPH += phase2b.Count
			}

			// Phase 3: SUDPH to other connected visors (non-public visors only)
			// The candidates are the visors the reach feed lists as bound for sudph;
			// connectByReach drops those without a live UDP control connection at
			// the AR or with a NAT class this visor cannot punch to, and dials the
			// ones that recently accepted a sudph first.
			sudphPeers := a.nonPublicPeers(v.conf.PK, reachPeers(reach, arfeed.TypeSUDPH), addrs)
			if localSupportsSUDPH && !visorIsPublic && len(sudphPeers) > 0 {
				a.log.Debug("Phase 3: Connecting to other visors via SUDPH")
				phase3, err := a.connectByReach(ctx, v.conf.PK, sudphPeers, tptypes.SUDPH,
					existingByPK, reach, selfNAT, 0, countSUDPH, reachExplorePerCycle, false)
				if err != nil {
					return err
				}
				countSUDPH += phase3.Count
			}

			// Phase 3b: WT, then WS, to public visors that still lack ANY direct
			// transport. These are two of the three carriers a browser visor can
			// dial (it has no raw TCP/UDP socket): without this phase the
			// root-binary visor under js could only ever form webrtc — the old
			// wasm edge's own autoconnect dialed WT/WS and this one didn't. On a
			// native visor the peers are normally reached by stcpr/sudph/quic
			// above, so the no-direct filter keeps these phases from stacking
			// extra transports onto already-reached peers; they only fire where
			// the lighter families all failed. WT before WS mirrors the global
			// preference order (…WT > WS > WEBRTC), and whatever succeeds here
			// is then skipped by the WebRTC fallback's own hasDirect filter.
			if localSupportsWT || localSupportsWS {
				hasDirectTP := map[cipher.PubKey]bool{}
				hasLink := map[cipher.PubKey]bool{}
				for _, tp := range a.tm.GetTransportsByLabels(transport.LabelAutomatic, transport.LabelUser, transport.LabelSkycoin) {
					switch tp.Type() {
					case tptypes.WEBRTC, tptypes.DMSG:
						// below swtr, but a peer the visor already reaches is one
						// to move up
						hasLink[tp.Remote()] = true
					default:
						if tp.Entry.Label == transport.LabelAutomatic {
							hasDirectTP[tp.Remote()] = true
						}
					}
				}
				// Peers the visor already reaches over webrtc or dmsg are dialed
				// in a pass of their own, before slots go to peers it has never
				// reached: the reach verdicts reorder one pass, and a browser
				// visor's own host would rarely surface among about a thousand
				// public visors.
				var known, wtwsTargets []cipher.PubKey
				for _, pk := range connectedPublicVisors {
					switch {
					case hasDirectTP[pk]:
					case hasLink[pk]:
						known = append(known, pk)
						wtwsTargets = append(wtwsTargets, pk)
					default:
						wtwsTargets = append(wtwsTargets, pk)
					}
				}
				if len(wtwsTargets) > 0 && localSupportsWT {
					a.log.Debug("Phase 3b: Connecting to direct-unreachable public visors via WT (swtr)")
					for _, targets := range [][]cipher.PubKey{known, wtwsTargets} {
						var pending []cipher.PubKey
						for _, pk := range targets {
							if !hasDirectTP[pk] {
								pending = append(pending, pk)
							}
						}
						if len(pending) == 0 {
							continue
						}
						phaseWT, err := a.connectByReach(ctx, v.conf.PK, pending, tptypes.WT,
							existingByPK, reach, selfNAT, maxSWTR, countSWTR, reachExplorePerCycle, false)
						if err != nil {
							return err
						}
						countSWTR += phaseWT.Count
						for _, pk := range phaseWT.Connected {
							hasDirectTP[pk] = true
						}
					}
				}
				if localSupportsWS {
					var wsTargets []cipher.PubKey
					for _, pk := range wtwsTargets {
						if !hasDirectTP[pk] {
							wsTargets = append(wsTargets, pk)
						}
					}
					if len(wsTargets) > 0 {
						a.log.Debug("Phase 3c: Connecting to direct-unreachable public visors via WS (swsr)")
						phaseWS, err := a.connectByReach(ctx, v.conf.PK, wsTargets, tptypes.WS,
							existingByPK, reach, selfNAT, maxPublicVisors, countSWSR, reachExplorePerCycle, false)
						if err != nil {
							return err
						}
						countSWSR += phaseWS.Count
					}
				}
			}

			// Phase 4: WebRTC as a LAST-RESORT fallback — only to peers no direct
			// carrier (stcpr/sudph/squicr) could establish. WebRTC's ICE/STUN reaches
			// more NAT types than sudph hole-punch, but it's heavy, so we spend it
			// only where nothing lighter works. dmsg does NOT count as "reachable"
			// here — the point is a DIRECT path better than dmsg relay. Candidates are
			// the public visors plus the peers that declare they accept WebRTC; a
			// peer that declares it does not, or whose NAT class cannot pair with
			// ours, is skipped (autoconnect_reach.go).
			if localSupportsWEBRTC {
				hasDirect := map[cipher.PubKey]bool{}
				hasWebRTC := map[cipher.PubKey]bool{}
				for _, tp := range a.tm.GetTransportsByLabel(transport.LabelAutomatic) {
					switch tp.Type() {
					case tptypes.WEBRTC:
						hasWebRTC[tp.Remote()] = true
					case tptypes.DMSG:
						// dmsg is the relay baseline, not a direct path — ignore
					default:
						hasDirect[tp.Remote()] = true
					}
				}
				var webrtcTargets []cipher.PubKey
				for _, pk := range connectedPublicVisors {
					if !hasDirect[pk] && !hasWebRTC[pk] {
						webrtcTargets = append(webrtcTargets, pk)
					}
				}
				for _, pk := range a.nonPublicPeers(v.conf.PK, reachPeers(reach, arfeed.TypeWEBRTC), addrs) {
					if !hasDirect[pk] && !hasWebRTC[pk] {
						webrtcTargets = append(webrtcTargets, pk)
					}
				}
				if len(webrtcTargets) > 0 {
					a.log.Debug("Phase 4: WebRTC fallback to direct-unreachable visors")
					phase4, err := a.connectByReach(ctx, v.conf.PK, webrtcTargets, tptypes.WEBRTC,
						existingByPK, reach, selfNAT, maxWEBRTC, countWEBRTC, reachExplorePerCycle, false)
					if err != nil {
						return err
					}
					countWEBRTC += phase4.Count
				}
			}

			a.log.WithField("stcpr", countSTCPR).WithField("sudph", countSUDPH).
				WithField("squicr", countSQUICR).
				WithField("webrtc", countWEBRTC).
				WithField("swtr", countSWTR).WithField("swsr", countSWSR).
				WithField("public_visors", len(connectedPublicVisors)).
				Debug("Public autoconnect cycle completed")
			if !localSupportsSTCPR && localSupportsWT && countSWTR < maxSWTR {
				publicServiceTimer.Reset(browserAutoconnectRetry)
			}
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
// services/visor/ and returns the visor PKs as a slice. Reads the
// batched per-type leaf (services/visor/all) preferred, and the legacy
// per-service leaves (services/visor/<pk>/entry) as a fallback. Empty
// slice (rather than nil) when the snapshot exists but has no visor
// entries; nil when the snapshot is missing entirely (caller falls
// through to HTTP).
func pubVisorsFromCXOSnapshot(mgr *CXOSubscriptionManager) []cipher.PubKey {
	var pks []cipher.PubKey
	mgr.Walk(FeedSDServices, "services/visor/", func(path string, body []byte) bool {
		// Batched per-type leaf: services/visor/all.
		if strings.HasSuffix(path, "/all") {
			for _, svc := range decodeServicesBatch(body) {
				pks = append(pks, svc.Addr.PubKey())
			}
			return true
		}
		// Legacy per-service leaf: services/visor/<pk>/entry.
		if !strings.HasSuffix(path, "/entry") {
			return true
		}
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

// nonPublicPeers returns the candidates that are not public visors (the
// earlier phases reach those), not this visor, and not already reached by an
// automatic transport.
func (a *autoconnector) nonPublicPeers(self cipher.PubKey, candidates map[cipher.PubKey]struct{}, public []cipher.PubKey) []cipher.PubKey {
	if len(candidates) == 0 {
		return nil
	}
	skip := make(map[cipher.PubKey]struct{}, len(public)+1)
	skip[self] = struct{}{}
	for _, pk := range public {
		skip[pk] = struct{}{}
	}
	pks := make([]cipher.PubKey, 0, len(candidates))
	for pk := range candidates {
		if _, s := skip[pk]; !s {
			pks = append(pks, pk)
		}
	}
	return a.filterDuplicates(pks, a.tm.GetTransportsByLabel(transport.LabelAutomatic))
}

// filterDuplicatesOfType returns the pks that have no transport of type t in trs.
func (a *autoconnector) filterDuplicatesOfType(pks []cipher.PubKey, t tptypes.Type, trs []*transport.ManagedTransport) []cipher.PubKey {
	var absent []cipher.PubKey
	for _, pk := range pks {
		found := false
		for _, tr := range trs {
			if tr.Entry.Type == t && tr.Entry.HasEdge(pk) {
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
