// Package history pkg/skychat/history/store_mem.go c4-app-chat
//
// MemStore is a non-durable, in-memory Store. It exists so the history
// package compiles and runs under GOOS=js (the wasm visor) where BoltStore's
// bbolt backend can't build, and so tests / ephemeral deployments have a
// dependency-free Store. It honors the same Limits as BoltStore — per-message
// size, per-peer rate (token bucket), per-peer FIFO cap, total-bytes cap, TTL
// sweep, and whitelist — with one documented difference: TotalCapBytes is
// measured against the accumulated JSON size of stored records rather than an
// on-disk file size, so the numbers won't line up byte-for-byte with a bolt
// file, but the reject-when-full contract is identical.
package history

import (
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// MemStore keeps messages in per-peer and per-group slices guarded by a single
// mutex. Slices are held sorted by Timestamp ascending (newest last) so reads
// and FIFO eviction are trivial. It is safe for concurrent use.
type MemStore struct {
	mu     sync.Mutex
	limits Limits

	byPeer  map[string][]Message
	byGroup map[string][]GroupMessage
	// totalBytes is the running sum of the JSON size of every stored record
	// (1:1 and group), maintained incrementally on append and eviction/sweep.
	totalBytes int64

	// rate limiting state: per-key token bucket in a 1-minute window, keyed
	// by peer PK (1:1) or group ID (group). Mirrors BoltStore.acceptRate.
	rateWindow   time.Time
	rateCount    map[string]int
	peerLastSeen map[string]time.Time

	done   chan struct{}
	closed bool
	wg     sync.WaitGroup
}

// NewMemStore returns an in-memory Store. A non-zero Limits.TTL starts a
// background sweep goroutine (stopped by Close), exactly as BoltStore does.
func NewMemStore(limits Limits) (*MemStore, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	s := &MemStore{
		limits:       limits,
		byPeer:       make(map[string][]Message),
		byGroup:      make(map[string][]GroupMessage),
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

func msgSize(v interface{}) int64 {
	raw, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return int64(len(raw))
}

// Append implements Store.
func (s *MemStore) Append(msg Message) error {
	if msg.Peer == "" {
		return ErrEmptyPeer
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}
	if s.limits.MaxMessageSize > 0 && len(msg.Text) > s.limits.MaxMessageSize {
		return ErrTooLarge
	}
	if s.limits.WhitelistOnly && !s.limits.Whitelist[msg.Peer] {
		return ErrNotWhitelisted
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.acceptRateLocked(msg.Peer) {
		return ErrRateLimited
	}
	if s.limits.TotalCapBytes > 0 && s.totalBytes >= s.limits.TotalCapBytes {
		return ErrStorageFull
	}

	msgs := append(s.byPeer[msg.Peer], msg)
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].Timestamp.Before(msgs[j].Timestamp) })
	s.totalBytes += msgSize(msg)

	// Per-peer FIFO cap: drop oldest (front) beyond the cap.
	if s.limits.PerPeerCap > 0 && len(msgs) > s.limits.PerPeerCap {
		drop := len(msgs) - s.limits.PerPeerCap
		for _, ev := range msgs[:drop] {
			s.totalBytes -= msgSize(ev)
		}
		msgs = append([]Message(nil), msgs[drop:]...)
	}
	s.byPeer[msg.Peer] = msgs
	return nil
}

// acceptRateLocked mirrors BoltStore.acceptRate; caller holds s.mu.
func (s *MemStore) acceptRateLocked(key string) bool {
	if s.limits.PerPeerRatePerMin == 0 {
		return true
	}
	now := time.Now().UTC()
	currentWindow := now.Truncate(time.Minute)
	if currentWindow.After(s.rateWindow) {
		s.rateWindow = currentWindow
		s.rateCount = make(map[string]int, len(s.rateCount))
	}
	s.peerLastSeen[key] = now
	if len(s.peerLastSeen) > 1024 {
		cutoff := now.Add(-10 * time.Minute)
		for p, t := range s.peerLastSeen {
			if t.Before(cutoff) {
				delete(s.peerLastSeen, p)
			}
		}
	}
	if s.rateCount[key] >= s.limits.PerPeerRatePerMin {
		return false
	}
	s.rateCount[key]++
	return true
}

// ListByPeer implements Store.
func (s *MemStore) ListByPeer(peer string, limit int) ([]Message, error) {
	if peer == "" {
		return nil, ErrEmptyPeer
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.byPeer[peer]
	return tailCopy(src, limit), nil
}

// DeleteByID implements Store.
func (s *MemStore) DeleteByID(peer, id string) (bool, error) {
	if peer == "" {
		return false, ErrEmptyPeer
	}
	if id == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.byPeer[peer]
	if len(msgs) == 0 {
		return false, nil
	}
	kept := msgs[:0:0]
	found := false
	for _, m := range msgs {
		if m.ID == id {
			s.totalBytes -= msgSize(m)
			found = true
			continue
		}
		kept = append(kept, m)
	}
	if !found {
		return false, nil
	}
	if len(kept) == 0 {
		delete(s.byPeer, peer)
	} else {
		s.byPeer[peer] = kept
	}
	return true, nil
}

// ListRecent implements Store.
func (s *MemStore) ListRecent(limit int) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []Message
	for _, msgs := range s.byPeer {
		all = append(all, msgs...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Timestamp.Before(all[j].Timestamp) })
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all, nil
}

// Peers implements Store.
func (s *MemStore) Peers() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	peers := make([]string, 0, len(s.byPeer))
	for p := range s.byPeer {
		peers = append(peers, p)
	}
	return peers, nil
}

