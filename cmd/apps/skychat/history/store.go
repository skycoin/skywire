// Package history bolt-backed skychat persistence.
package history

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// BoltStore persists skychat messages to a local BoltDB file.
//
// Schema: one top-level bucket "messages" with nested buckets keyed by peer
// PK hex. Within each peer bucket, keys are big-endian 8-byte timestamps
// (nanoseconds since epoch) and values are JSON-encoded Message. This lets
// us iterate per-peer in chronological order cheaply and evict oldest first.
type BoltStore struct {
	db     *bolt.DB
	limits Limits

	// rate limiting state: per-peer token bucket approximation.
	// We track the count of messages accepted in the current minute window
	// for each peer. Simple and bounded.
	rateMu       sync.Mutex
	rateWindow   time.Time            // start of current 1-minute window
	rateCount    map[string]int       // peer -> count in current window
	peerLastSeen map[string]time.Time // for GC of rateCount map

	// background sweep
	done chan struct{}
	wg   sync.WaitGroup
}

const (
	messagesBucket = "messages"
	// groupsBucket is the parallel top-level bucket for group-chat
	// messages. Nested buckets keyed by group ID (string), keys are
	// big-endian 8-byte timestamps, values JSON GroupMessage. Kept
	// schema-separate from 1:1 messages so a query like ListByPeer
	// can't accidentally surface group traffic and vice versa.
	groupsBucket = "groups"
)

// NewBoltStore opens (or creates) a BoltDB file at path and returns a Store.
// The parent directory is created if it doesn't exist.
func NewBoltStore(path string, limits Limits) (*BoltStore, error) {
	if err := limits.Validate(); err != nil {
		return nil, fmt.Errorf("invalid limits: %w", err)
	}

	// Ensure parent dir exists.
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open bolt: %w", err)
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		if _, e := tx.CreateBucketIfNotExists([]byte(messagesBucket)); e != nil {
			return e
		}
		_, e := tx.CreateBucketIfNotExists([]byte(groupsBucket))
		return e
	}); err != nil {
		_ = db.Close() //nolint:errcheck
		return nil, fmt.Errorf("init bucket: %w", err)
	}

	s := &BoltStore{
		db:           db,
		limits:       limits,
		rateWindow:   time.Now().UTC().Truncate(time.Minute),
		rateCount:    make(map[string]int),
		peerLastSeen: make(map[string]time.Time),
		done:         make(chan struct{}),
	}

	if limits.TTL > 0 {
		s.wg.Add(1)
		go s.sweepLoop()
	}
	return s, nil
}

// Append implements Store.
func (s *BoltStore) Append(msg Message) error {
	if msg.Peer == "" {
		return ErrEmptyPeer
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}

	if s.limits.MaxMessageSize > 0 && len(msg.Text) > s.limits.MaxMessageSize {
		return ErrTooLarge
	}

	if s.limits.WhitelistOnly {
		if !s.limits.Whitelist[msg.Peer] {
			return ErrNotWhitelisted
		}
	}

	if !s.acceptRate(msg.Peer) {
		return ErrRateLimited
	}

	// Total storage cap check. bolt.DB.Stats().TxStats doesn't give file size
	// directly, so we stat the file. Cheap, called on each write.
	if s.limits.TotalCapBytes > 0 {
		if fi, err := os.Stat(s.db.Path()); err == nil {
			if fi.Size() >= s.limits.TotalCapBytes {
				return ErrStorageFull
			}
		}
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket([]byte(messagesBucket))
		peerBkt, err := root.CreateBucketIfNotExists([]byte(msg.Peer))
		if err != nil {
			return err
		}

		// Key = big-endian nanoseconds. Resolve collisions by incrementing.
		key := tsKey(msg.Timestamp)
		for peerBkt.Get(key) != nil {
			key = nextKey(key)
		}
		if err := peerBkt.Put(key, raw); err != nil {
			return err
		}

		// Enforce per-peer cap via FIFO eviction of oldest.
		if s.limits.PerPeerCap > 0 {
			if err := evictOldest(peerBkt, s.limits.PerPeerCap); err != nil {
				return err
			}
		}
		return nil
	})
}

