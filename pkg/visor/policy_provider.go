// Package visor pkg/visor/policy_provider.go — visor-backed
// implementation of pkg/router/policy.Provider. Surfaces the
// visor's transport-tracker state, embedded geoip database,
// service-discovery snapshot, operator-configured trust list,
// and configured hypervisor PKs to the operator's Starlark
// routing policy script.
//
// Geo resolution is layered:
//  1. SD snapshot (PK→country built from public proxy/vpn/visor
//     entries). Covers multihop intermediates that advertise
//     themselves as a service — without this, every non-neighbor
//     PK would return "??" because the visor has no direct IP
//     for off-path hops. Refreshed on a TTL so it's not in the
//     dial hot path more than once every refreshTTL.
//  2. Direct-transport IP → embedded geoip. Catches direct
//     neighbors that don't advertise as a service.
//  3. "??" — the policy script's filter falls through.
//
// SD snapshot source is the CXO subscription manager (zero
// network cost when running) with an HTTP fallback (the existing
// servicesFromHTTP path used by VPNServers/ProxyServers/etc.)
// when CXO isn't installed or returns empty.
package visor

import (
	"strings"
	"sync"
	"time"

	"github.com/oschwald/geoip2-golang/v2"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/geoip"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// parsePK is the local helper for the multiple Provider methods
// that take a hex PK string. cipher.PubKey doesn't have a single-
// expression constructor; .Set() mutates the receiver.
func parsePK(s string) (cipher.PubKey, bool) {
	var pk cipher.PubKey
	if err := pk.Set(s); err != nil {
		return cipher.PubKey{}, false
	}
	return pk, true
}

// sdGeoRefreshTTL bounds how often the SD snapshot is rebuilt.
// SD entries change on the order of minutes (apps come and go);
// 5 minutes keeps the snapshot fresh enough for routing policy
// without burning CXO walks on every dial.
const sdGeoRefreshTTL = 5 * time.Minute

// visorPolicyProvider implements router/policy.Provider on top of
// the running visor's state. Construct via newVisorPolicyProvider
// inside the router init.
type visorPolicyProvider struct {
	tpM   *transport.Manager
	visor *Visor // for CXOSubMgr access + servicesFromCXO/HTTP fallback

	geoMu sync.Mutex
	geoDB *geoip2.Reader // nil when OpenEmbedded failed

	// SD-derived PK→country map. Populated lazily on first Geo
	// lookup and refreshed on a TTL. Lifetime-scoped to the
	// provider — no goroutine; lookups outside the TTL window
	// trigger a fresh refresh inline.
	sdMu      sync.RWMutex
	sdGeo     map[string]string // hex PK → ISO country code
	sdExpires time.Time

	hypervisors map[string]struct{} // hex PK → present
	trusted     map[string]struct{} // hex PK → present (dmsgpty whitelist + hypervisors)
}

func newVisorPolicyProvider(v *Visor) *visorPolicyProvider {
	p := &visorPolicyProvider{
		tpM:         v.tpM,
		visor:       v,
		hypervisors: make(map[string]struct{}),
		trusted:     make(map[string]struct{}),
	}

	// Embedded geoip DB. Failure to open isn't fatal — Geo() just
	// returns "??" until OpenEmbedded() works.
	if db, err := geoip.OpenEmbedded(); err == nil {
		p.geoDB = db
	} else if v.log != nil {
		v.log.WithError(err).Warn("routing-policy provider: embedded geoip db unavailable; geo.country() will return \"??\"")
	}

	// Pre-build the hypervisor set from config. Hypervisors are
	// auto-trusted (they're the operator's control plane).
	for _, pk := range v.conf.Hypervisors {
		p.hypervisors[pk.Hex()] = struct{}{}
		p.trusted[pk.Hex()] = struct{}{}
	}

	// Pre-build the trust set from the dmsgpty whitelist — it's
	// the most operator-curated trust signal the visor maintains.
	// (After the dmsgpty → pty rename track this is v.conf.Pty.)
	if v.conf.Pty != nil {
		for _, pk := range v.conf.Pty.Whitelist {
			p.trusted[pk.Hex()] = struct{}{}
		}
	}

	return p
}

// Geo implements policy.Provider. Tries the SD snapshot first
// (covers multihop intermediates), falls back to direct-transport
// IP + embedded geoip (covers direct neighbors that don't
// advertise as a service). Returns "??" when neither produces a
// country code — visor has no direct transport and the peer isn't
// in any public SD entry.
func (p *visorPolicyProvider) Geo(pk string) string {
	pkLower := strings.ToLower(pk)

	// 1. SD snapshot — the only path that works for multihop
	// intermediates the visor has no direct transport to.
	if c := p.sdGeoForPK(pkLower); c != "" {
		return c
	}

	// 2. Direct-transport IP + embedded geoip.
	if p.geoDB != nil && p.tpM != nil {
		if pubkey, ok := parsePK(pk); ok {
			ip := p.remoteIPForPeer(pubkey)
			if ip != "" {
				p.geoMu.Lock()
				res, err := geoip.Lookup(p.geoDB, ip)
				p.geoMu.Unlock()
				if err == nil && res != nil && res.CountryCode != "" {
					return res.CountryCode
				}
			}
		}
	}
	return "??"
}

// sdGeoForPK returns the country code for pk from the SD snapshot,
// refreshing the snapshot if the TTL has elapsed. Returns "" when
// the snapshot is empty or the PK isn't in it. pkLower must be
// already lowercased.
func (p *visorPolicyProvider) sdGeoForPK(pkLower string) string {
	p.sdMu.RLock()
	fresh := time.Now().Before(p.sdExpires) && p.sdGeo != nil
	if fresh {
		c := p.sdGeo[pkLower]
		p.sdMu.RUnlock()
		return c
	}
	p.sdMu.RUnlock()

	p.refreshSDGeo()

	p.sdMu.RLock()
	defer p.sdMu.RUnlock()
	if p.sdGeo == nil {
		return ""
	}
	return p.sdGeo[pkLower]
}

// refreshSDGeo rebuilds the SD-derived PK→country map. Tries CXO
// snapshot first (zero network cost when the subscription is
// active); falls back to HTTP service-discovery (which honors the
// visor's configured ServiceDisc URL — dmsg-routed when the visor
// is configured for dmsg-discovery). Iterates the three public
// service types so a visor advertised under any of them is
// resolvable.
//
// On total failure (CXO empty + every HTTP call errored) the
// previous snapshot stays in place; sdExpires is bumped so we
// don't refresh on every dial. The script falls through to the
// direct-transport path or "??".
func (p *visorPolicyProvider) refreshSDGeo() {
	p.sdMu.Lock()
	defer p.sdMu.Unlock()

	// Double-check after acquiring write lock — another goroutine
	// may have refreshed while we were waiting.
	if time.Now().Before(p.sdExpires) && p.sdGeo != nil {
		return
	}

	if p.visor == nil {
		p.sdGeo = map[string]string{}
		p.sdExpires = time.Now().Add(sdGeoRefreshTTL)
		return
	}

	// Hold the CXO subscription open across the three walks so
	// the manager doesn't tear down the feed between types.
	if mgr := p.visor.CXOSubMgr(); mgr != nil {
		mgr.AcquireFor(TabRoutingPolicy)
		defer mgr.ReleaseFor(TabRoutingPolicy)
	}

	next := make(map[string]string)
	for _, t := range []string{
		servicedisc.ServiceTypeProxy,
		servicedisc.ServiceTypeVPN,
		servicedisc.ServiceTypeVisor,
	} {
		var entries []servicedisc.Service
		if services, ok := p.visor.servicesFromCXO(t, "", ""); ok {
			entries = services
		} else if services, err := p.visor.servicesFromHTTP(t, "", ""); err == nil {
			entries = services
		} else if p.visor.log != nil {
			p.visor.log.WithError(err).WithField("service_type", t).
				Debug("routing-policy provider: SD refresh failed for type; multihop geo coverage degraded.")
		}
		for i := range entries {
			s := &entries[i]
			if s.Geo == nil || s.Geo.Country == "" {
				continue
			}
			pkHex := strings.ToLower(s.Addr.PubKey().Hex())
			// Last write wins across types — a visor advertised
			// under multiple types should resolve to the same
			// country regardless of which type was scanned last.
			next[pkHex] = s.Geo.Country
		}
	}
	p.sdGeo = next
	p.sdExpires = time.Now().Add(sdGeoRefreshTTL)
}

// Latency implements policy.Provider. Returns the most recent
// ms reading from the transport tracker. Zero means "unknown" —
// either no transport, no recent measurement, or a measurement
// of literally zero (which we conflate with unknown).
func (p *visorPolicyProvider) Latency(pk string) int {
	if p.tpM == nil {
		return 0
	}
	pubkey, ok := parsePK(pk)
	if !ok {
		return 0
	}
	if mt := p.preferredTransport(pubkey); mt != nil {
		return int(mt.GetLatency())
	}
	return 0
}

// Kind implements policy.Provider. Returns the network type of
// the visor's preferred transport to the peer ("stcpr" / "sudph"
// / "stcp" / "dmsg"). Empty when no transport exists.
func (p *visorPolicyProvider) Kind(pk string) string {
	if p.tpM == nil {
		return ""
	}
	pubkey, ok := parsePK(pk)
	if !ok {
		return ""
	}
	if mt := p.preferredTransport(pubkey); mt != nil {
		return string(mt.Entry.Type)
	}
	return ""
}

// IsTrusted implements policy.Provider. True when the PK is in
// either the dmsgpty whitelist or the hypervisor list (which is
// auto-trusted).
func (p *visorPolicyProvider) IsTrusted(pk string) bool {
	_, ok := p.trusted[strings.ToLower(pk)]
	return ok
}

// IsHypervisor implements policy.Provider. True when the PK is
// in conf.Hypervisors.
func (p *visorPolicyProvider) IsHypervisor(pk string) bool {
	_, ok := p.hypervisors[strings.ToLower(pk)]
	return ok
}

// preferredTransport returns the most-preferred existing transport
// to the peer (stcpr > sudph > stcp > dmsg) or nil. Used by
// Latency() and Kind() to get a single representative transport
// per peer without enumerating all of them.
func (p *visorPolicyProvider) preferredTransport(peer cipher.PubKey) *transport.ManagedTransport {
	for _, t := range []tptypes.Type{tptypes.STCPR, tptypes.SUDPH, tptypes.STCP, tptypes.DMSG} {
		if mt, err := p.tpM.GetTransport(peer, t); err == nil && mt != nil {
			return mt
		}
	}
	return nil
}

// remoteIPForPeer extracts the remote IP (no port) from the
// peer's preferred transport's raw remote address. Returns "" if
// no transport exists or the address isn't an IP form (dmsg
// addresses don't have a routable IP — they go through a relay).
func (p *visorPolicyProvider) remoteIPForPeer(peer cipher.PubKey) string {
	mt := p.preferredTransport(peer)
	if mt == nil || mt.Entry.Type == tptypes.DMSG {
		return ""
	}
	return mt.RemoteIP()
}
