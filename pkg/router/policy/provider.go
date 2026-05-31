// Package policy pkg/router/policy/provider.go — Provider
// interface for the data the stdlib (geo, transports, peers)
// surfaces to policy scripts. The Evaluator caches Provider
// results per-visor-uptime so even a script that queries the
// same data on every dial doesn't burn CPU.
//
// Production wires this to the visor's actual geoip / transport-
// tracker / hypervisor-list state. Tests inject FakeProvider with
// controlled return values to assert script semantics
// deterministically.
package policy

import (
	"sync"
)

// Provider is the contract the Evaluator uses to surface visor
// state to policy scripts. Methods are called per script
// invocation (memoized — same PK queried twice during one Decide
// returns the cached value), so they should be near-instant.
// Implementations that need to hit a network or do I/O should
// pre-cache before NewEvaluator returns.
type Provider interface {
	// Geo returns the ISO-3166 country code for a peer PK, or
	// "??" when unknown. Operators use this for geographic
	// routing filters ("only routes through ID").
	Geo(pk string) string

	// Latency returns the most recent measured round-trip in
	// milliseconds to this peer. Zero means "no recent
	// measurement" — scripts should treat zero as unknown, not
	// fast.
	Latency(pk string) int

	// Kind returns the transport family of the most recent
	// connection to this peer ("stcpr", "sudph", "dmsg"). Empty
	// when unknown.
	Kind(pk string) string

	// IsTrusted reports whether the peer's PK is in the
	// operator-configured trust list. Used by policies that want
	// "trusted peers get direct routes, untrusted peers go
	// through 2+ hops" style rules.
	IsTrusted(pk string) bool

	// IsHypervisor reports whether the peer's PK is one of the
	// visor's configured hypervisors. Policies typically want to
	// dial hypervisors with minimum hops + maximum mux for low-
	// latency control-plane traffic.
	IsHypervisor(pk string) bool
}

// NopProvider returns a Provider that knows nothing — Geo
// returns "??", Latency 0, Kind "", IsTrusted/IsHypervisor false.
// Used as the default when no Provider is wired up, so policies
// that don't depend on visor state still work.
func NopProvider() Provider { return nopProvider{} }

type nopProvider struct{}

func (nopProvider) Geo(string) string        { return "??" }
func (nopProvider) Latency(string) int       { return 0 }
func (nopProvider) Kind(string) string       { return "" }
func (nopProvider) IsTrusted(string) bool    { return false }
func (nopProvider) IsHypervisor(string) bool { return false }

// FakeProvider is a test helper. Construct via NewFakeProvider
// with the visor state the test wants to simulate.
type FakeProvider struct {
	mu          sync.RWMutex
	geo         map[string]string
	latency     map[string]int
	kind        map[string]string
	trusted     map[string]bool
	hypervisors map[string]bool
}

// NewFakeProvider returns an empty FakeProvider. Tests call the
// Set* methods to populate it before passing to the Evaluator.
func NewFakeProvider() *FakeProvider {
	return &FakeProvider{
		geo:         map[string]string{},
		latency:     map[string]int{},
		kind:        map[string]string{},
		trusted:     map[string]bool{},
		hypervisors: map[string]bool{},
	}
}

// SetGeo sets the country code for a peer PK.
func (p *FakeProvider) SetGeo(pk, country string) *FakeProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.geo[pk] = country
	return p
}

// SetLatency sets the cached latency for a peer PK.
func (p *FakeProvider) SetLatency(pk string, ms int) *FakeProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.latency[pk] = ms
	return p
}

// SetKind sets the transport family for a peer PK.
func (p *FakeProvider) SetKind(pk, kind string) *FakeProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.kind[pk] = kind
	return p
}

// SetTrusted marks a peer as trusted.
func (p *FakeProvider) SetTrusted(pk string) *FakeProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.trusted[pk] = true
	return p
}

// SetHypervisor marks a peer as a configured hypervisor.
func (p *FakeProvider) SetHypervisor(pk string) *FakeProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hypervisors[pk] = true
	return p
}

// Geo implements Provider.
func (p *FakeProvider) Geo(pk string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if v, ok := p.geo[pk]; ok {
		return v
	}
	return "??"
}

// Latency implements Provider.
func (p *FakeProvider) Latency(pk string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.latency[pk]
}

// Kind implements Provider.
func (p *FakeProvider) Kind(pk string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.kind[pk]
}

// IsTrusted implements Provider.
func (p *FakeProvider) IsTrusted(pk string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.trusted[pk]
}

// IsHypervisor implements Provider.
func (p *FakeProvider) IsHypervisor(pk string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.hypervisors[pk]
}