// AppendGroup implements Store.
func (s *MemStore) AppendGroup(msg GroupMessage) error {
	if msg.GroupID == "" {
		return ErrEmptyPeer
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}
	if s.limits.MaxMessageSize > 0 && len(msg.Text) > s.limits.MaxMessageSize {
		return ErrTooLarge
	}
	if s.limits.WhitelistOnly && !s.limits.Whitelist[msg.GroupID] {
		return ErrNotWhitelisted
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.acceptRateLocked(msg.GroupID) {
		return ErrRateLimited
	}
	if s.limits.TotalCapBytes > 0 && s.totalBytes >= s.limits.TotalCapBytes {
		return ErrStorageFull
	}

	msgs := append(s.byGroup[msg.GroupID], msg)
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].Timestamp.Before(msgs[j].Timestamp) })
	s.totalBytes += msgSize(msg)

	if s.limits.PerPeerCap > 0 && len(msgs) > s.limits.PerPeerCap {
		drop := len(msgs) - s.limits.PerPeerCap
		for _, ev := range msgs[:drop] {
			s.totalBytes -= msgSize(ev)
		}
		msgs = append([]GroupMessage(nil), msgs[drop:]...)
	}
	s.byGroup[msg.GroupID] = msgs
	return nil
}

// ListByGroup implements Store.
func (s *MemStore) ListByGroup(groupID string, limit int) ([]GroupMessage, error) {
	if groupID == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return tailCopyGroup(s.byGroup[groupID], limit), nil
}

// ListGroupBefore implements Store. Returns up to limit messages strictly
// older than `before`, newest last — matching BoltStore's exclusive-cursor
// semantics so the two backends page identically.
//
// The slice is already in insertion (chronological) order, so this is a
// filter plus a tail rather than a cursor walk. The mem store is bounded
// by PerPeerCap anyway, which is why the linear scan the bolt backend
// avoids is fine here.
func (s *MemStore) ListGroupBefore(groupID string, before time.Time, limit int) ([]GroupMessage, error) {
	if groupID == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.byGroup[groupID]
	if len(src) == 0 {
		return nil, nil
	}
	// A zero cursor means "from the newest", so the whole slice is in
	// scope and this degenerates to ListByGroup.
	if before.IsZero() || before.UnixNano() <= 0 {
		return tailCopyGroup(src, limit), nil
	}
	var older []GroupMessage
	for _, m := range src {
		if m.Timestamp.Before(before) {
			older = append(older, m)
		}
	}
	return tailCopyGroup(older, limit), nil
}

// ListGroupSince implements Store. Returns messages strictly newer than
// `since`, oldest first — matching BoltStore's exclusive-cursor semantics.
func (s *MemStore) ListGroupSince(groupID string, since time.Time) ([]GroupMessage, error) {
	if groupID == "" {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.byGroup[groupID]
	if len(src) == 0 {
		return nil, nil
	}
	var out []GroupMessage
	for _, m := range src {
		if since.IsZero() || m.Timestamp.After(since) {
			out = append(out, m)
		}
	}
	return out, nil
}

// Groups implements Store.
func (s *MemStore) Groups() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	groups := make([]string, 0, len(s.byGroup))
	for g := range s.byGroup {
		groups = append(groups, g)
	}
	return groups, nil
}

// Close implements Store. Idempotent.
func (s *MemStore) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.done)
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}

func (s *MemStore) sweepLoop() {
	defer s.wg.Done()
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
			s.sweep()
		}
	}
}

// sweep drops 1:1 messages older than the TTL. Group messages are NOT swept
// — matching BoltStore.sweep, which only walks the messages bucket.
func (s *MemStore) sweep() {
	cutoff := time.Now().UTC().Add(-s.limits.TTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	for peer, msgs := range s.byPeer {
		kept := msgs[:0:0]
		for _, m := range msgs {
			if m.Timestamp.After(cutoff) {
				kept = append(kept, m)
			} else {
				s.totalBytes -= msgSize(m)
			}
		}
		if len(kept) == 0 {
			delete(s.byPeer, peer)
		} else {
			s.byPeer[peer] = kept
		}
	}
}

// tailCopy returns a fresh slice of the most recent `limit` messages (all if
// limit <= 0), so callers can't mutate the store's backing array.
func tailCopy(src []Message, limit int) []Message {
	if len(src) == 0 {
		return nil
	}
	start := 0
	if limit > 0 && len(src) > limit {
		start = len(src) - limit
	}
	out := make([]Message, len(src)-start)
	copy(out, src[start:])
	return out
}

func tailCopyGroup(src []GroupMessage, limit int) []GroupMessage {
	if len(src) == 0 {
		return nil
	}
	start := 0
	if limit > 0 && len(src) > limit {
		start = len(src) - limit
	}
	out := make([]GroupMessage, len(src)-start)
	copy(out, src[start:])
	return out
}
