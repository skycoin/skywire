// Package visor pkg/visor/local_graph.go c3-vis-core
//
// A local graph source is a set of transport entries this visor knows about
// beyond its own transport manager and the network-wide TPD snapshot. Today
// the one source is a hypervisor's view of its attached visors
// (Hypervisor.AttachedTransportEntries, #4750): every attached visor reports
// its transports in the summary the hypervisor already polls, so route
// calculation can use those edges without a TPD or route-finder query.
package visor

import (
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// LocalGraphSource returns transport entries and a version: a time that
// advances only when the returned set changes, so a cache keyed on it
// rebuilds exactly then and not otherwise.
type LocalGraphSource func() ([]*transport.Entry, time.Time)

// SetLocalGraphSource installs (or with nil, removes) the local graph source.
func (v *Visor) SetLocalGraphSource(src LocalGraphSource) {
	v.localGraphMu.Lock()
	v.localGraph = src
	v.localGraphMu.Unlock()
}

// localGraphEntries returns the source's entries and version, or nil and a
// zero time when no source is installed.
func (v *Visor) localGraphEntries() ([]*transport.Entry, time.Time) {
	if v == nil {
		return nil, time.Time{}
	}
	v.localGraphMu.RLock()
	src := v.localGraph
	v.localGraphMu.RUnlock()
	if src == nil {
		return nil, time.Time{}
	}
	return src()
}

// localGraphHasPeer reports whether pk is an edge of any local-graph entry —
// the router's cue to try a local route before asking the route finder.
func (v *Visor) localGraphHasPeer(pk cipher.PubKey) bool {
	entries, _ := v.localGraphEntries()
	for _, e := range entries {
		if e != nil && (e.Edges[0] == pk || e.Edges[1] == pk) {
			return true
		}
	}
	return false
}

// mergeTransportEntries appends the extra entries whose IDs are not already
// in base. Base wins on a duplicate: the TPD view carries label and QoS
// metadata a local view may lack.
func mergeTransportEntries(base, extra []*transport.Entry) []*transport.Entry {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[uuid.UUID]struct{}, len(base))
	for _, e := range base {
		if e != nil {
			seen[e.ID] = struct{}{}
		}
	}
	out := base
	for _, e := range extra {
		if e == nil {
			continue
		}
		if _, dup := seen[e.ID]; dup {
			continue
		}
		seen[e.ID] = struct{}{}
		out = append(out, e)
	}
	return out
}

// laterTime returns the later of a and b.
func laterTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
