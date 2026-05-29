// Package node — connmap.go: sharded address- and pubkey-keyed
// connection maps. Replaces the single Node.mx-guarded pendConns +
// addrToConn + pkToConn triple.
//
// Why sharded:
//   Pre-fix every accept/init/removeConn serialized on Node.mx. On a
//   production transport-discovery the visor pile-up reached ~1000
//   goroutines waiting on (*Node).onNewConn + ~400 on (*Node).removeConn
//   while the dmsg listener dropped streams with "accept chan maxed".
//   The critical sections are themselves microsecond-fast; the
//   problem is the single mutex, not the work under it. Sharding by
//   addr/pk decorrelates accepts from different peers so they no
//   longer fight for the same lock.
//
// Shape choice:
//   - addrConnMap holds BOTH pending and active entries in the same
//     shard. onNewConn's three-way check (active? pending? new?)
//     becomes one lock acquire instead of two.
//   - pkConnMap is a sibling sharded map for peer-pk lookups, used
//     by hasPeer / onConnInit / removeConn. Separate because pk is
//     not derivable from addr at accept time.
//   - Atomic counters (pendCount, activeCount) back pendingLen /
//     activeLen, so cap checks (connCap, pendingConnCap) and Stats
//     don't touch any shard mutex.

package node

import (
	"sync"
	"sync/atomic"

	"github.com/skycoin/skycoin/src/cipher"
)

// connMapShards: count of shards for both connection maps. Picked 32
// (rather than NumCPU) because the contention sources are per-peer
// dmsg accepts — scales with deployment topology (hundreds to
// thousands of peers), not with local CPU count. 32 absorbs the
// observed ~1000-waiter pile-up without overdoing memory for the
// low-peer case. Power of two so the modulo lowers to a mask.
const connMapShards = 32

// addrShardFor reduces an address string to a shard index via a
// tiny djb2 hash. Inline, alloc-free — onNewConn runs in the accept
// hot path; the previous fnv.New32a()+Write+Sum32 added a heap
// allocation per accept which the contention profile already
// flagged via chacha20poly1305.Seal allocations downstream.
func addrShardIndex(addr string) uint32 {
	var h uint32 = 5381
	for i := 0; i < len(addr); i++ {
		h = h*33 + uint32(addr[i])
	}
	return h & (connMapShards - 1)
}

// pkShardIndex reads the first 4 bytes of the pubkey as a uniform
// random uint32. Pubkeys are uniformly random by construction, so
// no hashing is needed.
func pkShardIndex(pk cipher.PubKey) uint32 {
	return (uint32(pk[0])<<24 | uint32(pk[1])<<16 | uint32(pk[2])<<8 | uint32(pk[3])) & (connMapShards - 1)
}

// addrConnShard holds pending + active conns for one shard. Both
// maps share the same mutex so onNewConn's lookup-then-insert
// across the two maps is one lock acquire per shard.
type addrConnShard struct {
	mu      sync.Mutex
	pending map[string]*Conn
	active  map[string]*Conn
}

// addrConnMap replaces Node.pendConns + Node.addrToConn. Sharded by
// remote address; per-shard mutex; atomic counters for cap reads.
type addrConnMap struct {
	shards      [connMapShards]addrConnShard
	pendCount   atomic.Int64
	activeCount atomic.Int64
}

func newAddrConnMap() *addrConnMap {
	m := &addrConnMap{}
	for i := range m.shards {
		m.shards[i].pending = make(map[string]*Conn)
		m.shards[i].active = make(map[string]*Conn)
	}
	return m
}

func (m *addrConnMap) shardFor(addr string) *addrConnShard {
	return &m.shards[addrShardIndex(addr)]
}

// reservePending is the atomic combo onNewConn used to do under
// Node.mx: check the address against both maps and, if neither
// holds it, insert c into pending. Returns the existing entry on
// hit (with isPending reflecting which map it came from), or
// isNew=true on a fresh insert. Single shard mutex acquisition.
//
// capCheck (when non-nil) runs under the shard lock just before
// the insert and may veto with an error — used by onNewConn to
// enforce MaxConnections / MaxPendingConnections without a separate
// lock acquire. The cap reads use the atomic counters so capCheck
// itself is lock-free.
func (m *addrConnMap) reservePending(addr string, c *Conn, capCheck func() error) (existing *Conn, isPending, isNew bool, err error) {
	s := m.shardFor(addr)
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.active[addr]; ok {
		return e, false, false, nil
	}
	if e, ok := s.pending[addr]; ok {
		return e, true, false, nil
	}
	if capCheck != nil {
		if err := capCheck(); err != nil {
			return nil, false, false, err
		}
	}
	s.pending[addr] = c
	m.pendCount.Add(1)
	return nil, false, true, nil
}

