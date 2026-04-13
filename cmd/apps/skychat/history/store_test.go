// Package history store_test.go
package history

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T, limits Limits) *BoltStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skychat.db")
	s, err := NewBoltStore(path, limits)
	if err != nil {
		t.Fatalf("NewBoltStore: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Logf("store close: %v", err)
		}
	})
	return s
}

func TestBoltStore_AppendAndListByPeer(t *testing.T) {
	s := newTestStore(t, DefaultLimits())

	peer := "03abc" + strings.Repeat("0", 61)
	for i := 0; i < 5; i++ {
		if err := s.Append(Message{
			Peer:      peer,
			Text:      fmt.Sprintf("msg-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	msgs, err := s.ListByPeer(peer, 0)
	if err != nil {
		t.Fatalf("ListByPeer: %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("want 5 messages, got %d", len(msgs))
	}
	for i, m := range msgs {
		want := fmt.Sprintf("msg-%d", i)
		if m.Text != want {
			t.Errorf("msg %d: text = %q, want %q", i, m.Text, want)
		}
	}
}

func TestBoltStore_MaxMessageSize(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxMessageSize = 10
	s := newTestStore(t, lim)

	err := s.Append(Message{Peer: "abc", Text: strings.Repeat("x", 11)})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	if err := s.Append(Message{Peer: "abc", Text: "short"}); err != nil {
		t.Fatalf("short message should succeed: %v", err)
	}
}

func TestBoltStore_RateLimit(t *testing.T) {
	lim := DefaultLimits()
	lim.PerPeerRatePerMin = 3
	s := newTestStore(t, lim)

	peer := "rate-peer"
	for i := 0; i < 3; i++ {
		if err := s.Append(Message{Peer: peer, Text: "ok"}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	err := s.Append(Message{Peer: peer, Text: "overflow"})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}

	// A different peer should not be rate-limited.
	if err := s.Append(Message{Peer: "other-peer", Text: "ok"}); err != nil {
		t.Fatalf("other peer should not be rate-limited: %v", err)
	}
}

func TestBoltStore_PerPeerCap(t *testing.T) {
	lim := DefaultLimits()
	lim.PerPeerCap = 3
	lim.PerPeerRatePerMin = 0 // disable rate limit for this test
	s := newTestStore(t, lim)

	peer := "cap-peer"
	for i := 0; i < 5; i++ {
		if err := s.Append(Message{
			Peer:      peer,
			Text:      fmt.Sprintf("m%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	msgs, err := s.ListByPeer(peer, 0)
	if err != nil {
		t.Fatalf("ListByPeer: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages after cap eviction, got %d", len(msgs))
	}
	// Oldest (m0, m1) should have been evicted.
	want := []string{"m2", "m3", "m4"}
	for i, m := range msgs {
		if m.Text != want[i] {
			t.Errorf("msg %d: want %q got %q", i, want[i], m.Text)
		}
	}
}

func TestBoltStore_Whitelist(t *testing.T) {
	lim := DefaultLimits()
	lim.WhitelistOnly = true
	lim.Whitelist = map[string]bool{"allowed": true}
	s := newTestStore(t, lim)

	if err := s.Append(Message{Peer: "allowed", Text: "ok"}); err != nil {
		t.Fatalf("allowed peer should succeed: %v", err)
	}
	err := s.Append(Message{Peer: "blocked", Text: "ok"})
	if !errors.Is(err, ErrNotWhitelisted) {
		t.Fatalf("want ErrNotWhitelisted, got %v", err)
	}
}

func TestBoltStore_ListRecent(t *testing.T) {
	s := newTestStore(t, Limits{})

	now := time.Now().UTC()
	// Interleave peers to verify cross-peer ordering.
	writes := []Message{
		{Peer: "a", Text: "a1", Timestamp: now.Add(1 * time.Second)},
		{Peer: "b", Text: "b1", Timestamp: now.Add(2 * time.Second)},
		{Peer: "a", Text: "a2", Timestamp: now.Add(3 * time.Second)},
		{Peer: "b", Text: "b2", Timestamp: now.Add(4 * time.Second)},
	}
	for _, m := range writes {
		if err := s.Append(m); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.ListRecent(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("want 4 messages, got %d", len(all))
	}
	want := []string{"a1", "b1", "a2", "b2"}
	for i, m := range all {
		if m.Text != want[i] {
			t.Errorf("msg %d: want %q got %q", i, want[i], m.Text)
		}
	}

	recent, err := s.ListRecent(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].Text != "a2" || recent[1].Text != "b2" {
		t.Errorf("unexpected recent list: %+v", recent)
	}
}

func TestBoltStore_Peers(t *testing.T) {
	s := newTestStore(t, Limits{})
	for _, p := range []string{"a", "b", "c"} {
		if err := s.Append(Message{Peer: p, Text: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	peers, err := s.Peers()
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 3 {
		t.Fatalf("want 3 peers, got %d: %v", len(peers), peers)
	}
}

func TestBoltStore_EmptyPeerRejected(t *testing.T) {
	s := newTestStore(t, Limits{})
	err := s.Append(Message{Peer: "", Text: "x"})
	if !errors.Is(err, ErrEmptyPeer) {
		t.Fatalf("want ErrEmptyPeer, got %v", err)
	}
}

func TestBoltStore_TTLSweep(t *testing.T) {
	lim := Limits{
		TTL: 10 * time.Second,
	}
	s := newTestStore(t, lim)

	now := time.Now().UTC()
	// Old message — well beyond TTL
	if err := s.Append(Message{Peer: "p", Text: "old", Timestamp: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// New message — well within TTL (won't expire during test)
	if err := s.Append(Message{Peer: "p", Text: "new", Timestamp: now}); err != nil {
		t.Fatal(err)
	}

	// Manually trigger sweep instead of waiting for background ticker.
	s.sweep(context.TODO())

	msgs, err := s.ListByPeer("p", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message after sweep, got %d", len(msgs))
	}
	if msgs[0].Text != "new" {
		t.Errorf("sweep removed wrong message: %+v", msgs[0])
	}
}

func TestBoltStore_Persists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.db")
	s1, err := NewBoltStore(path, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Append(Message{Peer: "p", Text: "persistent"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := NewBoltStore(path, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close() //nolint:errcheck
	msgs, err := s2.ListByPeer("p", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Text != "persistent" {
		t.Errorf("reopened store lost data: %+v", msgs)
	}
}
