package dmsg

import (
	"sort"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// defaultPortHitCap bounds the no-listener hit tracker so a scanning peer can't
// grow it without limit. On overflow the least-recently-seen entry is evicted.
const defaultPortHitCap = 256

// PortHit summarizes inbound stream requests from a peer to a local port that
// had NO listener (dmsg error 306, ErrReqNoListener) — i.e. someone tried to
// reach a port this client isn't serving. Useful for spotting would-be
// connectors (e.g. managed visors hitting a disabled hypervisor port),
// misconfigured peers, or port scans.
type PortHit struct {
	SrcPK     cipher.PubKey `json:"src_pk"`
	DstPort   uint16        `json:"dst_port"`
	Count     uint64        `json:"count"`
	FirstSeen time.Time     `json:"first_seen"`
	LastSeen  time.Time     `json:"last_seen"`

	// seq is a monotonic recency counter, bumped on every record. It gives a
	// deterministic most-recent-first ordering (and eviction) independent of
	// the wall clock — two hits recorded within the same coarse-clock tick
	// (common on Windows' ~15ms timer) would otherwise share a LastSeen and
	// sort arbitrarily. Unexported so it stays out of the JSON.
	seq uint64
}

type portHitKey struct {
	pk   cipher.PubKey
	port uint16
}

// portHitTracker is a bounded, mutex-guarded set of no-listener PortHits keyed
// by (src PK, dst port).
type portHitTracker struct {
	mu   sync.Mutex
	hits map[portHitKey]*PortHit
	cap  int
	seq  uint64 // monotonic recency counter (see PortHit.seq)
}

func newPortHitTracker(capN int) *portHitTracker {
	if capN <= 0 {
		capN = defaultPortHitCap
	}
	return &portHitTracker{hits: make(map[portHitKey]*PortHit), cap: capN}
}

// record notes one no-listener hit from srcPK to dstPort. Nil-safe.
func (t *portHitTracker) record(srcPK cipher.PubKey, dstPort uint16) {
	if t == nil {
		return
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	k := portHitKey{pk: srcPK, port: dstPort}
	if h, ok := t.hits[k]; ok {
		h.Count++
		h.LastSeen = now
		h.seq = t.seq
		return
	}
	if len(t.hits) >= t.cap {
		t.evictOldestLocked()
	}
	t.hits[k] = &PortHit{SrcPK: srcPK, DstPort: dstPort, Count: 1, FirstSeen: now, LastSeen: now, seq: t.seq}
}

// evictOldestLocked removes the least-recently-seen entry. Caller holds mu.
func (t *portHitTracker) evictOldestLocked() {
	var oldestK portHitKey
	var oldestSeq uint64
	first := true
	for k, h := range t.hits {
		if first || h.seq < oldestSeq {
			oldestK, oldestSeq, first = k, h.seq, false
		}
	}
	if !first {
		delete(t.hits, oldestK)
	}
}

// snapshot returns the current hits most-recent-first. Ordering is by the
// monotonic seq counter (not LastSeen) so it's deterministic even when several
// hits share a coarse-clock LastSeen. Nil-safe.
func (t *portHitTracker) snapshot() []PortHit {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	out := make([]PortHit, 0, len(t.hits))
	for _, h := range t.hits {
		out = append(out, *h)
	}
	t.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].seq > out[j].seq })
	return out
}