// acceptRate is the simple per-minute token bucket check.
// Returns true if this message is within the per-peer rate for the current
// 1-minute window.
func (s *BoltStore) acceptRate(peer string) bool {
	if s.limits.PerPeerRatePerMin == 0 {
		return true
	}
	s.rateMu.Lock()
	defer s.rateMu.Unlock()

	now := time.Now().UTC()
	currentWindow := now.Truncate(time.Minute)
	if currentWindow.After(s.rateWindow) {
		// Rolled over to a new minute — reset counts.
		s.rateWindow = currentWindow
		s.rateCount = make(map[string]int, len(s.rateCount))
	}

	s.peerLastSeen[peer] = now
	// Opportunistic GC of stale rate tracking state.
	if len(s.peerLastSeen) > 1024 {
		cutoff := now.Add(-10 * time.Minute)
		for p, t := range s.peerLastSeen {
			if t.Before(cutoff) {
				delete(s.peerLastSeen, p)
			}
		}
	}

	if s.rateCount[peer] >= s.limits.PerPeerRatePerMin {
		return false
	}
	s.rateCount[peer]++
	return true
}

// ListByPeer implements Store.
func (s *BoltStore) ListByPeer(peer string, limit int) ([]Message, error) {
	if peer == "" {
		return nil, ErrEmptyPeer
	}
	var out []Message
	err := s.db.View(func(tx *bolt.Tx) error {
		root := tx.Bucket([]byte(messagesBucket))
		peerBkt := root.Bucket([]byte(peer))
		if peerBkt == nil {
			return nil
		}
		return iterateForward(peerBkt, limit, &out)
	})
	return out, err
}

// ListRecent implements Store. Walks every peer bucket and merges by timestamp.
// For typical personal-use sizes (hundreds of peers, thousands of messages)
// this is fast enough. For larger datasets, consider a global timestamp index.
func (s *BoltStore) ListRecent(limit int) ([]Message, error) {
	var all []Message
	err := s.db.View(func(tx *bolt.Tx) error {
		root := tx.Bucket([]byte(messagesBucket))
		return root.ForEachBucket(func(k []byte) error {
			peerBkt := root.Bucket(k)
			if peerBkt == nil {
				return nil
			}
			return iterateForward(peerBkt, 0, &all)
		})
	})
	if err != nil {
		return nil, err
	}
	// Sort by timestamp ascending (newest last).
	sortByTimestamp(all)
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all, nil
}

// Peers implements Store.
func (s *BoltStore) Peers() ([]string, error) {
	var peers []string
	err := s.db.View(func(tx *bolt.Tx) error {
		root := tx.Bucket([]byte(messagesBucket))
		return root.ForEachBucket(func(k []byte) error {
			peers = append(peers, string(k))
			return nil
		})
	})
	return peers, err
}