// promote moves the pending entry for addr to active, but only if
// the slot currently holds c. The identity check guards against a
// late promote of an evicted Conn overwriting a live replacement.
func (m *addrConnMap) promote(addr string, c *Conn) {
	s := m.shardFor(addr)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.pending[addr]; ok && existing == c {
		delete(s.pending, addr)
		m.pendCount.Add(-1)
	}
	if _, ok := s.active[addr]; !ok {
		m.activeCount.Add(1)
	}
	s.active[addr] = c
}

// removePending deletes the pending entry for addr if it points at
// c. Identity-checked — same reason as promote.
func (m *addrConnMap) removePending(addr string, c *Conn) {
	s := m.shardFor(addr)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.pending[addr]; ok && existing == c {
		delete(s.pending, addr)
		m.pendCount.Add(-1)
	}
}

// removeActive deletes the active entry for addr if it points at c.
// Identity check preserves the pre-shard removeConn semantics: a
// late remove from an evicted Conn must not wipe the slot now
// pointing at the live replacement Conn (see node.go removeConn
// comment).
func (m *addrConnMap) removeActive(addr string, c *Conn) {
	s := m.shardFor(addr)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.active[addr]; ok && existing == c {
		delete(s.active, addr)
		m.activeCount.Add(-1)
	}
}

func (m *addrConnMap) pendingLen() int { return int(m.pendCount.Load()) }
func (m *addrConnMap) activeLen() int  { return int(m.activeCount.Load()) }

// rangeActive calls fn for each active Conn. Snapshots each shard
// under its own lock then invokes fn outside the lock so a slow
// callback can't stall accepts on the same shard.
func (m *addrConnMap) rangeActive(fn func(*Conn)) {
	for i := range m.shards {
		s := &m.shards[i]
		s.mu.Lock()
		snap := make([]*Conn, 0, len(s.active))
		for _, c := range s.active {
			snap = append(snap, c)
		}
		s.mu.Unlock()
		for _, c := range snap {
			fn(c)
		}
	}
}

// closePending closes every pending Conn. Used by Node.Close.
func (m *addrConnMap) closePending() {
	for i := range m.shards {
		s := &m.shards[i]
		s.mu.Lock()
		snap := make([]*Conn, 0, len(s.pending))
		for _, c := range s.pending {
			snap = append(snap, c)
		}
		s.mu.Unlock()
		for _, c := range snap {
			c.Close() //nolint:errcheck,gosec
		}
	}
}

// pkConnShard mirrors addrConnShard but keyed by cipher.PubKey.
type pkConnShard struct {
	mu sync.Mutex
	m  map[cipher.PubKey]*Conn
}

// pkConnMap replaces Node.pkToConn. Sharded by peer pubkey.
type pkConnMap struct {
	shards [connMapShards]pkConnShard
}

func newPkConnMap() *pkConnMap {
	m := &pkConnMap{}
	for i := range m.shards {
		m.shards[i].m = make(map[cipher.PubKey]*Conn)
	}
	return m
}

func (m *pkConnMap) shardFor(pk cipher.PubKey) *pkConnShard {
	return &m.shards[pkShardIndex(pk)]
}

func (m *pkConnMap) set(pk cipher.PubKey, c *Conn) {
	s := m.shardFor(pk)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[pk] = c
}

func (m *pkConnMap) get(pk cipher.PubKey) (*Conn, bool) {
	s := m.shardFor(pk)
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.m[pk]
	return c, ok
}

// remove deletes pk → c if the slot currently holds c. Identity-
// checked for the same evict-then-replace race as addrConnMap.
func (m *pkConnMap) remove(pk cipher.PubKey, c *Conn) {
	s := m.shardFor(pk)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.m[pk]; ok && existing == c {
		delete(s.m, pk)
	}
}

// closeAll closes every Conn in the map. Used by Node.Close.
func (m *pkConnMap) closeAll() {
	for i := range m.shards {
		s := &m.shards[i]
		s.mu.Lock()
		snap := make([]*Conn, 0, len(s.m))
		for _, c := range s.m {
			snap = append(snap, c)
		}
		s.mu.Unlock()
		for _, c := range snap {
			c.Close() //nolint:errcheck,gosec
		}
	}
}
