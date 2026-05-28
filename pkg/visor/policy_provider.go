// Package visor pkg/visor/policy_provider.go — visor-backed
// implementation of pkg/router/policy.Provider. Surfaces the
// visor's transport-tracker state, embedded geoip database,
// operator-configured trust list, and configured hypervisor PKs
// to the operator's Starlark routing policy script.
//
// Per-call lookups, no caching — the underlying state (transport
// manager, geoip mmdb) is already optimized for in-process queries
// (sub-µs). Adding a TTL cache here would only matter if a policy
// hammers the same PKs in a tight loop, which today's per-dial
// policy invocation model doesn't.
package visor

import (
	"strings"
	"sync"

	"github.com/oschwald/geoip2-golang/v2"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/geoip"
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

// visorPolicyProvider implements router/policy.Provider on top of
// the running visor's state. Construct via newVisorPolicyProvider
// inside the router init.
type visorPolicyProvider struct {
	tpM *transport.Manager

	geoMu sync.Mutex
	geoDB *geoip2.Reader // nil when OpenEmbedded failed

	hypervisors map[string]struct{} // hex PK → present
	trusted     map[string]struct{} // hex PK → present (dmsgpty whitelist + hypervisors)
}

func newVisorPolicyProvider(v *Visor) *visorPolicyProvider {
	p := &visorPolicyProvider{
		tpM:         v.tpM,
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

// Geo implements policy.Provider. Looks up the peer's most-recent
// transport's remote IP and queries the embedded geoip db. Returns
// "??" when:
//   - the geoip db isn't loaded
//   - no transport exists for this PK
//   - the IP isn't in the db
func (p *visorPolicyProvider) Geo(pk string) string {
	if p.geoDB == nil || p.tpM == nil {
		return "??"
	}
	pubkey, ok := parsePK(pk)
	if !ok {
		return "??"
	}
	ip := p.remoteIPForPeer(pubkey)
	if ip == "" {
		return "??"
	}
	p.geoMu.Lock()
	res, err := geoip.Lookup(p.geoDB, ip)
	p.geoMu.Unlock()
	if err != nil || res == nil || res.CountryCode == "" {
		return "??"
	}
	return res.CountryCode
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