// AppendGroup implements Store. Same limit semantics as Append, but the
// rate-limit / whitelist key is the GroupID instead of a peer PK. Groups
// are typically larger than 1:1 traffic (N senders fan in) so the
// per-key cap is the most important guardrail here — TotalCapBytes
// still applies globally across both 1:1 and group buckets.
func (s *BoltStore) AppendGroup(msg GroupMessage) error {
	if msg.GroupID == "" {
		return ErrEmptyPeer
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}

	if s.limits.MaxMessageSize > 0 && len(msg.Text) > s.limits.MaxMessageSize {
		return ErrTooLarge
	}

	if s.limits.WhitelistOnly {
		if !s.limits.Whitelist[msg.GroupID] {
			return ErrNotWhitelisted
		}
	}

	if !s.acceptRate(msg.GroupID) {
		return ErrRateLimited
	}

	if s.limits.TotalCapBytes > 0 {
		if fi, err := os.Stat(s.db.Path()); err == nil {
			if fi.Size() >= s.limits.TotalCapBytes {
				return ErrStorageFull
			}
		}
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal group message: %w", err)
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket([]byte(groupsBucket))
		gBkt, err := root.CreateBucketIfNotExists([]byte(msg.GroupID))
		if err != nil {
			return err
		}

		key := tsKey(msg.Timestamp)
		for gBkt.Get(key) != nil {
			key = nextKey(key)
		}
		if err := gBkt.Put(key, raw); err != nil {
			return err
		}

		if s.limits.PerPeerCap > 0 {
			if err := evictOldest(gBkt, s.limits.PerPeerCap); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListByGroup implements Store. Returns up to limit most recent
// messages for the named group, newest last. limit <= 0 returns all.
// Empty groupID returns an empty slice (rather than scanning every
// group; callers that want recent-across-groups should iterate Groups()
// + per-group ListByGroup).
func (s *BoltStore) ListByGroup(groupID string, limit int) ([]GroupMessage, error) {
	if groupID == "" {
		return nil, nil
	}
	var msgs []GroupMessage
	err := s.db.View(func(tx *bolt.Tx) error {
		root := tx.Bucket([]byte(groupsBucket))
		gBkt := root.Bucket([]byte(groupID))
		if gBkt == nil {
			return nil
		}
		return iterateGroupForward(gBkt, limit, func(m GroupMessage) error {
			msgs = append(msgs, m)
			return nil
		})
	})
	return msgs, err
}

// ListGroupSince implements Store. The bolt bucket is keyed by
// tsKey(Timestamp), so we Seek to since's tsKey and walk forward —
// O(messages-since) instead of O(all-messages-in-group). Returns
// nil for an unknown group; an empty (non-nil error) result for a
// group with messages but none newer than `since`.
//
// The Seek key is the exclusive boundary: messages with
// Timestamp.Equal(since) are NOT included (since they're already
// known to the caller — that's the cursor they passed). Anything
// strictly newer surfaces. Matches the inbox snapshotAfter's TS.After
// semantics.
func (s *BoltStore) ListGroupSince(groupID string, since time.Time) ([]GroupMessage, error) {
	if groupID == "" {
		return nil, nil
	}
	var msgs []GroupMessage
	err := s.db.View(func(tx *bolt.Tx) error {
		root := tx.Bucket([]byte(groupsBucket))
		gBkt := root.Bucket([]byte(groupID))
		if gBkt == nil {
			return nil
		}
		cur := gBkt.Cursor()
		var k, v []byte
		if since.IsZero() || since.UnixNano() < 0 {
			// "From the beginning" mode. uint64(negative-nano) wraps
			// to a huge value whose tsKey sorts AFTER every real key,
			// which would silently return zero results — so the seek
			// path skipped here and we just walk from the first key.
			k, v = cur.First()
		} else {
			// Seek to the smallest key strictly greater than tsKey(since).
			// nextKey is the +1 byte-increment of tsKey(since); a
			// cursor.Seek to it lands on the first key with TS > since
			// (bolt keys are byte-lex ordered and tsKey is monotonic
			// in time across the positive-Unix-nano range).
			boundary := nextKey(tsKey(since))
			k, v = cur.Seek(boundary)
		}
		for ; k != nil; k, v = cur.Next() {
			var m GroupMessage
			if err := json.Unmarshal(v, &m); err != nil {
				continue
			}
			msgs = append(msgs, m)
		}
		return nil
	})
	return msgs, err
}

// Groups implements Store. Returns the set of group IDs that have any
// stored messages — symmetric with Peers() for the 1:1 store.
func (s *BoltStore) Groups() ([]string, error) {
	var groups []string
	err := s.db.View(func(tx *bolt.Tx) error {
		root := tx.Bucket([]byte(groupsBucket))
		return root.ForEachBucket(func(k []byte) error {
			groups = append(groups, string(k))
			return nil
		})
	})
	return groups, err
}

// iterateGroupForward mirrors iterateForward's tail-walk-then-reverse
// pattern but decodes GroupMessage records. The two functions could be
// merged with generics; kept parallel here for symmetry with the
// existing 1:1 path and so callers can pass a typed callback.
func iterateGroupForward(bkt *bolt.Bucket, limit int, fn func(GroupMessage) error) error {
	if limit > 0 {
		cur := bkt.Cursor()
		var collected []GroupMessage
		for k, v := cur.Last(); k != nil; k, v = cur.Prev() {
			var m GroupMessage
			if err := json.Unmarshal(v, &m); err != nil {
				continue
			}
			collected = append(collected, m)
			if len(collected) >= limit {
				break
			}
		}
		// Reverse so newest is last.
		for i, j := 0, len(collected)-1; i < j; i, j = i+1, j-1 {
			collected[i], collected[j] = collected[j], collected[i]
		}
		for _, m := range collected {
			if err := fn(m); err != nil {
				return err
			}
		}
		return nil
	}
	return bkt.ForEach(func(_, v []byte) error {
		var m GroupMessage
		if err := json.Unmarshal(v, &m); err != nil {
			return nil // skip unparseable
		}
		return fn(m)
	})
}

// Close implements Store.
func (s *BoltStore) Close() error {
	close(s.done)
	s.wg.Wait()
	return s.db.Close()
}

func (s *BoltStore) sweepLoop() {
	defer s.wg.Done()

	// Run every 10 minutes or at TTL/10, whichever is smaller.
	interval := 10 * time.Minute
	if s.limits.TTL/10 < interval {
		interval = s.limits.TTL / 10
		if interval < time.Minute {
			interval = time.Minute
		}
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.sweep(context.Background())
		}
	}
}

// sweep removes messages older than the TTL.
func (s *BoltStore) sweep(_ context.Context) {
	cutoff := time.Now().UTC().Add(-s.limits.TTL)
	cutoffKey := tsKey(cutoff)

	_ = s.db.Update(func(tx *bolt.Tx) error { //nolint:errcheck
		root := tx.Bucket([]byte(messagesBucket))
		return root.ForEachBucket(func(k []byte) error {
			peerBkt := root.Bucket(k)
			if peerBkt == nil {
				return nil
			}
			cur := peerBkt.Cursor()
			var expired [][]byte
			for kk, _ := cur.First(); kk != nil && lessOrEqual(kk, cutoffKey); kk, _ = cur.Next() {
				// copy because cursor-returned slices are invalid after Delete
				kc := make([]byte, len(kk))
				copy(kc, kk)
				expired = append(expired, kc)
			}
			for _, kk := range expired {
				if err := peerBkt.Delete(kk); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

// --- helpers -----------------------------------------------------------------

func tsKey(t time.Time) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(t.UTC().UnixNano())) //nolint:gosec
	return b[:]
}

func nextKey(k []byte) []byte {
	out := make([]byte, len(k))
	copy(out, k)
	for i := len(out) - 1; i >= 0; i-- {
		out[i]++
		if out[i] != 0 {
			break
		}
	}
	return out
}

func lessOrEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		if a[i] < b[i] {
			return true
		}
		if a[i] > b[i] {
			return false
		}
	}
	return true
}

// iterateForward walks the bucket in ascending key order and decodes each
// value as a Message. If limit > 0, only the most recent `limit` messages
// are returned (the newest come last in the returned slice).
func iterateForward(bkt *bolt.Bucket, limit int, out *[]Message) error {
	// If limit > 0, walk backwards first to collect the newest N then reverse,
	// to avoid loading the whole bucket.
	if limit > 0 {
		cur := bkt.Cursor()
		var collected []Message
		for k, v := cur.Last(); k != nil; k, v = cur.Prev() {
			var m Message
			if err := json.Unmarshal(v, &m); err != nil {
				continue
			}
			collected = append(collected, m)
			if len(collected) >= limit {
				break
			}
		}
		// Reverse so newest is last.
		for i, j := 0, len(collected)-1; i < j; i, j = i+1, j-1 {
			collected[i], collected[j] = collected[j], collected[i]
		}
		*out = append(*out, collected...)
		return nil
	}

	return bkt.ForEach(func(_, v []byte) error {
		var m Message
		if err := json.Unmarshal(v, &m); err != nil {
			return nil // skip unparseable
		}
		*out = append(*out, m)
		return nil
	})
}

// evictOldest removes the oldest messages from the bucket until its count
// is at most cap. Counts keys via cursor walk because Bucket.Stats() is
// per-transaction-cached and not reliable for just-inserted keys within
// the same transaction.
func evictOldest(bkt *bolt.Bucket, cap int) error {
	// Count current keys.
	total := 0
	_ = bkt.ForEach(func(_, _ []byte) error { //nolint:errcheck
		total++
		return nil
	})
	if total <= cap {
		return nil
	}
	toRemove := total - cap
	cur := bkt.Cursor()
	var keys [][]byte
	for k, _ := cur.First(); k != nil && len(keys) < toRemove; k, _ = cur.Next() {
		kc := make([]byte, len(k))
		copy(kc, k)
		keys = append(keys, kc)
	}
	for _, k := range keys {
		if err := bkt.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

func sortByTimestamp(msgs []Message) {
	// Insertion sort — messages are nearly sorted per-peer already.
	for i := 1; i < len(msgs); i++ {
		for j := i; j > 0 && msgs[j-1].Timestamp.After(msgs[j].Timestamp); j-- {
			msgs[j-1], msgs[j] = msgs[j], msgs[j-1]
		}
	}
}
