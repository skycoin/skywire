package dht

import (
	"sort"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// K is the Kademlia replication parameter — max peers per bucket and
// number of closest nodes returned by lookups.
const K = 20

// Peer represents a known DHT node.
type Peer struct {
	ID       NodeID        `json:"id"`
	PK       cipher.PubKey `json:"pk"`
	LastSeen time.Time     `json:"last_seen"`
}

// KBucket holds up to K peers, ordered by last-seen (index 0 = least recent).
type KBucket struct {
	peers []Peer
}

// Len returns the number of peers in the bucket.
func (b *KBucket) Len() int { return len(b.peers) }

// IsFull returns true if the bucket has K peers.
func (b *KBucket) IsFull() bool { return len(b.peers) >= K }

// Peers returns a copy of the peers in the bucket.
func (b *KBucket) Peers() []Peer {
	out := make([]Peer, len(b.peers))
	copy(out, b.peers)
	return out
}

// Contains returns true if the bucket contains a peer with the given ID.
func (b *KBucket) Contains(id NodeID) bool {
	for _, p := range b.peers {
		if p.ID == id {
			return true
		}
	}
	return false
}

// Update adds or refreshes a peer. If the peer exists, it is moved to
// the tail (most recent). If the bucket is full, returns the
// least-recently-seen peer for liveness checking; the caller should ping
// that peer and call Evict+Update if it doesn't respond.
func (b *KBucket) Update(p Peer) (evictCandidate *Peer) {
	p.LastSeen = time.Now()

	// If peer already exists, move to tail.
	for i, existing := range b.peers {
		if existing.ID == p.ID {
			b.peers = append(b.peers[:i], b.peers[i+1:]...)
			b.peers = append(b.peers, p)
			return nil
		}
	}

	// Bucket not full — append.
	if !b.IsFull() {
		b.peers = append(b.peers, p)
		return nil
	}

	// Bucket full — return least-recently-seen for eviction check.
	lrs := b.peers[0]
	return &lrs
}

// Evict removes a peer by ID. Used when a liveness ping fails.
func (b *KBucket) Evict(id NodeID) {
	for i, p := range b.peers {
		if p.ID == id {
			b.peers = append(b.peers[:i], b.peers[i+1:]...)
			return
		}
	}
}

// LeastRecentlySeen returns the peer that hasn't been seen for the longest, or nil.
func (b *KBucket) LeastRecentlySeen() *Peer {
	if len(b.peers) == 0 {
		return nil
	}
	p := b.peers[0]
	return &p
}

// RoutingTable is a Kademlia routing table with 256 k-buckets.
type RoutingTable struct {
	self    NodeID
	buckets [IDSize * 8]KBucket // 256 buckets
	mu      sync.RWMutex
}

// NewRoutingTable creates a routing table for the given local node ID.
func NewRoutingTable(self NodeID) *RoutingTable {
	return &RoutingTable{self: self}
}

// Self returns the local node's ID.
func (rt *RoutingTable) Self() NodeID { return rt.self }

// BucketIndex returns the k-bucket index for a given node ID.
// This is the common prefix length between self and the target.
func (rt *RoutingTable) BucketIndex(id NodeID) int {
	cpl := CommonPrefixLen(rt.self, id)
	if cpl >= IDSize*8 {
		return IDSize*8 - 1
	}
	return cpl
}

// Update adds or refreshes a peer in the appropriate bucket.
// Returns an eviction candidate if the bucket is full (caller should
// ping it and call Evict+Update if unreachable).
func (rt *RoutingTable) Update(p Peer) *Peer {
	if p.ID == rt.self {
		return nil // don't add ourselves
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	idx := rt.BucketIndex(p.ID)
	return rt.buckets[idx].Update(p)
}

// Evict removes a peer from its bucket.
func (rt *RoutingTable) Evict(id NodeID) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	idx := rt.BucketIndex(id)
	rt.buckets[idx].Evict(id)
}

// FindClosest returns up to count peers closest to the target ID,
// sorted by XOR distance (ascending).
func (rt *RoutingTable) FindClosest(target NodeID, count int) []Peer {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	var all []Peer
	for i := range rt.buckets {
		all = append(all, rt.buckets[i].peers...)
	}

	sort.Slice(all, func(i, j int) bool {
		di := Distance(all[i].ID, target)
		dj := Distance(all[j].ID, target)
		return Less(di, dj)
	})

	if len(all) > count {
		all = all[:count]
	}
	return all
}

// Size returns the total number of peers across all buckets.
func (rt *RoutingTable) Size() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	n := 0
	for i := range rt.buckets {
		n += rt.buckets[i].Len()
	}
	return n
}

// AllPeers returns a copy of all peers in the routing table.
func (rt *RoutingTable) AllPeers() []Peer {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var out []Peer
	for i := range rt.buckets {
		out = append(out, rt.buckets[i].peers...)
	}
	return out
}
