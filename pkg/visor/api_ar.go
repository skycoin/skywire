package visor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
)

// CheckAREntry checks if a public key is registered in the address
// resolver. Returns the transport types the visor is registered for
// (e.g., ["stcpr", "sudph"]) without revealing its IP address.
func (v *Visor) CheckAREntry(pk string) ([]string, error) {
	if v.tpM == nil {
		return nil, fmt.Errorf("transport manager not running")
	}

	var pubKey cipher.PubKey
	if err := pubKey.Set(pk); err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}

	var types []string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	arClient := v.tpM.ARClient()
	if arClient == nil {
		return nil, fmt.Errorf("address resolver client not available")
	}

	// Try each transport type — only report whether the entry exists,
	// not the resolved address (privacy).
	for _, tpType := range []string{"stcpr", "sudph"} {
		_, err := arClient.Resolve(ctx, tpType, pubKey)
		if err == nil {
			types = append(types, tpType)
		}
	}

	return types, nil
}

// ARSelfEntry is one transport-type-row of this visor's own AR registration:
// the IP:port it is reachable on according to the address-resolver.
//
// RemoteAddrV6 is the optional IPv6 counterpart of RemoteAddr, populated
// when this visor has bound the AR over an IPv6 HTTP client (#2719's
// secondary bind). Empty for v4-only deployments and on older AR servers
// that don't capture family — same backward-compat shape as the underlying
// addrresolver.VisorData (#2715).
type ARSelfEntry struct {
	Type         string   `json:"type"`                     // "stcpr" or "sudph"
	RemoteAddr   string   `json:"remote_addr,omitempty"`    // public IPv4 as AR sees the visor
	RemoteAddrV6 string   `json:"remote_addr_v6,omitempty"` // public IPv6 as AR sees the visor (#1525 Phase 4a)
	Port         string   `json:"port,omitempty"`           // listen port
	Addresses    []string `json:"addresses,omitempty"`      // local interface addresses the visor advertised
}

// ARSelfRegistration is the visor's own AR record across transport types.
// One ARSelfEntry per transport the visor is currently registered for.
// Empty when the visor has not (yet) bound any transport with AR.
type ARSelfRegistration struct {
	Entries []ARSelfEntry `json:"entries,omitempty"`
}

// arSelfState caches the visor's own AR-side bind state, populated by a
// refresh loop that calls Resolve(self) periodically.
//
// The cache lets ARSelfInfo() answer the CLI without a fresh AR HTTP
// round-trip, and gives the DHT publisher a stable source for self-
// publishing under salts addr:stcpr / addr:sudph (dual-publish: the
// AR-side mirror also writes the same target signed by AR's PK; older
// visors that don't self-publish are covered by the AR mirror).
type arSelfState struct {
	mu      sync.RWMutex
	entries map[string]addrresolver.VisorData // key: "stcpr" | "sudph"
}

func newARSelfState() arSelfState {
	return arSelfState{entries: make(map[string]addrresolver.VisorData)}
}

func (s *arSelfState) get(tpType string) (addrresolver.VisorData, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.entries[tpType]
	return d, ok
}

func (s *arSelfState) set(tpType string, d addrresolver.VisorData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[tpType] = d
}

// clear removes the cached AR self-registration for one transport
// type. Currently unreferenced in-tree but kept on the type for
// symmetry with set() and to give callers a way to invalidate a
// stale entry on tear-down.
func (s *arSelfState) clear(tpType string) { //nolint:unused
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, tpType)
}

// snapshot returns a copy of the AR self-registration map. Currently
// unreferenced in-tree; intended for diagnostic dumps that walk all
// configured transport types at once.
func (s *arSelfState) snapshot() map[string]addrresolver.VisorData { //nolint:unused
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]addrresolver.VisorData, len(s.entries))
	for k, v := range s.entries {
		out[k] = v
	}
	return out
}

// ARSelfInfo returns the visor's own cached AR registration. Reads the
// in-memory cache populated by the refresh loop — no HTTP round-trip.
func (v *Visor) ARSelfInfo() (*ARSelfRegistration, error) {
	out := &ARSelfRegistration{}
	for _, tpType := range []string{"stcpr", "sudph"} {
		d, ok := v.arSelf.get(tpType)
		if !ok {
			continue
		}
		out.Entries = append(out.Entries, ARSelfEntry{
			Type:         tpType,
			RemoteAddr:   d.RemoteAddr,
			RemoteAddrV6: d.RemoteAddrV6,
			Port:         d.Port,
			Addresses:    append([]string(nil), d.Addresses...),
		})
	}
	return out, nil
}

// arSelfRefreshLoop periodically reconciles the visor's local AR-self
// cache with what the AR HTTP service reports for our own PK. AR's
// own RedisMirror is the canonical replication channel; the visor
// just needs the local cache so the CLI display and any callers
// asking for our public addresses get a recent answer.
//
// The first refresh runs after a short warmup, then every refreshEvery.
func (v *Visor) arSelfRefreshLoop(ctx context.Context, log *logging.Logger) {
	const (
		warmup       = 15 * time.Second
		refreshEvery = 60 * time.Second
	)

	select {
	case <-ctx.Done():
		return
	case <-time.After(warmup):
	}

	tick := time.NewTicker(refreshEvery)
	defer tick.Stop()

	for {
		v.arSelfRefreshOnce(ctx, log)

		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// arSelfRefreshOnce resolves AR for both transport types and updates
// the local cache. On Resolve failure we keep the previous cache
// entry — a transient AR error shouldn't drop the CLI display.
func (v *Visor) arSelfRefreshOnce(ctx context.Context, _ *logging.Logger) {
	if v.tpM == nil {
		return
	}
	arClient := v.tpM.ARClient()
	if arClient == nil {
		return
	}

	for _, tpType := range []string{"stcpr", "sudph"} {
		rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
		data, err := arClient.Resolve(rctx, tpType, v.conf.PK)
		rcancel()
		if err != nil {
			// Treat "not found" as a transient absence rather than a hard
			// reset: legitimate ARs will report ErrNoEntry for visors that
			// haven't yet bound, and we'd rather hold the last-known value
			// over a single failed poll than flicker the CLI display.
			continue
		}
		v.arSelf.set(tpType, data)
	}
}
