// Package visor pkg/visor/hypervisor_attached_graph.go c3-vis-api
//
// The hypervisor's attached visors report their transports in the Summary it
// polls every hypervisorBackgroundPollInterval. AttachedTransportEntries turns
// that cache into transport entries, so route calculation on the hypervisor's
// own visor can use those edges without a TPD or route-finder query (#4750).
package visor

import (
	"crypto/sha256"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// attachedGraphStaleAfter drops a visor whose last summary is older than two
// poll rounds: it is offline or its RPC conn is being redialed, and its
// transports may be gone.
const attachedGraphStaleAfter = 2 * hypervisorBackgroundPollInterval

// AttachedTransportEntries is the hypervisor's LocalGraphSource: the
// transports of every attached visor seen within attachedGraphStaleAfter,
// setup transports excluded, deduplicated by ID (both ends of an edge between
// two attached visors report it). The returned time advances only when the
// set of IDs changes.
func (hv *Hypervisor) AttachedTransportEntries() ([]*transport.Entry, time.Time) {
	hv.summaryCacheMx.RLock()
	cache := make(map[cipher.PubKey]cachedSummary, len(hv.summaryCache))
	for pk, cs := range hv.summaryCache {
		cache[pk] = cs
	}
	hv.summaryCacheMx.RUnlock()

	entries, sum := attachedEntriesFromSummaries(cache, time.Now())

	hv.attachedGraphMu.Lock()
	if sum != hv.attachedGraphHash {
		hv.attachedGraphHash = sum
		hv.attachedGraphAt = time.Now()
	}
	at := hv.attachedGraphAt
	hv.attachedGraphMu.Unlock()
	return entries, at
}

// attachedEntriesFromSummaries builds the entries from a summary cache
// snapshot and returns them sorted by ID with a hash of the ID set.
func attachedEntriesFromSummaries(cache map[cipher.PubKey]cachedSummary, now time.Time) ([]*transport.Entry, [32]byte) {
	byID := make(map[uuid.UUID]*transport.Entry)
	for _, cs := range cache {
		if cs.sum == nil || cs.sum.Overview == nil || now.Sub(cs.seenAt) > attachedGraphStaleAfter {
			continue
		}
		for _, ts := range cs.sum.Overview.Transports {
			if ts == nil || ts.IsSetup || ts.Remote == (cipher.PubKey{}) || ts.Local == (cipher.PubKey{}) {
				continue
			}
			id := ts.ID
			if id == (uuid.UUID{}) {
				id = transport.MakeTransportID(ts.Local, ts.Remote, ts.Type)
			}
			if have, ok := byID[id]; ok {
				// The other end already reported it; keep the better metric.
				if have.Latency == 0 && ts.LatencyMS > 0 {
					have.Latency = ts.LatencyMS
				}
				if ts.ThroughputBps > have.ThroughputBps {
					have.ThroughputBps = ts.ThroughputBps
				}
				continue
			}
			e := transport.MakeEntry(ts.Local, ts.Remote, ts.Type, ts.Label)
			e.ID = id
			e.Latency = ts.LatencyMS
			e.ThroughputBps = ts.ThroughputBps
			byID[id] = &e
		}
	}
	entries := make([]*transport.Entry, 0, len(byID))
	for _, e := range byID {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ID.String() < entries[j].ID.String()
	})
	h := sha256.New()
	for _, e := range entries {
		h.Write(e.ID[:])
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return entries, sum
}
